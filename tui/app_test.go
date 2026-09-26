package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngavilan-dogfy/datadog-cli/viz"

	"github.com/charmbracelet/x/ansi"
)

// Every screen, at every size, draws exactly its box — including the
// awkward ones (tiny terminals, a zoomed widget, open details).
func TestEveryScreenFitsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 40}, {203, 61}} {
		h := newHarness(t, size[0], size[1])
		for _, tab := range []string{"1", "2", "3", "4", "5"} {
			h.keys(tab)
			h.screen()
		}
		h.keys("2<enter>") // open "Service health"
		h.screen()
		h.keys("jj<enter>") // zoom something
		h.screen()
		h.keys("<esc><esc>3<enter>") // a monitor's detail
		h.screen()
		h.keys("<esc>4<enter>") // a log's detail
		h.screen()
		h.keys("<esc>5system.cpu<tab><enter>")
		h.screen()
		h.keys("?")
		h.screen()
	}
}

func TestDashboardShowsWidgetsWithVariables(t *testing.T) {
	h := newHarness(t, 160, 48)
	h.keys("2")
	h.expect("Service health", "Billing")
	h.keys("<enter>")
	// The note, the group, the formatted query value (precision 0 + unit),
	// the chart's legend and the toplist all render.
	big, _ := viz.BigText("412", nil) // query values are drawn in big block digits
	h.expect("$env prod", "Runbook", "Traffic", ansi.Strip(big[0]), ansi.Strip(big[2]), "ms · avg",
		"Requests by host", "host:a", "Errors by endpoint", "/checkout")
	h.expect("image", "o opens it in Datadog")
	if !h.dd.sawQuery("sum:requests{env:prod} by {host}") || !h.dd.sawQuery("status:error env:prod") {
		t.Fatalf("template variables weren't applied: %q", h.dd.queries)
	}
	// Switch $env to staging: every widget asks again with the new value.
	h.keys("v<enter>")
	h.expect("staging")
	h.keys("staging<enter>")
	h.expect("$env staging")
	if !h.dd.sawQuery("sum:requests{env:staging} by {host}") {
		t.Fatalf("changing $env didn't refetch: %q", h.dd.queries)
	}
}

func TestDashboardZoomAndGroups(t *testing.T) {
	h := newHarness(t, 140, 44)
	h.keys("2<enter>")
	// Focus the group (second widget) and fold it: its children disappear.
	h.keys("j")
	h.keys("<enter>")
	h.expect("► Traffic", "3 widgets")
	h.reject("Requests by host")
	h.keys("<enter>")
	h.expect("▼ Traffic", "Requests by host")
	// Zoom the time series: full width, legend with values, the query below.
	h.keys("j")
	for i := 0; i < 3 && !strings.Contains(h.screen(), "sum:requests"); i++ {
		h.keys("<enter>")
		if !strings.Contains(h.screen(), "sum:requests") {
			h.keys("<esc>l")
		}
	}
	h.expect("sum:requests{env:prod} by {host}", "host:a", "host:b")
	h.keys("<left><left>")
	h.screen()
	h.keys("o")
	if len(h.opened) == 0 || !strings.Contains(h.opened[len(h.opened)-1], "fullscreen_widget=") {
		t.Errorf("o in a zoomed widget should open it fullscreen in Datadog: %q", h.opened)
	}
}

func TestMonitorsMuteAndUnmute(t *testing.T) {
	h := newHarness(t, 140, 40)
	h.keys("3")
	h.expect("1 Alert", "1 Warn", "1 No Data", "High CPU on api", "P1", "api")
	h.reject("Disk space") // OK is folded
	// Mute the alerting monitor for an hour.
	h.keys("m")
	h.expect("Mute «High CPU on api»", "For 1 hour")
	h.keys("<enter>")
	if len(h.dd.scheduled) != 1 || h.dd.scheduled[0] != 1 {
		t.Fatalf("mute didn't schedule a downtime for monitor 1: %v", h.dd.scheduled)
	}
	h.expect("muted", "Muted for 1h")
	// And unmute it: the downtime is canceled.
	h.keys("u")
	if len(h.dd.canceled) != 1 {
		t.Fatalf("unmute didn't cancel the downtime: %v", h.dd.canceled)
	}
	// The detail: chart with thresholds, groups, the message without {{#…}}.
	h.keys("<enter>")
	h.expect("Alert", "High CPU on api", "critical 90", "warning 80", "host:web-1", "CPU is high", "@pagerduty-platform")
	h.reject("{{#is_alert}}")
}

func TestLogsSearchDetailAndFilters(t *testing.T) {
	h := newHarness(t, 140, 40)
	h.keys("4")
	h.expect("status:error", "Could not verify token", "ERROR", "api")
	h.keys("<enter>")
	h.expect("err.name", "TokenExpiredError", "at verify (verify.js:190)", "http.status_code", "401")
	h.keys("<esc>s")
	if !h.dd.sawQuery("logs:status:error service:api") {
		t.Fatalf("s should narrow to the log's service: %q", h.dd.queries)
	}
	// Edit the query by hand.
	h.keys("/<backspace><backspace><backspace>web<enter>")
	h.expect("service:web")
	h.keys("L")
	h.expect("live")
}

func TestMetricsExplorerCompletesAndReshapes(t *testing.T) {
	h := newHarness(t, 140, 40)
	h.keys("5")
	h.expect("Type a metric name")
	h.keys("system.cp")
	h.expect("system.cpu.user", "system.cpu.system")
	h.keys("<tab>")
	h.expect("avg:system.cpu.user{*}")
	h.keys("<enter>")
	h.expect("host:a", "min / avg / max")
	h.keys("a")
	if !h.dd.sawQuery("sum:system.cpu.user{*}") {
		t.Fatalf("a should switch avg → sum: %q", h.dd.queries)
	}
}

func TestNowShowsWhatNeedsAttention(t *testing.T) {
	h := newHarness(t, 150, 44)
	h.expect("Alerting", "Needs attention", "High CPU on api", "Errors by service", "api", "Recent events",
		"3 × gcp_bigquery_table … was updated", "Deployed api v1.4.2")
	h.keys("<enter>")
	h.expect("critical 90")
}

func TestPaletteJumpsToADashboard(t *testing.T) {
	h := newHarness(t, 120, 36)
	h.keys("2") // loads the dashboard list the palette searches
	h.keys(":")
	h.expect("Jump to")
	h.keys("service he<enter>")
	h.expect("Runbook", "$env prod")
}

func TestInvestigateCopiesTheCommandOutsideITerm(t *testing.T) {
	h := newHarness(t, 120, 36)
	h.keys("3C")
	if !strings.Contains(h.clipboard, "claude") || !strings.Contains(h.clipboard, "prompts") {
		t.Fatalf("clipboard: %q", h.clipboard)
	}
	h.expect("Command copied")
	// The prompt tells Claude how to dig with this CLI.
	file := h.clipboard[strings.Index(h.clipboard, "$(cat '")+7:]
	file = file[:strings.Index(file, "'")]
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"High CPU on api", "datadog monitors show 1 --json", "datadog triage", "services context api"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("prompt lacks %q:\n%s", want, data)
		}
	}
}

func TestChartStyleToggleIsRemembered(t *testing.T) {
	h := newHarness(t, 100, 30)
	before := h.app.ctx.style
	h.keys("B")
	if h.app.ctx.style == before {
		t.Fatal("B didn't switch the chart style")
	}
	data, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "datadog-cli", "ui.json"))
	want := map[viz.Style]string{viz.Braille: "braille", viz.Blocks: "blocks"}[h.app.ctx.style]
	if !strings.Contains(string(data), want) {
		t.Errorf("style not saved: %s", data)
	}
}

// Screens start loading before the terminal says how big it is; once it
// does, charts asked for at a guessed width are asked again at the real one.
func TestChartsFollowTheRealWidth(t *testing.T) {
	h := newHarness(t, 140, 40)
	h.dd.mu.Lock()
	logs := h.dd.intervals["status:error"]
	h.dd.mu.Unlock()
	if logs == 0 || logs > 60_000 {
		t.Fatalf("the logs histogram uses %d ms buckets on a 140-column screen, want ≤ 1 min", logs)
	}
}
