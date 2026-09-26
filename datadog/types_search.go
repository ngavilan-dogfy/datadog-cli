package datadog

import "fmt"

// --- APM spans (v2) ---

type SpansSearchRequest struct {
	Filter SpansFilter `json:"filter"`
	Sort   string      `json:"sort,omitempty"`
	Page   SpansPage   `json:"page,omitempty"`
}

type SpansFilter struct {
	Query string `json:"query"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type SpansPage struct {
	Limit int `json:"limit,omitempty"`
}

type SpansResponse struct {
	Data []SpanData `json:"data"`
	Meta SpansMeta  `json:"meta"`
}

type SpanData struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Attributes SpanAttributes `json:"attributes"`
}

type SpanAttributes struct {
	// Top-level fields per the v2 /spans/events/search response.
	StartTimestamp string `json:"start_timestamp"`
	EndTimestamp   string `json:"end_timestamp"`
	Service        string `json:"service"`
	ResourceName   string `json:"resource_name"`
	OperationName  string `json:"operation_name"`
	Env            string `json:"env"`
	Host           string `json:"host"`
	SpanID         string `json:"span_id"`
	ParentID       string `json:"parent_id"`
	TraceIDHex     string `json:"trace_id"` // 128-bit hex
	Status         string `json:"status"`
	// Why the span was kept: ingestion_reason (rule, error, auto…) and
	// retained_by (retention_filter, flex_retention…).
	IngestionReason string                 `json:"ingestion_reason"`
	RetainedBy      string                 `json:"retained_by"`
	Type            string                 `json:"type"`
	Tags            []string               `json:"tags"`
	Custom          map[string]interface{} `json:"custom"`
}

// Timestamp returns the best-effort start timestamp string.
func (a SpanAttributes) Timestamp() string {
	if a.StartTimestamp != "" {
		return a.StartTimestamp
	}
	return a.EndTimestamp
}

// DurationNS returns the span duration in nanoseconds, sourced from
// attributes.custom.duration when present (Datadog ships it there).
func (a SpanAttributes) DurationNS() int64 {
	if a.Custom == nil {
		return 0
	}
	switch v := a.Custom["duration"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// TraceID extracts the trace ID from custom.trace_id when present.
func (a SpanAttributes) TraceID() string {
	if a.TraceIDHex != "" {
		return a.TraceIDHex
	}
	if a.Custom == nil {
		return ""
	}
	if v, ok := a.Custom["trace_id"].(string); ok {
		return v
	}
	// Some payloads place it as a number
	if v, ok := a.Custom["trace_id"].(float64); ok {
		return fmt.Sprintf("%.0f", v)
	}
	return ""
}

type SpansMeta struct {
	Page SpansMetaPage `json:"page"`
}

type SpansMetaPage struct {
	After string `json:"after"`
}

// --- RUM events (v2) ---

type RUMSearchRequest struct {
	Filter RUMFilter `json:"filter"`
	Sort   string    `json:"sort,omitempty"`
	Page   RUMPage   `json:"page,omitempty"`
}

type RUMFilter struct {
	Query string `json:"query"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type RUMPage struct {
	Limit int `json:"limit,omitempty"`
}

type RUMResponse struct {
	Data []RUMEvent `json:"data"`
	Meta RUMMeta    `json:"meta"`
}

type RUMEvent struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Attributes RUMAttributes `json:"attributes"`
}

type RUMAttributes struct {
	Timestamp  string                 `json:"timestamp"`
	Service    string                 `json:"service"`
	Tags       []string               `json:"tags"`
	Attributes map[string]interface{} `json:"attributes"`
}

type RUMMeta struct {
	Page RUMMetaPage `json:"page"`
}

type RUMMetaPage struct {
	After string `json:"after"`
}

// --- Logs aggregate (v2) ---

type LogsAggregateRequest struct {
	Filter  LogsFilter             `json:"filter"`
	Compute []LogsAggregateCompute `json:"compute"`
	GroupBy []LogsAggregateGroupBy `json:"group_by,omitempty"`
}

type LogsAggregateCompute struct {
	Aggregation string `json:"aggregation"`
	Type        string `json:"type"`
	Metric      string `json:"metric,omitempty"`
}

type LogsAggregateGroupBy struct {
	Facet string             `json:"facet"`
	Limit int                `json:"limit,omitempty"`
	Sort  *LogsAggregateSort `json:"sort,omitempty"`
}

type LogsAggregateSort struct {
	Aggregation string `json:"aggregation"`
	Order       string `json:"order"`
}

type LogsAggregateResponse struct {
	Data LogsAggregateData `json:"data"`
}

type LogsAggregateData struct {
	Buckets []LogsAggregateBucket `json:"buckets"`
}

type LogsAggregateBucket struct {
	By       map[string]string      `json:"by"`
	Computes map[string]interface{} `json:"computes"`
}

// --- Monitor /search ---

type MonitorSearchResponse struct {
	Monitors []MonitorSearchHit `json:"monitors"`
	Counts   MonitorSearchCount `json:"counts"`
	Metadata MonitorSearchMeta  `json:"metadata"`
}

type MonitorSearchHit struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Status        string   `json:"status"`
	Tags          []string `json:"tags"`
	Notifications []struct {
		Name   string `json:"name"`
		Handle string `json:"handle"`
	} `json:"notifications"`
	Last_triggered_ts int64 `json:"last_triggered_ts"`
}

type MonitorSearchCount struct {
	Status []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"status"`
}

// Matches the real /api/v1/monitor/search response, where page fields are
// flat numbers on metadata (not a nested page object).
type MonitorSearchMeta struct {
	Page       int `json:"page"`
	PageCount  int `json:"page_count"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
}
