package datadog

// --- Audit trail (v2) ---

type AuditEventsResponse struct {
	Data []AuditEvent `json:"data"`
	Meta LogsMeta     `json:"meta"`
}

type AuditEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Attributes AuditEventAttrs `json:"attributes"`
}

type AuditEventAttrs struct {
	Timestamp  string                 `json:"timestamp"`
	Message    string                 `json:"message"`
	Tags       []string               `json:"tags"`
	Attributes map[string]interface{} `json:"attributes"`
}

// Product returns the audited product area (evt.name: "Monitor", "Dashboard",
// "Datadog Agent", ...).
func (a AuditEventAttrs) Product() string {
	if evt, ok := a.Attributes["evt"].(map[string]interface{}); ok {
		if name, ok := evt["name"].(string); ok {
			return name
		}
	}
	return ""
}

// Action returns what happened to the asset: created, modified, deleted...
func (a AuditEventAttrs) Action() string {
	if v, ok := a.Attributes["action"].(string); ok {
		return v
	}
	return ""
}

// ResourceName returns a best-effort human label for the changed asset.
func (a AuditEventAttrs) ResourceName() string {
	if m, ok := a.Attributes["asset"].(map[string]interface{}); ok {
		for _, key := range []string{"name", "type"} {
			if v, ok := m[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// Actor returns the user behind the change, falling back to the actor type
// (e.g. SYSTEM) for non-human events.
func (a AuditEventAttrs) Actor() string {
	if usr, ok := a.Attributes["usr"].(map[string]interface{}); ok {
		for _, key := range []string{"email", "name", "handle"} {
			if v, ok := usr[key].(string); ok && v != "" {
				return v
			}
		}
	}
	if evt, ok := a.Attributes["evt"].(map[string]interface{}); ok {
		if actor, ok := evt["actor"].(map[string]interface{}); ok {
			if v, ok := actor["type"].(string); ok {
				return v
			}
		}
	}
	return ""
}

// --- Host totals ---

type HostTotals struct {
	TotalUp     int `json:"total_up"`
	TotalActive int `json:"total_active"`
}

// --- Metric metadata ---

type MetricMetadata struct {
	Description    string `json:"description"`
	ShortName      string `json:"short_name"`
	Integration    string `json:"integration"`
	StatsdInterval int    `json:"statsd_interval"`
	Type           string `json:"type"`
	Unit           string `json:"unit"`
	PerUnit        string `json:"per_unit"`
}
