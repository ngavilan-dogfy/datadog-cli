package datadog

import (
	"fmt"
	"time"
)

// --- Validate ---

type ValidateResponse struct {
	Valid bool `json:"valid"`
}

// --- Monitors ---

type Monitor struct {
	ID           int64           `json:"id"`
	OrgID        int64           `json:"org_id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Query        string          `json:"query"`
	Message      string          `json:"message"`
	Tags         []string        `json:"tags"`
	OverallState string          `json:"overall_state"`
	Priority     *int            `json:"priority"`
	Created      string          `json:"created"`
	Modified     string          `json:"modified"`
	Creator      Creator         `json:"creator"`
	Options      MonitorOptions  `json:"options"`
	Multi        bool            `json:"multi"`
	Deleted      *string         `json:"deleted"`
	MatchingDowntimes []MatchingDowntime `json:"matching_downtimes"`
}

type Creator struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Handle string `json:"handle"`
	ID     int64  `json:"id"`
}

type MonitorOptions struct {
	Thresholds    map[string]interface{} `json:"thresholds"`
	NotifyNoData  bool                   `json:"notify_no_data"`
	NotifyAudit   bool                   `json:"notify_audit"`
	TimeoutH      *int                   `json:"timeout_h"`
	EscalationMsg string                 `json:"escalation_message"`
}

type MatchingDowntime struct {
	ID    int64 `json:"id"`
	Scope []string `json:"scope"`
	End   *int64 `json:"end"`
}

// --- Dashboards ---

type DashboardSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	AuthorHandle string `json:"author_handle"`
	LayoutType   string `json:"layout_type"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at"`
	ModifiedAt   string `json:"modified_at"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type DashboardListResponse struct {
	Dashboards []DashboardSummary `json:"dashboards"`
}

// --- Hosts ---

type Host struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Apps            []string `json:"apps"`
	IsMuted         bool     `json:"is_muted"`
	LastReportedTime int64   `json:"last_reported_time"`
	Meta            HostMeta `json:"meta"`
	Sources         []string `json:"sources"`
	Up              bool     `json:"up"`
	HostName        string   `json:"host_name"`
	MuteTimeout     *int64   `json:"mute_timeout"`
}

type HostMeta struct {
	Platform     string `json:"platform"`
	Processor    string `json:"processor"`
	AgentVersion string `json:"agent_version"`
	GoVersion    string `json:"goV"`
}

type HostsResponse struct {
	HostList      []Host `json:"host_list"`
	TotalReturned int    `json:"total_returned"`
	TotalMatching int    `json:"total_matching"`
}

// --- Events ---

type Event struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"`
	Text         string   `json:"text"`
	DateHappened int64    `json:"date_happened"`
	Priority     string   `json:"priority"`
	AlertType    string   `json:"alert_type"`
	Source       string   `json:"source_type_name"`
	Tags         []string `json:"tags"`
	Host         string   `json:"host"`
	URL          string   `json:"url"`
}

type EventsResponse struct {
	Events []Event `json:"events"`
}

type PostEventRequest struct {
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	Priority  string   `json:"priority,omitempty"`
	AlertType string   `json:"alert_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Host      string   `json:"host,omitempty"`
}

type PostEventResponse struct {
	Event Event `json:"event"`
}

// --- Logs ---

type LogsSearchRequest struct {
	Filter LogsFilter `json:"filter"`
	Sort   string     `json:"sort,omitempty"`
	Page   LogsPage   `json:"page,omitempty"`
}

type LogsFilter struct {
	Query string `json:"query"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type LogsPage struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type LogsResponse struct {
	Data []LogData `json:"data"`
	Meta LogsMeta  `json:"meta"`
}

type LogData struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Attributes LogAttributes `json:"attributes"`
}

type LogAttributes struct {
	Timestamp  string                 `json:"timestamp"`
	Host       string                 `json:"host"`
	Service    string                 `json:"service"`
	Message    string                 `json:"message"`
	Status     string                 `json:"status"`
	Tags       []string               `json:"tags"`
	Attributes map[string]interface{} `json:"attributes"`
}

type LogsMeta struct {
	Page LogsMetaPage `json:"page"`
}

type LogsMetaPage struct {
	After string `json:"after"`
}

// --- Downtimes (v2) ---

type DowntimeResponse struct {
	Data []DowntimeData `json:"data"`
}

type DowntimeData struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes DowntimeAttributes `json:"attributes"`
}

type DowntimeAttributes struct {
	Scope             string                  `json:"scope"`
	Message           *string                 `json:"message"`
	MonitorIdentifier *DowntimeMonitorID      `json:"monitor_identifier"`
	Schedule          *DowntimeSchedule       `json:"schedule"`
	Status            string                  `json:"status"`
	DisplayTimezone   string                  `json:"display_timezone"`
	CreatedAt         string                  `json:"created_at"`
	ModifiedAt        string                  `json:"modified_at"`
	Canceled          *string                 `json:"canceled"`
	MuteFirstRecoveryNotification bool        `json:"mute_first_recovery_notification"`
}

type DowntimeMonitorID struct {
	MonitorID   *int64  `json:"monitor_id"`
	MonitorTags []string `json:"monitor_tags"`
}

type DowntimeSchedule struct {
	Start       string  `json:"start"`
	End         *string `json:"end"`
	Timezone    string  `json:"timezone"`
}

type CreateDowntimeRequest struct {
	Data CreateDowntimeData `json:"data"`
}

type CreateDowntimeData struct {
	Type       string                   `json:"type"`
	Attributes CreateDowntimeAttributes `json:"attributes"`
}

type CreateDowntimeAttributes struct {
	Scope             string              `json:"scope"`
	Message           string              `json:"message,omitempty"`
	MonitorIdentifier *DowntimeMonitorID  `json:"monitor_identifier,omitempty"`
	Schedule          *DowntimeSchedule   `json:"schedule,omitempty"`
	MuteFirstRecoveryNotification bool    `json:"mute_first_recovery_notification"`
}

// --- Incidents (v2) ---

type IncidentsResponse struct {
	Data []IncidentData `json:"data"`
	Meta *PaginationMeta `json:"meta"`
}

type IncidentDetailResponse struct {
	Data IncidentData `json:"data"`
}

type IncidentData struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Attributes IncidentAttributes  `json:"attributes"`
}

type IncidentAttributes struct {
	Title             string  `json:"title"`
	Status            string  `json:"status"` // active, stable, resolved
	Severity          string  `json:"severity"`
	Created           string  `json:"created"`
	Modified          string  `json:"modified"`
	CustomerImpacted  bool    `json:"customer_impact_scope_is_set"`
	CustomerImpactScope string `json:"customer_impact_scope"`
	Detected          string  `json:"detected"`
	Resolved          *string `json:"resolved"`
	TimeToDetect      *int64  `json:"time_to_detect"`
	TimeToRepair      *int64  `json:"time_to_repair"`
	CommanderUser     *IncidentUser `json:"commander_user"`
}

type IncidentUser struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

type PaginationMeta struct {
	Pagination Pagination `json:"pagination"`
}

type Pagination struct {
	Offset int `json:"offset"`
	Size   int `json:"size"`
}

// --- SLOs ---

type SLOListResponse struct {
	Data []SLO `json:"data"`
}

type SLODetailResponse struct {
	Data SLO `json:"data"`
}

type SLO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags"`
	Type        string         `json:"type"` // metric, monitor
	Thresholds  []SLOThreshold `json:"thresholds"`
	Creator     Creator        `json:"creator"`
	CreatedAt   int64          `json:"created_at"`
	ModifiedAt  int64          `json:"modified_at"`
	OverallStatus []SLOOverallStatus `json:"overall_status"`
}

type SLOThreshold struct {
	Target      float64  `json:"target"`
	Timeframe   string   `json:"timeframe"` // 7d, 30d, 90d
	TargetDisplay string `json:"target_display"`
	Warning     *float64 `json:"warning"`
}

type SLOOverallStatus struct {
	SLI                float64 `json:"sli_value"`
	Target             float64 `json:"target"`
	Timeframe          string  `json:"timeframe"`
	ErrorBudgetRemaining float64 `json:"error_budget_remaining"`
	Status             string  `json:"status"` // OK, WARNING, BREACHED
	SpanPrecision      int     `json:"span_precision"`
}

// --- Metrics ---

type MetricsSearchResponse struct {
	Results MetricsResults `json:"results"`
}

type MetricsResults struct {
	Metrics []string `json:"metrics"`
}

type MetricsQueryResponse struct {
	Series []MetricsSeries `json:"series"`
	Status string          `json:"status"`
	FromDate int64         `json:"from_date"`
	ToDate   int64         `json:"to_date"`
	Query    string        `json:"query"`
}

type MetricsSeries struct {
	Metric     string      `json:"metric"`
	Pointlist  [][]float64 `json:"pointlist"`
	Scope      string      `json:"scope"`
	Expression string      `json:"expression"`
	DisplayName string     `json:"display_name"`
	Unit       []MetricsUnit `json:"unit"`
	Start      int64       `json:"start"`
	End        int64       `json:"end"`
	Interval   int         `json:"interval"`
	Length     int         `json:"length"`
}

type MetricsUnit struct {
	Name   string `json:"name"`
	Family string `json:"family"`
}

// --- Time helpers ---

func ParseTime(s string) time.Time {
	layouts := []string{
		"2006-01-02T15:04:05.000000+00:00",
		"2006-01-02T15:04:05+00:00",
		"2006-01-02T15:04:05.000-0700",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func RelativeTime(s string) string {
	t := ParseTime(s)
	if t.IsZero() {
		return s
	}
	return RelativeTimeSince(t)
}

func RelativeTimeSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	default:
		return t.Format("Jan 02")
	}
}

func UnixRelativeTime(unix int64) string {
	if unix == 0 {
		return "—"
	}
	t := time.Unix(unix, 0)
	return RelativeTimeSince(t)
}

func FormatUnix(unix int64) string {
	if unix == 0 {
		return "—"
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}
