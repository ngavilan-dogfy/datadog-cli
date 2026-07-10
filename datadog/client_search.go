package datadog

import (
	"encoding/json"
	"fmt"
)

// --- APM traces / spans search (v2) ---
//
// The spans endpoint uses a JSON:API envelope (data.attributes), unlike logs
// which accepts a flat body. We wrap silently so callers don't need to care.

func (c *Client) SearchSpans(query, from, to string, limit int) (*SpansResponse, error) {
	if limit == 0 {
		limit = 25
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "search_request",
			"attributes": map[string]interface{}{
				"filter": map[string]interface{}{"from": from, "to": to, "query": query},
				"sort":   "-timestamp",
				"page":   map[string]interface{}{"limit": limit},
			},
		},
	}
	data, err := c.do("POST", "/api/v2/spans/events/search", body)
	if err != nil {
		return nil, err
	}
	var result SpansResponse
	return &result, json.Unmarshal(data, &result)
}

// --- RUM events search (v2) ---
//
// RUM also uses the JSON:API envelope. Returns "No valid indexes specified"
// if RUM is not enabled for the org — surface that error verbatim.

func (c *Client) SearchRUM(query, from, to string, limit int) (*RUMResponse, error) {
	if limit == 0 {
		limit = 25
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "search_request",
			"attributes": map[string]interface{}{
				"filter": map[string]interface{}{"from": from, "to": to, "query": query},
				"sort":   "-timestamp",
				"page":   map[string]interface{}{"limit": limit},
			},
		},
	}
	data, err := c.do("POST", "/api/v2/rum/events/search", body)
	if err != nil {
		return nil, err
	}
	var result RUMResponse
	return &result, json.Unmarshal(data, &result)
}

// --- Logs analytics aggregate (v2) ---

// AggregateLogs runs a count() aggregation grouped by the given facets.
// If groupBy is empty, a single total count is returned in result.Data.Buckets[0].
func (c *Client) AggregateLogs(query, from, to string, groupBy []string, limit int) (*LogsAggregateResponse, error) {
	if limit == 0 {
		limit = 50
	}
	groups := make([]LogsAggregateGroupBy, 0, len(groupBy))
	for _, f := range groupBy {
		if f == "" {
			continue
		}
		groups = append(groups, LogsAggregateGroupBy{
			Facet: f,
			Limit: limit,
		})
	}
	req := LogsAggregateRequest{
		Filter:  LogsFilter{Query: query, From: from, To: to},
		Compute: []LogsAggregateCompute{{Aggregation: "count", Type: "total"}},
		GroupBy: groups,
	}
	data, err := c.do("POST", "/api/v2/logs/analytics/aggregate", req)
	if err != nil {
		return nil, err
	}
	var result LogsAggregateResponse
	return &result, json.Unmarshal(data, &result)
}

// --- Monitors: search via /monitor/search (returns triggered status / counts) ---

func (c *Client) SearchMonitorsRich(query string, perPage int) (*MonitorSearchResponse, error) {
	if perPage <= 0 {
		perPage = 50
	}
	path := fmt.Sprintf("/api/v1/monitor/search?per_page=%d", perPage)
	if query != "" {
		path += "&query=" + urlEscape(query)
	}
	data, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result MonitorSearchResponse
	return &result, json.Unmarshal(data, &result)
}

// Tiny helper so callers don't need to import net/url.
func urlEscape(s string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.~"
	out := make([]byte, 0, len(s)*3)
	for _, c := range []byte(s) {
		isSafe := false
		for i := 0; i < len(safe); i++ {
			if c == safe[i] {
				isSafe = true
				break
			}
		}
		if isSafe {
			out = append(out, c)
		} else {
			out = append(out, '%')
			out = append(out, hexDigit(c>>4))
			out = append(out, hexDigit(c&0xF))
		}
	}
	return string(out)
}

func hexDigit(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + n - 10
}
