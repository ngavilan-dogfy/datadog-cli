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
	return c.SearchSpansPage(query, from, to, limit, "-timestamp", "")
}

// SearchSpansPage is SearchSpans with a sort ("timestamp" or "-timestamp")
// and a cursor: pass Meta.Page.After of the previous page to continue.
func (c *Client) SearchSpansPage(query, from, to string, limit int, sort, cursor string) (*SpansResponse, error) {
	if limit == 0 {
		limit = 25
	}
	page := map[string]interface{}{"limit": limit}
	if cursor != "" {
		page["cursor"] = cursor
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "search_request",
			"attributes": map[string]interface{}{
				"filter": map[string]interface{}{"from": from, "to": to, "query": query},
				"sort":   sort,
				"page":   page,
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

// SpanCount is one bucket of a spans aggregation.
type SpanCount struct {
	By    map[string]string
	Count int
}

// AggregateSpans counts indexed spans matching query, grouped by facets
// (service, status, resource_name…): up to limit values per facet, and
// always 5 for status. Datadog refuses more than 10,000 groups in all.
func (c *Client) AggregateSpans(query, from, to string, groupBy []string, limit int) ([]SpanCount, error) {
	if limit <= 0 {
		limit = 100
	}
	groups := make([]map[string]interface{}, 0, len(groupBy))
	total := 1
	for _, f := range groupBy {
		n := limit
		if f == "status" || f == "env" {
			n = 5
			if f == "env" {
				n = 20
			}
		}
		for total*n > 10000 && n > 1 {
			n /= 2
		}
		total *= n
		groups = append(groups, map[string]interface{}{"facet": f, "limit": n})
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "aggregate_request",
			"attributes": map[string]interface{}{
				"filter":   map[string]interface{}{"from": from, "to": to, "query": query},
				"compute":  []map[string]interface{}{{"aggregation": "count", "type": "total"}},
				"group_by": groups,
			},
		},
	}
	data, err := c.do("POST", "/api/v2/spans/analytics/aggregate", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			Attributes struct {
				By      map[string]interface{} `json:"by"`
				Compute map[string]float64     `json:"compute"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	out := make([]SpanCount, 0, len(result.Data))
	for _, b := range result.Data {
		sc := SpanCount{By: map[string]string{}}
		for k, v := range b.Attributes.By {
			sc.By[k] = fmt.Sprint(v)
		}
		for _, v := range b.Attributes.Compute {
			sc.Count = int(v)
		}
		out = append(out, sc)
	}
	return out, nil
}

// CountLogsSeries counts the logs matching query per interval (1m, 5m,
// 1h…): one request for a volume curve. Empty buckets are left out.
func (c *Client) CountLogsSeries(query, from, to, interval string) ([]LogsCountPoint, error) {
	req := LogsAggregateRequest{
		Filter:  LogsFilter{Query: query, From: from, To: to},
		Compute: []LogsAggregateCompute{{Aggregation: "count", Type: "timeseries", Interval: interval}},
	}
	data, err := c.do("POST", "/api/v2/logs/analytics/aggregate", req)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			Buckets []struct {
				Computes map[string][]LogsCountPoint `json:"computes"`
			} `json:"buckets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var out []LogsCountPoint
	for _, b := range result.Data.Buckets {
		for _, pts := range b.Computes {
			out = append(out, pts...)
		}
	}
	return out, nil
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
