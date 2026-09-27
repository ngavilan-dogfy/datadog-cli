package datadog

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// EventV2 is one event from the v2 events search: monitor transitions,
// resource changes (a Cloud Run revision, a config change), deploys and
// integration or custom events, in one shape.
type EventV2 struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Title     string    `json:"title"`
	Message   string    `json:"message,omitempty"`
	Source    string    `json:"source,omitempty"`   // the "source:" tag: alert, resource_changes, github…
	Category  string    `json:"category,omitempty"` // alert, change, deployment…
	Tags      []string  `json:"tags,omitempty"`
	// Monitor is set on monitor transitions (source:alert).
	Monitor *EventMonitor `json:"monitor,omitempty"`
	// Changed names what changed, on change events ("checkout-00042-kx7").
	Changed string `json:"changed,omitempty"`
}

// EventMonitor is the monitor a transition event is about.
type EventMonitor struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Query     string   `json:"query,omitempty"`
	FromState string   `json:"from_state,omitempty"`
	ToState   string   `json:"to_state,omitempty"`
	Priority  int      `json:"priority,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Groups    []string `json:"groups,omitempty"`
}

// Tag returns the value of a "key:value" tag on the event.
func (e EventV2) Tag(key string) string {
	for _, t := range e.Tags {
		if v, ok := strings.CutPrefix(t, key+":"); ok {
			return v
		}
	}
	return ""
}

// SearchEvents searches events (POST /api/v2/events/search), newest
// first, following pages up to limit. from and to take RFC3339 or Datadog's
// relative times ("now-7d").
func (c *Client) SearchEvents(query, from, to string, limit int) ([]EventV2, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []EventV2
	cursor := ""
	for len(out) < limit {
		page := map[string]interface{}{"limit": min(1000, limit-len(out))}
		if cursor != "" {
			page["cursor"] = cursor
		}
		body := map[string]interface{}{
			"filter": map[string]interface{}{"query": query, "from": from, "to": to},
			"sort":   "-timestamp",
			"page":   page,
		}
		data, err := c.do("POST", "/api/v2/events/search", body)
		if err != nil {
			return out, err
		}
		var resp struct {
			Data []json.RawMessage `json:"data"`
			Meta struct {
				Page struct {
					After string `json:"after"`
				} `json:"page"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return out, err
		}
		for _, raw := range resp.Data {
			if e, ok := parseEventV2(raw); ok {
				out = append(out, e)
			}
		}
		cursor = resp.Meta.Page.After
		if cursor == "" || len(resp.Data) == 0 {
			break
		}
	}
	return out, nil
}

func parseEventV2(raw json.RawMessage) (EventV2, bool) {
	var e struct {
		ID         string `json:"id"`
		Attributes struct {
			Timestamp  string   `json:"timestamp"`
			Message    string   `json:"message"`
			Tags       []string `json:"tags"`
			Attributes struct {
				Title           string `json:"title"`
				ChangedResource *struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"changed_resource"`
				Evt struct {
					Category string `json:"category"`
					Name     string `json:"name"`
				} `json:"evt"`
				Monitor *struct {
					ID         int64    `json:"id"`
					Name       string   `json:"name"`
					Query      string   `json:"query"`
					Priority   int      `json:"priority"`
					Tags       []string `json:"tags"`
					Groups     []string `json:"groups"`
					Transition struct {
						From string `json:"source_state"`
						To   string `json:"destination_state"`
					} `json:"transition"`
				} `json:"monitor"`
			} `json:"attributes"`
		} `json:"attributes"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return EventV2{}, false
	}
	a := e.Attributes
	ev := EventV2{
		ID:       e.ID,
		Title:    a.Attributes.Title,
		Message:  strings.TrimSpace(a.Message),
		Category: a.Attributes.Evt.Category,
		Tags:     a.Tags,
	}
	if t, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
		ev.Timestamp = t
	} else if ms, err := strconv.ParseInt(a.Timestamp, 10, 64); err == nil {
		ev.Timestamp = time.UnixMilli(ms)
	}
	if ev.Title == "" {
		ev.Title = a.Attributes.Evt.Name
	}
	if ev.Title == "" {
		ev.Title, _, _ = strings.Cut(ev.Message, "\n")
	}
	ev.Source = ev.Tag("source")
	if m := a.Attributes.Monitor; m != nil && m.ID != 0 {
		ev.Monitor = &EventMonitor{ID: m.ID, Name: m.Name, Query: m.Query, Priority: m.Priority, Tags: m.Tags, Groups: m.Groups,
			FromState: m.Transition.From, ToState: m.Transition.To}
	}
	if r := a.Attributes.ChangedResource; r != nil {
		ev.Changed = r.Name
	}
	return ev, true
}
