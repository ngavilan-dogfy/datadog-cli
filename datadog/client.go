package datadog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserAgent is sent on every request; cmd overwrites it with the build version.
var UserAgent = "datadog-cli"

type Client struct {
	apiURL     string
	appURL     string
	httpClient *http.Client
	apiKey     string
	appKey     string
}

func NewClient(apiURL, appURL, apiKey, appKey string) *Client {
	return &Client{
		apiURL:     strings.TrimRight(apiURL, "/"),
		appURL:     strings.TrimRight(appURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
		appKey:     appKey,
	}
}

func (c *Client) BrowseURL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.appURL + path
}

const maxRetries = 3

func (c *Client) do(method, path string, body interface{}) ([]byte, error) {
	var bodyData []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyData = data
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		var bodyReader io.Reader
		if bodyData != nil {
			bodyReader = bytes.NewReader(bodyData)
		}

		req, err := http.NewRequest(method, c.apiURL+path, bodyReader)
		if err != nil {
			return nil, err
		}

		req.Header.Set("DD-API-KEY", c.apiKey)
		req.Header.Set("DD-APPLICATION-KEY", c.appKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", UserAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(backoff(attempt))
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			time.Sleep(backoff(attempt))
			continue
		}

		switch {
		case resp.StatusCode == 429:
			lastErr = fmt.Errorf("rate limited (429): %s", apiErrorMessage(respBody))
			time.Sleep(retryAfter(resp, attempt))
			continue
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("API error %d: %s", resp.StatusCode, apiErrorMessage(respBody))
			time.Sleep(backoff(attempt))
			continue
		case resp.StatusCode == 403:
			msg := "forbidden (403) — your keys can't read this; 'datadog doctor' shows what they can access"
			if detail := apiErrorMessage(respBody); detail != "" && detail != "Forbidden" && len(detail) < 300 {
				msg += ": " + detail
			}
			return nil, fmt.Errorf("%s", msg)
		case resp.StatusCode == 401:
			return nil, fmt.Errorf("unauthorized (401) — the keys aren't valid; run 'datadog setup'")
		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, apiErrorMessage(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("giving up after %d retries: %w", maxRetries, lastErr)
}

func backoff(attempt int) time.Duration {
	return time.Duration(attempt+1) * 500 * time.Millisecond
}

// retryAfter honors the Retry-After header on 429s, capped at 30s.
func retryAfter(resp *http.Response, attempt int) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			d := time.Duration(secs) * time.Second
			if d > 30*time.Second {
				d = 30 * time.Second
			}
			return d
		}
	}
	return backoff(attempt)
}

// apiErrorMessage extracts Datadog's {"errors": [...]} body into a readable
// message. Handles both shapes the API uses — plain strings (v1) and
// {title, detail} objects (v2) — falling back to the truncated raw body.
func apiErrorMessage(body []byte) string {
	var e struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err == nil && len(e.Errors) > 0 {
		var msgs []string
		for _, raw := range e.Errors {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				msgs = append(msgs, s)
				continue
			}
			var obj struct {
				Title  string `json:"title"`
				Detail string `json:"detail"`
			}
			if json.Unmarshal(raw, &obj) == nil {
				if obj.Detail != "" {
					msgs = append(msgs, obj.Detail)
				} else if obj.Title != "" {
					msgs = append(msgs, obj.Title)
				}
			}
		}
		if len(msgs) > 0 {
			return strings.Join(msgs, "; ")
		}
	}
	return truncate(string(body), 2000)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Validate ---

func (c *Client) Validate() (*ValidateResponse, error) {
	data, err := c.do("GET", "/api/v1/validate", nil)
	if err != nil {
		return nil, err
	}
	var result ValidateResponse
	return &result, json.Unmarshal(data, &result)
}

// --- Monitors ---

func (c *Client) ListMonitors(query string, limit int) ([]Monitor, error) {
	params := url.Values{}
	if query != "" {
		params.Set("name", query)
	}
	if limit > 0 {
		params.Set("page_size", strconv.Itoa(limit))
	}

	path := "/api/v1/monitor"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	data, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var monitors []Monitor
	return monitors, json.Unmarshal(data, &monitors)
}

func (c *Client) GetMonitor(id int64) (*Monitor, error) {
	data, err := c.do("GET", fmt.Sprintf("/api/v1/monitor/%d", id), nil)
	if err != nil {
		return nil, err
	}

	var monitor Monitor
	return &monitor, json.Unmarshal(data, &monitor)
}

func (c *Client) MuteMonitor(id int64, end int64) error {
	body := map[string]interface{}{}
	if end > 0 {
		body["end"] = end
	}
	_, err := c.do("POST", fmt.Sprintf("/api/v1/monitor/%d/mute", id), body)
	return err
}

func (c *Client) UnmuteMonitor(id int64) error {
	_, err := c.do("POST", fmt.Sprintf("/api/v1/monitor/%d/unmute", id), nil)
	return err
}

// --- Dashboards ---

func (c *Client) ListDashboards() ([]DashboardSummary, error) {
	data, err := c.do("GET", "/api/v1/dashboard", nil)
	if err != nil {
		return nil, err
	}

	var result DashboardListResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Dashboards, nil
}

// --- Hosts ---

func (c *Client) ListHosts(filter string, limit int) (*HostsResponse, error) {
	params := url.Values{}
	if filter != "" {
		params.Set("filter", filter)
	}
	if limit > 0 {
		params.Set("count", strconv.Itoa(limit))
	}
	params.Set("include_muted_hosts_data", "true")

	path := "/api/v1/hosts?" + params.Encode()
	data, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result HostsResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) MuteHost(hostname string, end int64, message string) error {
	body := map[string]interface{}{}
	if end > 0 {
		body["end"] = end
	}
	if message != "" {
		body["message"] = message
	}
	_, err := c.do("POST", "/api/v1/host/"+hostname+"/mute", body)
	return err
}

func (c *Client) UnmuteHost(hostname string) error {
	_, err := c.do("POST", "/api/v1/host/"+hostname+"/unmute", nil)
	return err
}

// --- Events ---

func (c *Client) ListEvents(start, end int64, priority string) ([]Event, error) {
	params := url.Values{}
	params.Set("start", strconv.FormatInt(start, 10))
	params.Set("end", strconv.FormatInt(end, 10))
	if priority != "" {
		params.Set("priority", priority)
	}

	data, err := c.do("GET", "/api/v1/events?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result EventsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Events, nil
}

func (c *Client) PostEvent(req PostEventRequest) (*Event, error) {
	data, err := c.do("POST", "/api/v1/events", req)
	if err != nil {
		return nil, err
	}

	var result PostEventResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Event, nil
}

// --- Logs ---

func (c *Client) SearchLogs(query string, from, to string, limit int) (*LogsResponse, error) {
	return c.SearchLogsCursor(query, from, to, limit, "")
}

// --- Downtimes (v2) ---

func (c *Client) ListDowntimes() ([]DowntimeData, error) {
	data, err := c.do("GET", "/api/v2/downtime", nil)
	if err != nil {
		return nil, err
	}

	var result DowntimeResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *Client) ScheduleDowntime(scope, message string, start, end time.Time, monitorID *int64) (*DowntimeData, error) {
	attrs := CreateDowntimeAttributes{
		Scope:   scope,
		Message: message,
		Schedule: &DowntimeSchedule{
			Start: start.Format(time.RFC3339),
		},
		MuteFirstRecoveryNotification: true,
	}
	if !end.IsZero() {
		endStr := end.Format(time.RFC3339)
		attrs.Schedule.End = &endStr
	}
	if monitorID != nil {
		attrs.MonitorIdentifier = &DowntimeMonitorID{MonitorID: monitorID}
	}

	req := CreateDowntimeRequest{
		Data: CreateDowntimeData{
			Type:       "downtime",
			Attributes: attrs,
		},
	}

	data, err := c.do("POST", "/api/v2/downtime", req)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data DowntimeData `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

func (c *Client) CancelDowntime(id string) error {
	_, err := c.do("DELETE", "/api/v2/downtime/"+id, nil)
	return err
}

// --- Incidents (v2) ---

func (c *Client) ListIncidents() ([]IncidentData, error) {
	data, err := c.do("GET", "/api/v2/incidents", nil)
	if err != nil {
		return nil, err
	}

	var result IncidentsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *Client) GetIncident(id string) (*IncidentData, error) {
	data, err := c.do("GET", "/api/v2/incidents/"+id, nil)
	if err != nil {
		return nil, err
	}

	var result IncidentDetailResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// --- SLOs ---

func (c *Client) ListSLOs(query string) ([]SLO, error) {
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	params.Set("limit", "100")

	path := "/api/v1/slo?" + params.Encode()
	data, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result SLOListResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *Client) GetSLO(id string) (*SLO, error) {
	data, err := c.do("GET", "/api/v1/slo/"+id, nil)
	if err != nil {
		return nil, err
	}

	var result SLODetailResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// --- Metrics ---

func (c *Client) SearchMetrics(query string) ([]string, error) {
	params := url.Values{}
	params.Set("q", "metrics:"+query)

	data, err := c.do("GET", "/api/v1/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result MetricsSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Results.Metrics, nil
}

func (c *Client) QueryMetrics(query string, from, to int64) (*MetricsQueryResponse, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("from", strconv.FormatInt(from, 10))
	params.Set("to", strconv.FormatInt(to, 10))

	data, err := c.do("GET", "/api/v1/query?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result MetricsQueryResponse
	return &result, json.Unmarshal(data, &result)
}
