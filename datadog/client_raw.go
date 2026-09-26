package datadog

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RawResponse is an API answer as it came: for 'datadog api', which shows
// errors as Datadog wrote them instead of turning them into Go errors.
type RawResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// ReadOnlyRequest reports whether a request only reads data. Datadog runs
// many searches and queries as POSTs; those are reads too.
func ReadOnlyRequest(method, path string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS":
		return true
	case "POST":
		p := strings.TrimRight(strings.SplitN(path, "?", 2)[0], "/")
		return strings.HasSuffix(p, "/search") || strings.HasSuffix(p, "/aggregate") ||
			strings.HasPrefix(p, "/api/v2/query/") || p == "/api/v1/validate"
	}
	return false
}

// Raw sends any request to the API with this client's keys and site.
// Reads are retried on 429 and 5xx like every other call; writes only on
// 429 (Datadog rejected them, so nothing happened), never on 5xx, where a
// retry could apply a change twice.
func (c *Client) Raw(method, path string, query url.Values, body []byte) (*RawResponse, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := c.apiURL + path
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + query.Encode()
	}
	read := ReadOnlyRequest(method, path)
	var last *RawResponse
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequest(strings.ToUpper(method), u, r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("DD-API-KEY", c.apiKey)
		req.Header.Set("DD-APPLICATION-KEY", c.appKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if !read {
				return nil, err // it may have reached Datadog: don't send it twice
			}
			time.Sleep(backoff(attempt))
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		last, lastErr = &RawResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
		switch {
		case resp.StatusCode == 429:
			time.Sleep(retryAfter(resp, attempt))
			continue
		case resp.StatusCode >= 500 && read:
			time.Sleep(backoff(attempt))
			continue
		}
		return last, nil
	}
	if last != nil {
		return last, nil
	}
	return nil, lastErr
}
