package datadog

import (
	"encoding/json"
	"fmt"
)

// --- Monitor CRUD ---

type CreateMonitorRequest struct {
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Query    string                 `json:"query"`
	Message  string                 `json:"message,omitempty"`
	Tags     []string               `json:"tags,omitempty"`
	Priority *int                   `json:"priority,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

func (c *Client) CreateMonitor(req CreateMonitorRequest) (*Monitor, error) {
	data, err := c.do("POST", "/api/v1/monitor", req)
	if err != nil {
		return nil, err
	}

	var monitor Monitor
	return &monitor, json.Unmarshal(data, &monitor)
}

func (c *Client) DeleteMonitor(id int64) error {
	_, err := c.do("DELETE", fmt.Sprintf("/api/v1/monitor/%d", id), nil)
	return err
}

func (c *Client) UpdateMonitor(id int64, fields map[string]interface{}) error {
	_, err := c.do("PUT", fmt.Sprintf("/api/v1/monitor/%d", id), fields)
	return err
}

// MonitorOptionsRaw returns a monitor's options as Datadog stores them,
// every field included: to change some and send them all back.
func (c *Client) MonitorOptionsRaw(id int64) (map[string]interface{}, error) {
	data, err := c.do("GET", fmt.Sprintf("/api/v1/monitor/%d", id), nil)
	if err != nil {
		return nil, err
	}
	var m struct {
		Options map[string]interface{} `json:"options"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Options == nil {
		m.Options = map[string]interface{}{}
	}
	return m.Options, nil
}

// --- Incident CRUD ---

type CreateIncidentRequest struct {
	Data CreateIncidentData `json:"data"`
}

type CreateIncidentData struct {
	Type       string                   `json:"type"`
	Attributes CreateIncidentAttributes `json:"attributes"`
}

type CreateIncidentAttributes struct {
	Title            string          `json:"title"`
	CustomerImpacted bool            `json:"customer_impacted"`
	Fields           *IncidentFields `json:"fields,omitempty"`
}

type IncidentFields struct {
	Severity *IncidentFieldValue `json:"severity,omitempty"`
}

type IncidentFieldValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (c *Client) CreateIncident(title string, customerImpacted bool, severity string) (*IncidentData, error) {
	attrs := CreateIncidentAttributes{
		Title:            title,
		CustomerImpacted: customerImpacted,
	}

	if severity != "" {
		attrs.Fields = &IncidentFields{
			Severity: &IncidentFieldValue{
				Type:  "dropdown",
				Value: severity,
			},
		}
	}

	req := CreateIncidentRequest{
		Data: CreateIncidentData{
			Type:       "incidents",
			Attributes: attrs,
		},
	}

	data, err := c.do("POST", "/api/v2/incidents", req)
	if err != nil {
		return nil, err
	}

	var result IncidentDetailResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

type UpdateIncidentRequest struct {
	Data UpdateIncidentData `json:"data"`
}

type UpdateIncidentData struct {
	ID         string                   `json:"id"`
	Type       string                   `json:"type"`
	Attributes UpdateIncidentAttributes `json:"attributes"`
}

type UpdateIncidentAttributes struct {
	Title  string          `json:"title,omitempty"`
	Status string          `json:"status,omitempty"` // active, stable, resolved
	Fields *IncidentFields `json:"fields,omitempty"`
}

func (c *Client) UpdateIncident(id string, status string, title string, severity string) (*IncidentData, error) {
	attrs := UpdateIncidentAttributes{}
	if status != "" {
		attrs.Status = status
	}
	if title != "" {
		attrs.Title = title
	}
	if severity != "" {
		attrs.Fields = &IncidentFields{
			Severity: &IncidentFieldValue{
				Type:  "dropdown",
				Value: severity,
			},
		}
	}

	req := UpdateIncidentRequest{
		Data: UpdateIncidentData{
			ID:         id,
			Type:       "incidents",
			Attributes: attrs,
		},
	}

	data, err := c.do("PATCH", "/api/v2/incidents/"+id, req)
	if err != nil {
		return nil, err
	}

	var result IncidentDetailResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}
