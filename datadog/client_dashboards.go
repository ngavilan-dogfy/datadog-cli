package datadog

import (
	"encoding/json"
	"fmt"
)

// GetDashboard returns the full dashboard document (widgets, layout, etc.)
// as a generic map[string]interface{} so we don't have to mirror Datadog's
// large widget schema in Go types.
func (c *Client) GetDashboard(id string) (map[string]interface{}, error) {
	data, err := c.do("GET", "/api/v1/dashboard/"+id, nil)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// CreateDashboard POSTs a payload to /api/v1/dashboard. The payload should
// already be the bare dashboard document (no JSON:API envelope). Returns the
// created dashboard with server-assigned id/url.
func (c *Client) CreateDashboard(payload map[string]interface{}) (map[string]interface{}, error) {
	cleaned := stripServerFields(payload)
	data, err := c.do("POST", "/api/v1/dashboard", cleaned)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// UpdateDashboard replaces an existing dashboard. Strips server-managed fields
// from payload to avoid 400s.
func (c *Client) UpdateDashboard(id string, payload map[string]interface{}) (map[string]interface{}, error) {
	cleaned := stripServerFields(payload)
	data, err := c.do("PUT", "/api/v1/dashboard/"+id, cleaned)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// DeleteDashboard removes a dashboard.
func (c *Client) DeleteDashboard(id string) error {
	_, err := c.do("DELETE", "/api/v1/dashboard/"+id, nil)
	return err
}

// DashboardURL returns the browser URL for a dashboard id.
func (c *Client) DashboardURL(id string) string {
	return c.appURL + "/dashboard/" + id
}

// stripServerFields removes fields that are assigned by Datadog and rejected
// (or ignored) when posted back: id, url, author_handle, author_name,
// created_at, modified_at. Also walks widget arrays to strip widget IDs so a
// clone doesn't end up duplicating server-side widget identifiers.
func stripServerFields(in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		switch k {
		case "id", "url", "author_handle", "author_name", "created_at", "modified_at":
			continue
		case "widgets":
			out[k] = stripWidgetIDs(v)
		default:
			out[k] = v
		}
	}
	return out
}

func stripWidgetIDs(widgets interface{}) interface{} {
	arr, ok := widgets.([]interface{})
	if !ok {
		return widgets
	}
	for i, w := range arr {
		m, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		delete(m, "id")
		// Recurse into group widget definitions which contain nested widgets.
		if def, ok := m["definition"].(map[string]interface{}); ok {
			if inner, ok := def["widgets"]; ok {
				def["widgets"] = stripWidgetIDs(inner)
			}
		}
		arr[i] = m
	}
	return arr
}

// Sanity: keep fmt referenced for future format errors in this file.
var _ = fmt.Errorf
