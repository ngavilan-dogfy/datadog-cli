package datadog

import (
	"encoding/json"
	"net/url"
)

// Extra context sources: audit trail, host totals, metric metadata,
// paginated log search. These feed triage and the service dossier.

// --- Audit trail (v2) ---

func (c *Client) SearchAuditEvents(query, from, to string, limit int) ([]AuditEvent, error) {
	if limit == 0 {
		limit = 25
	}

	body := map[string]interface{}{
		"filter": map[string]interface{}{
			"query": query,
			"from":  from,
			"to":    to,
		},
		"page": map[string]interface{}{"limit": limit},
		"sort": "-timestamp",
	}

	data, err := c.do("POST", "/api/v2/audit/events/search", body)
	if err != nil {
		return nil, err
	}

	var result AuditEventsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// --- Host totals ---

func (c *Client) GetHostTotals() (*HostTotals, error) {
	data, err := c.do("GET", "/api/v1/hosts/totals", nil)
	if err != nil {
		return nil, err
	}

	var result HostTotals
	return &result, json.Unmarshal(data, &result)
}

// --- Metric metadata ---

func (c *Client) GetMetricMetadata(name string) (*MetricMetadata, error) {
	data, err := c.do("GET", "/api/v1/metrics/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}

	var result MetricMetadata
	return &result, json.Unmarshal(data, &result)
}

// --- Paginated log search ---

// SearchLogsCursor is SearchLogs plus cursor-based pagination; the returned
// response's Meta.Page.After carries the cursor for the next page ("" = done).
func (c *Client) SearchLogsCursor(query, from, to string, limit int, cursor string) (*LogsResponse, error) {
	if limit == 0 {
		limit = 25
	}

	req := LogsSearchRequest{
		Filter: LogsFilter{
			Query: query,
			From:  from,
			To:    to,
		},
		Sort: "-timestamp",
		Page: LogsPage{Limit: limit, Cursor: cursor},
	}

	data, err := c.do("POST", "/api/v2/logs/events/search", req)
	if err != nil {
		return nil, err
	}

	var result LogsResponse
	return &result, json.Unmarshal(data, &result)
}
