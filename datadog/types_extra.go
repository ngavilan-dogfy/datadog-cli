package datadog

// --- Synthetics ---

type SyntheticsTest struct {
	PublicID   string            `json:"public_id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`    // api, browser
	Subtype    string            `json:"subtype"` // http, ssl, dns, tcp, icmp, websocket, multi
	Status     string            `json:"status"`  // live, paused
	MonitorID  int64             `json:"monitor_id"`
	Tags       []string          `json:"tags"`
	Locations  []string          `json:"locations"`
	Message    string            `json:"message"`
	CreatedAt  string            `json:"created_at"`
	ModifiedAt string            `json:"modified_at"`
	Config     SyntheticsConfig  `json:"config"`
	Options    SyntheticsOptions `json:"options"`
	CreatedBy  Creator           `json:"created_by"`
}

type SyntheticsConfig struct {
	Request    SyntheticsRequest     `json:"request"`
	Assertions []SyntheticsAssertion `json:"assertions"`
}

type SyntheticsRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}

type SyntheticsAssertion struct {
	Type     string      `json:"type"`
	Operator string      `json:"operator"`
	Target   interface{} `json:"target"`
}

type SyntheticsOptions struct {
	TickEvery      int  `json:"tick_every"`
	FollowRedirects bool `json:"follow_redirects"`
	MinFailureDuration int `json:"min_failure_duration"`
	MinLocationFailed  int `json:"min_location_failed"`
}

type SyntheticsListResponse struct {
	Tests []SyntheticsTest `json:"tests"`
}

type SyntheticsTriggerRequest struct {
	Tests []SyntheticsTriggerTest `json:"tests"`
}

type SyntheticsTriggerTest struct {
	PublicID string `json:"public_id"`
}

type SyntheticsTriggerResponse struct {
	Results         []SyntheticsTriggerResult `json:"results"`
	TriggeredChecks []SyntheticsTriggerResult `json:"triggered_check_ids"`
	BatchID         string                    `json:"batch_id"`
}

type SyntheticsTriggerResult struct {
	PublicID string `json:"public_id"`
	ResultID string `json:"result_id"`
}

// --- Host Tags ---

type HostTagsResponse struct {
	Tags []string `json:"tags"`
}

// --- Service Catalog ---

type ServiceCatalogResponse struct {
	Data []ServiceCatalogEntry `json:"data"`
}

type ServiceCatalogEntry struct {
	ID         string                   `json:"id"`
	Type       string                   `json:"type"`
	Attributes ServiceCatalogAttributes `json:"attributes"`
}

type ServiceCatalogAttributes struct {
	Meta   ServiceCatalogMeta   `json:"meta"`
	Schema ServiceCatalogSchema `json:"schema"`
}

type ServiceCatalogMeta struct {
	LastModifiedTime string `json:"last-modified-time"`
}

type ServiceCatalogSchema struct {
	DDService   string        `json:"dd-service"`
	Team        string        `json:"team"`
	Description string        `json:"description"`
	Tier        string        `json:"tier"`
	Lifecycle   string        `json:"lifecycle"`
	Application string        `json:"application"`
	Languages   []string      `json:"languages"`
	Type        string        `json:"type"`
	Links       []ServiceLink `json:"links"`
	Tags        []string      `json:"tags"`
	Contacts    []ServiceContact `json:"contacts"`
}

type ServiceLink struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
}

type ServiceContact struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Contact string `json:"contact"`
}

// --- Notebooks ---

type NotebooksResponse struct {
	Data []NotebookEntry `json:"data"`
	Meta *PaginationMeta `json:"meta"`
}

type NotebookEntry struct {
	ID         int64              `json:"id"`
	Type       string             `json:"type"`
	Attributes NotebookAttributes `json:"attributes"`
}

type NotebookAttributes struct {
	Name     string  `json:"name"`
	Author   Creator `json:"author"`
	Status   string  `json:"status"` // published, draft
	Created  string  `json:"created"`
	Modified string  `json:"modified"`
}

// --- Security Signals ---

type SecuritySignalsSearchRequest struct {
	Filter SecuritySignalsFilter `json:"filter"`
	Sort   string                `json:"sort,omitempty"`
	Page   LogsPage              `json:"page,omitempty"`
}

type SecuritySignalsFilter struct {
	Query string `json:"query"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type SecuritySignalsResponse struct {
	Data []SecuritySignalData `json:"data"`
	Meta LogsMeta             `json:"meta"`
}

type SecuritySignalData struct {
	ID         string                       `json:"id"`
	Type       string                       `json:"type"`
	Attributes SecuritySignalDataAttributes `json:"attributes"`
}

type SecuritySignalDataAttributes struct {
	Message    string                 `json:"message"`
	Timestamp  string                 `json:"timestamp"`
	Tags       []string               `json:"tags"`
	Attributes map[string]interface{} `json:"attributes"`
	Status     string                 `json:"status"`
	Severity   string                 `json:"severity"`
	Title      string                 `json:"title"`
}

// --- Usage ---

type UsageSummaryResponse struct {
	Usage     []UsageSummary `json:"usage"`
	EndDate   string         `json:"end_date"`
	StartDate string         `json:"start_date"`
}

type UsageSummary struct {
	Date                string `json:"date"`
	AgentHostCount      *int64 `json:"agent_host_count"`
	ContainerCount      *int64 `json:"container_count"`
	CustomTSCount       *int64 `json:"custom_ts_count"`
	IndexedEventsCount  *int64 `json:"indexed_events_count"`
	IngestedEventsBytes *int64 `json:"ingested_events_bytes"`
	APMHostCount        *int64 `json:"apm_host_count"`
	SyntheticsCount     *int64 `json:"synthetics_check_calls_count"`
	RUMSessionCount     *int64 `json:"rum_session_count"`
	ProfilingHostCount  *int64 `json:"profiling_host_count"`
}

// --- CI Pipelines ---

type CIPipelinesResponse struct {
	Data []CIPipelineEvent `json:"data"`
}

type CIPipelineEvent struct {
	ID         string                `json:"id"`
	Type       string                `json:"type"`
	Attributes CIPipelineAttributes  `json:"attributes"`
}

type CIPipelineAttributes struct {
	Status     string `json:"status"`     // success, error, canceled, skipped
	Level      string `json:"level"`      // pipeline, stage, job
	Name       string `json:"name"`
	Service    string `json:"service"`
	Duration   *int64 `json:"duration"`   // nanoseconds
	Start      string `json:"start"`
	End        string `json:"end"`
	PipelineID string `json:"pipeline_id"`
	URL        string `json:"url"`
	Error      *CIPipelineError `json:"error"`
}

type CIPipelineError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
