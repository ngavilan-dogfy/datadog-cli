package datadog

import (
	"encoding/json"
	"fmt"
)

// MonitorGroup is the state of one group of a multi-alert monitor
// (host:web-1, service:api…).
type MonitorGroup struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	LastTriggeredTS int64  `json:"last_triggered_ts"`
	LastNodataTS    int64  `json:"last_nodata_ts"`
}

// GetMonitorGroups returns every group's state
// (GET /api/v1/monitor/{id}?group_states=all).
func (c *Client) GetMonitorGroups(id int64) ([]MonitorGroup, error) {
	data, err := c.do("GET", fmt.Sprintf("/api/v1/monitor/%d?group_states=all", id), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		State struct {
			Groups map[string]MonitorGroup `json:"groups"`
		} `json:"state"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]MonitorGroup, 0, len(resp.State.Groups))
	for name, g := range resp.State.Groups {
		g.Name = name
		out = append(out, g)
	}
	return out, nil
}
