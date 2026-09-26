package tui

import (
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	"github.com/charmbracelet/lipgloss"
)

type lipglossColor = lipgloss.TerminalColor

// API is everything the TUI asks Datadog. *datadog.Client implements it;
// tests use an in-memory fake.
type API interface {
	ListDashboards() ([]datadog.DashboardSummary, error)
	GetDashboard(id string) (map[string]interface{}, error)
	DashboardURL(id string) string
	BrowseURL(path string) string

	QueryTimeseries(datadog.FormulaRequest) (*datadog.TimeseriesResult, error)
	QueryScalar(datadog.FormulaRequest) (*datadog.ScalarResult, error)
	QueryMetrics(query string, from, to int64) (*datadog.MetricsQueryResponse, error)
	SearchMetrics(query string) ([]string, error)

	ListMonitors(query string, limit int) ([]datadog.Monitor, error)
	GetMonitor(id int64) (*datadog.Monitor, error)
	SearchMonitorsRich(query string, perPage int) (*datadog.MonitorSearchResponse, error)
	GetMonitorGroups(id int64) ([]datadog.MonitorGroup, error)
	ListDowntimes() ([]datadog.DowntimeData, error)
	ScheduleDowntime(scope, message string, start, end time.Time, monitorID *int64) (*datadog.DowntimeData, error)
	CancelDowntime(id string) error

	SearchLogs(query, from, to string, limit int) (*datadog.LogsResponse, error)
	SearchLogsCursor(query, from, to string, limit int, cursor string) (*datadog.LogsResponse, error)
	AggregateLogs(query, from, to string, groupBy []string, limit int) (*datadog.LogsAggregateResponse, error)

	ListIncidents() ([]datadog.IncidentData, error)
	ListSLOs(query string) ([]datadog.SLO, error)
	ListEvents(start, end int64, priority string) ([]datadog.Event, error)
}

var _ API = (*datadog.Client)(nil)

// requests is a semaphore: dashboards fire many queries at once, and
// Datadog (300 queries / 10 s) and the network are happier with a few in
// flight.
var requests = make(chan struct{}, 6)

func limited[T any](fn func() T) T {
	requests <- struct{}{}
	defer func() { <-requests }()
	return fn()
}
