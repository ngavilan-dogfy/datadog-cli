package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// The review's rules run on synthetic facts: monitors, their transitions
// and their data, as if read from an organization, 30 days to now.

var reviewNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func reviewFacts30d(monitors ...datadog.Monitor) reviewFacts {
	return reviewFacts{
		Now: reviewNow, From: reviewNow.Add(-30 * 24 * time.Hour), To: reviewNow, Scope: "every monitor",
		All: monitors, InScope: monitors, Data: map[int64]*reviewData{}, Failed: map[string]string{},
	}
}

// mon is a metric monitor that notifies someone, says its value, links
// its runbook and has an owner: nothing to say about it by default.
func mon(id int64, name, query string, critical float64) datadog.Monitor {
	return datadog.Monitor{
		ID: id, Name: name, Type: "query alert", Query: query, OverallState: "OK",
		Message: "{{value}} (threshold {{threshold}}) https://runbooks.example.com/checkout @slack-checkout-alerts",
		Tags:    []string{"service:checkout", "team:payments"},
		Created: "2026-01-01T00:00:00.000000+00:00", Modified: "2026-09-01T00:00:00.000000+00:00",
		Options: datadog.MonitorOptions{Thresholds: map[string]interface{}{"critical": critical}},
	}
}

func tr(id int64, at time.Time, from, to string, groups ...string) datadog.EventV2 {
	if len(groups) == 0 {
		groups = []string{"*"}
	}
	return datadog.EventV2{ID: at.Format(time.RFC3339Nano) + from + to, Timestamp: at,
		Monitor: &datadog.EventMonitor{ID: id, FromState: from, ToState: to, Groups: groups}}
}

func reviewed(t *testing.T, rep *monitorsReview, id int64) reviewedMonitor {
	t.Helper()
	for _, m := range rep.Monitors {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("monitor %d isn't in the review", id)
	return reviewedMonitor{}
}

func problem(rm reviewedMonitor, rule string) *reviewProblem {
	for _, p := range rm.Problems {
		if p.Rule == rule {
			return &p
		}
	}
	return nil
}

func rules(rm reviewedMonitor) []string {
	var out []string
	for _, p := range rm.Problems {
		out = append(out, p.Rule)
	}
	return out
}

func intp(v int) *int { return &v }

const errorRate = "sum(last_5m):sum:trace.express.request.errors{service:checkout}.as_count() / sum:trace.express.request.hits{service:checkout}.as_count() > 0.05"

func TestReviewAWellKeptMonitorIsFine(t *testing.T) {
	f := reviewFacts30d(mon(1, "Checkout error rate", errorRate, 0.05))
	f.Data[1] = &reviewData{Evaluated: true, Points: 2000, Span: "7d", Max: 0.04, P99: 0.03}
	rm := reviewed(t, reviewFrom(f), 1)
	if rm.Verdict != "fine" || len(rm.Problems) != 0 {
		t.Fatalf("a well-kept monitor should be fine, got %s: %v", rm.Verdict, rules(rm))
	}
}

func TestReviewSilentUnlessADraftOrPartOfSomething(t *testing.T) {
	silent := mon(1, "Checkout error rate", errorRate, 0.05)
	silent.Message = "{{value}} https://runbooks.example.com"
	draft := silent
	draft.ID, draft.Name = 2, "[DRAFT] checkout latency"
	feeds := silent
	feeds.ID, feeds.Query = 3, strings.Replace(errorRate, "0.05", "0.1", 1)
	composite := datadog.Monitor{ID: 4, Name: "Checkout down", Type: "composite", Query: "3 && 5", OverallState: "OK",
		Message: "@pagerduty-checkout https://x {{value}}", Tags: []string{"team:payments"}}
	rep := reviewFrom(reviewFacts30d(silent, draft, feeds, composite))

	if p := problem(reviewed(t, rep, 1), "silent"); p == nil || p.Severity != "fix" {
		t.Fatalf("a monitor without @handles notifies no one: %+v", p)
	}
	if p := problem(reviewed(t, rep, 2), "silent"); p == nil || p.Severity != "tidy" {
		t.Fatalf("a quiet draft is only tidying: %+v", p)
	}
	rm := reviewed(t, rep, 3)
	if problem(rm, "silent") != nil || len(rm.Feeds) != 1 || rm.Feeds[0] != "composite #4" {
		t.Fatalf("a monitor that feeds a composite may stay quiet: %v, feeds %v", rules(rm), rm.Feeds)
	}
}

func TestReviewTrafficFloorThatGoesQuiet(t *testing.T) {
	legacy := mon(1, "Checkout traffic floor", "sum(last_15m):sum:trace.express.request.hits{service:checkout}.as_count() < 100", 100)
	showNoData := legacy
	showNoData.ID, showNoData.Query = 2, strings.Replace(legacy.Query, "100", "101", 1)
	showNoData.Options.OnMissingData = "show_no_data"
	countsAsZero := legacy
	countsAsZero.ID, countsAsZero.Query = 3, strings.Replace(legacy.Query, "100", "102", 1)
	countsAsZero.Options.OnMissingData = "default"
	notifies := legacy
	notifies.ID, notifies.Query = 4, strings.Replace(legacy.Query, "100", "103", 1)
	notifies.Options.NotifyNoData = true
	ratio := mon(5, "CDN hit rate", "sum(last_10m):sum:cdn.requests{cache:hit}.as_count() / sum:cdn.requests{*}.as_count() * 100 < 60", 60)
	orders := mon(6, "Orders per hour", "sum(last_1h):sum:shop.orders.created{env:prod}.as_count() < 5", 5)
	gauge := mon(7, "Replica health", "min(last_5m):min:db.replica.health{*} by {host} < 1", 1)
	rep := reviewFrom(reviewFacts30d(legacy, showNoData, countsAsZero, notifies, ratio, orders, gauge))
	if p := problem(reviewed(t, rep, 6), "floor-goes-quiet"); p == nil || !strings.HasSuffix(p.Fix, "no_data_timeframe=120") {
		t.Errorf("a floor on any count goes quiet when the count drops to nothing: %+v", p)
	}
	if p := problem(reviewed(t, rep, 7), "floor-goes-quiet"); p != nil {
		t.Errorf("a gauge's floor isn't about counting nothing: %+v", p)
	}

	p := problem(reviewed(t, rep, 1), "floor-goes-quiet")
	if p == nil || p.Severity != "fix" || p.Fix != "datadog monitors edit 1 --option notify_no_data=true --option no_data_timeframe=30" {
		t.Fatalf("a legacy floor without no-data notifications: %+v", p)
	}
	p = problem(reviewed(t, rep, 2), "floor-goes-quiet")
	if p == nil || p.Fix != "datadog monitors edit 2 --option on_missing_data=show_and_notify_no_data" || !strings.Contains(p.Hint, "on_missing_data=default") {
		t.Fatalf("a floor that shows No Data without notifying: %+v", p)
	}
	for _, id := range []int64{3, 4, 5} {
		if p := problem(reviewed(t, rep, id), "floor-goes-quiet"); p != nil {
			t.Errorf("monitor %d sees traffic stop (or isn't a traffic floor): %+v", id, p)
		}
	}
}

func TestReviewFlapping(t *testing.T) {
	m := mon(1, "Checkout p95 latency", "avg(last_5m):p95:trace.express.request{service:checkout} > 2", 2)
	f := reviewFacts30d(m)
	for day := 1; day <= 5; day++ {
		at := reviewNow.Add(-time.Duration(day) * 24 * time.Hour)
		f.Events = append(f.Events, tr(1, at, "OK", "Alert"), tr(1, at.Add(4*time.Minute), "Alert", "OK"))
	}
	rm := reviewed(t, reviewFrom(f), 1)
	p := problem(rm, "flapping")
	if rm.Alerts != 5 || rm.Flaps != 5 || p == nil {
		t.Fatalf("5 alerts over within minutes flap: alerts %d flaps %d, %v", rm.Alerts, rm.Flaps, rules(rm))
	}
	if p.Fix != "datadog monitors edit 1 --threshold critical_recovery=1.6" {
		t.Errorf("the fix is a recovery threshold short of the alert: %q", p.Fix)
	}
	if !strings.Contains(p.Hint, "last_10m") {
		t.Errorf("the alternative is a longer window: %q", p.Hint)
	}
	if rm.MinutesAlerting != 20 {
		t.Errorf("5 alerts of 4 minutes are 20 minutes alerting, got %d", rm.MinutesAlerting)
	}
}

func TestReviewStuck(t *testing.T) {
	alerting := mon(1, "Checkout error rate", errorRate, 0.05)
	alerting.OverallState = "Alert"
	noData := mon(2, "Checkout queue depth", "avg(last_5m):avg:checkout.queue.depth{*} > 1000", 1000)
	noData.OverallState = "No Data"
	recent := mon(3, "Checkout p95", "avg(last_5m):p95:trace.express.request{service:checkout} > 2", 2)
	recent.OverallState = "Alert"
	f := reviewFacts30d(alerting, noData, recent)
	f.Events = []datadog.EventV2{
		tr(1, reviewNow.Add(-5*24*time.Hour), "OK", "Warn"),
		tr(1, reviewNow.Add(-4*24*time.Hour), "Warn", "Alert"),
		tr(3, reviewNow.Add(-2*time.Hour), "OK", "Alert"),
	}
	rep := reviewFrom(f)

	p := problem(reviewed(t, rep, 1), "stuck")
	if p == nil || p.What != "Alert for 5 days" || p.Severity != "fix" {
		t.Fatalf("not OK for 5 days (warning, then alerting) is stuck: %+v", p)
	}
	p = problem(reviewed(t, rep, 2), "stuck")
	if p == nil || !strings.Contains(p.What, "since before the window") || !strings.Contains(p.Why, "stopped arriving") {
		t.Fatalf("No Data with no transition in 30 days: %+v", p)
	}
	if p := problem(reviewed(t, rep, 3), "stuck"); p != nil {
		t.Errorf("alerting for 2 hours isn't stuck: %+v", p)
	}
}

func TestReviewTransitionsInTheSameSecondCancelOut(t *testing.T) {
	// Creating a monitor flips it through states in the same second, in
	// any order: that's no alert, and it ends where it started.
	m := mon(1, "Checkout traffic floor", "sum(last_15m):sum:trace.express.request.hits{service:checkout}.as_count() < 100", 100)
	m.Options.NotifyNoData = true
	f := reviewFacts30d(m)
	at := reviewNow.Add(-28 * 24 * time.Hour)
	f.Events = []datadog.EventV2{
		tr(1, at, "No Data", "OK"), tr(1, at, "Alert", "OK"),
		tr(1, at, "OK", "No Data"), tr(1, at, "OK", "Alert"),
	}
	rm := reviewed(t, reviewFrom(f), 1)
	if rm.Alerts != 0 || rm.MinutesAlerting != 0 || rm.NoData != 0 {
		t.Fatalf("a burst that comes back to OK counts nothing: alerts %d, minutes %d, no data %d", rm.Alerts, rm.MinutesAlerting, rm.NoData)
	}

	got := netTransitions([]datadog.EventV2{
		tr(1, at, "OK", "Warn"), tr(1, at, "Warn", "Alert"),
	})
	if len(got) != 1 || got[0].from != "OK" || got[0].to != "Alert" {
		t.Fatalf("a burst that goes somewhere is one transition: %+v", got)
	}
}

func TestReviewGroupsAlertOnTheirOwn(t *testing.T) {
	m := mon(1, "Host CPU", "avg(last_5m):avg:system.cpu.user{*} by {host} > 90", 90)
	f := reviewFacts30d(m)
	at := reviewNow.Add(-3 * 24 * time.Hour)
	f.Events = []datadog.EventV2{
		tr(1, at, "OK", "Alert", "host:a"),
		tr(1, at.Add(time.Minute), "OK", "Alert", "host:b"),
		tr(1, at.Add(time.Hour), "Alert", "OK", "host:b"),
		tr(1, at.Add(2*time.Hour), "Alert", "OK", "host:a"),
	}
	rm := reviewed(t, reviewFrom(f), 1)
	if rm.Alerts != 2 || rm.MinutesAlerting != 120+59 {
		t.Fatalf("two hosts alerting on their own: alerts %d, minutes %d", rm.Alerts, rm.MinutesAlerting)
	}
}

func TestReviewUnfinishedQuery(t *testing.T) {
	m := mon(1, "High p95 latency for $service", "percentile(last_15m):p95:trace.__apm_operation_name_placeholder__.{*} > 1", 1)
	m.OverallState = "No Data"
	f := reviewFacts30d(m)
	f.Data[1] = &reviewData{Span: "7d"}
	rm := reviewed(t, reviewFrom(f), 1)
	p := problem(rm, "unfinished-query")
	if p == nil || !strings.Contains(p.What, "__apm_operation_name_placeholder__") {
		t.Fatalf("a query left with a placeholder: %v", rules(rm))
	}
	if problem(rm, "stuck") != nil || problem(rm, "ok-without-data") != nil {
		t.Errorf("the placeholder explains its No Data; nothing else should: %v", rules(rm))
	}
}

func TestReviewOKWithoutData(t *testing.T) {
	gauge := mon(1, "Checkout queue depth", "avg(last_5m):avg:checkout.queue.depth{*} > 1000", 1000)
	count := mon(2, "Checkout errors", "sum(last_5m):sum:trace.express.request.errors{service:checkout}.as_count() > 50", 50)
	f := reviewFacts30d(gauge, count)
	f.Data[1] = &reviewData{Span: "7d", Evaluated: true}
	f.Data[2] = &reviewData{Span: "7d", Evaluated: true}
	rep := reviewFrom(f)
	if p := problem(reviewed(t, rep, 1), "ok-without-data"); p == nil || p.Severity != "fix" {
		t.Fatalf("a gauge with no data that shows OK watches nothing: %+v", p)
	}
	if p := problem(reviewed(t, rep, 2), "ok-without-data"); p != nil {
		t.Errorf("a count with no data is zero, not missing: %+v", p)
	}
}

func TestReviewNeverClose(t *testing.T) {
	loose := mon(1, "Checkout error rate", errorRate, 0.05)
	p1 := mon(2, "[P1] Checkout error rate", strings.Replace(errorRate, "0.05", "0.5", 1), 0.5)
	p1.Priority = intp(1)
	p1.Options.RenotifyInterval = intp(30)
	flat := mon(3, "Checkout 5xx", strings.Replace(errorRate, "0.05", "0.06", 1), 0.06)
	f := reviewFacts30d(loose, p1, flat)
	f.Data[1] = &reviewData{Evaluated: true, Points: 2016, Span: "7d", Max: 0.004, P99: 0.003}
	f.Data[2] = &reviewData{Evaluated: true, Points: 2016, Span: "7d", Max: 0.004, P99: 0.003}
	f.Data[3] = &reviewData{Evaluated: true, Points: 2016, Span: "7d"}
	rep := reviewFrom(f)

	p := problem(reviewed(t, rep, 1), "never-close")
	if p == nil || p.Fix != "datadog monitors edit 1 --threshold critical=0.006" {
		t.Fatalf("an error-rate threshold 12 times its peak: %+v", p)
	}
	if !strings.Contains(p.What, "peaked at 0.004") {
		t.Errorf("it says how far: %q", p.What)
	}
	if p := problem(reviewed(t, rep, 2), "never-close"); p != nil {
		t.Errorf("a P1 is meant to catch disasters only: %+v", p)
	}
	if p := problem(reviewed(t, rep, 3), "never-close"); p != nil {
		t.Errorf("all zeros say nothing about the threshold: %+v", p)
	}
}

func TestReviewRemindersAndDelays(t *testing.T) {
	p2 := mon(1, "Checkout error rate", errorRate, 0.05)
	p2.Priority = intp(2)
	cloud := mon(2, "Checkout DB CPU", "avg(last_10m):avg:gcp.cloudsql.database.cpu.utilization{database_id:checkout} > 0.9", 0.9)
	aws := mon(3, "Checkout queue", "max(last_10m):max:aws.sqs.approximate_age_of_oldest_message{queuename:checkout} > 600", 600)
	delayed := cloud
	delayed.ID, delayed.Query = 4, strings.Replace(cloud.Query, "0.9", "0.95", 1)
	delayed.Options.EvaluationDelay = intp(300)
	rep := reviewFrom(reviewFacts30d(p2, cloud, aws, delayed))

	if p := problem(reviewed(t, rep, 1), "never-reminds"); p == nil || p.Fix != "datadog monitors edit 1 --option renotify_interval=60" {
		t.Fatalf("a P2 without reminders: %+v", p)
	}
	if p := problem(reviewed(t, rep, 2), "no-evaluation-delay"); p == nil || p.Fix != "datadog monitors edit 2 --option evaluation_delay=300" {
		t.Fatalf("GCP metrics judged before they arrive: %+v", p)
	}
	if p := problem(reviewed(t, rep, 3), "no-evaluation-delay"); p == nil || !strings.HasSuffix(p.Fix, "evaluation_delay=900") {
		t.Fatalf("AWS metrics arrive later: %+v", p)
	}
	if p := problem(reviewed(t, rep, 4), "no-evaluation-delay"); p != nil {
		t.Errorf("it already waits: %+v", p)
	}
}

func TestReviewMutedForever(t *testing.T) {
	m := mon(1, "Checkout error rate", errorRate, 0.05)
	m.MatchingDowntimes = []datadog.MatchingDowntime{{ID: 1014024896}}
	f := reviewFacts30d(m)
	id := int64(1)
	f.Downtimes = []datadog.DowntimeData{{ID: "0b6c1f2e-muted", Attributes: datadog.DowntimeAttributes{
		Status: "active", MonitorIdentifier: &datadog.DowntimeMonitorID{MonitorID: &id}, Schedule: &datadog.DowntimeSchedule{}}}}
	p := problem(reviewed(t, reviewFrom(f), 1), "muted-forever")
	if p == nil || p.Fix != "datadog downtimes cancel 0b6c1f2e-muted" {
		t.Fatalf("a mute with no end, and the downtime to cancel: %+v", p)
	}
}

func TestReviewMostlyAlertingAndNoisy(t *testing.T) {
	mostly := mon(1, "Checkout DLQ age", "max(last_1h):max:gcp.pubsub.subscription.oldest_unacked_message_age{subscription_id:checkout-dlq} > 86400", 86400)
	mostly.Options.EvaluationDelay = intp(300)
	noisy := mon(2, "Checkout p95", "avg(last_5m):p95:trace.express.request{service:checkout} > 2", 2)
	f := reviewFacts30d(mostly, noisy)
	f.Events = append(f.Events,
		tr(1, reviewNow.Add(-25*24*time.Hour), "OK", "Alert"),
		tr(1, reviewNow.Add(-12*24*time.Hour), "Alert", "OK"))
	for i := 0; i < 12; i++ {
		at := reviewNow.Add(-time.Duration(i+1) * 48 * time.Hour)
		f.Events = append(f.Events, tr(2, at, "OK", "Alert"), tr(2, at.Add(90*time.Minute), "Alert", "OK"))
	}
	rep := reviewFrom(f)
	if p := problem(reviewed(t, rep, 1), "mostly-alerting"); p == nil || p.What != "Alerting 13 days of the last 30d" {
		t.Fatalf("alerting 13 days of 30: %+v", p)
	}
	if p := problem(reviewed(t, rep, 2), "noisy"); p == nil || p.What != "Alerted 12 times in 30d" {
		t.Fatalf("12 alerts of an hour and a half: %+v", p)
	}
}

func TestReviewTidying(t *testing.T) {
	bare := datadog.Monitor{ID: 1, Name: "[draft] checkout latency", Type: "query alert", OverallState: "OK",
		Query:   "avg(last_5m):p95:trace.express.request{*} > 2",
		Message: "@slack-checkout-alerts", Modified: "2026-08-01T00:00:00.000000+00:00",
		Options: datadog.MonitorOptions{Thresholds: map[string]interface{}{"critical": 2.0}}}
	twin := mon(2, "Checkout p95 (copy)", bare.Query, 2)
	rep := reviewFrom(reviewFacts30d(bare, twin))
	rm := reviewed(t, rep, 1)
	want := []string{"no-runbook", "no-value", "no-owner", "old-draft", "duplicate"}
	if strings.Join(rules(rm), " ") != strings.Join(want, " ") || rm.Verdict != "tidy" {
		t.Fatalf("tidying: got %v (%s), want %v", rules(rm), rm.Verdict, want)
	}
	if p := problem(rm, "duplicate"); p.What != "Same query as #2" {
		t.Errorf("it names its twin: %q", p.What)
	}
	if p := problem(rm, "old-draft"); p.What != "A draft, untouched for 57 days" {
		t.Errorf("how old the draft is: %q", p.What)
	}
}

func TestReviewOutputs(t *testing.T) {
	floor := mon(1, "[P1] Checkout traffic floor", "sum(last_15m):sum:trace.express.request.hits{service:checkout}.as_count() < 100", 100)
	floor.Priority = intp(1)
	fine := mon(2, "Checkout error rate", errorRate, 0.05)
	rep := reviewFrom(reviewFacts30d(floor, fine))

	md := rep.markdown()
	for _, want := range []string{
		"# Monitors review · every monitor",
		"## To fix", "### [\\[P1\\] Checkout traffic floor](", "```sh\n  datadog monitors edit 1 --option notify_no_data=true",
		"datadog monitors edit 1 --option renotify_interval=30", "## Fine", "Nothing was changed",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the markdown lacks %q:\n%s", want, md)
		}
	}
	text := rep.text(false)
	for _, want := range []string{"To fix", "✗ [P1] Checkout traffic floor", "fix:   datadog monitors edit 1", "Fine: Checkout error rate"} {
		if !strings.Contains(text, want) {
			t.Errorf("the terminal output lacks %q:\n%s", want, text)
		}
	}
	if _, err := json.Marshal(rep); err != nil {
		t.Fatal(err)
	}
	if rep.Summary["fix"] != 1 || rep.Summary["fine"] != 1 {
		t.Errorf("summary: %v", rep.Summary)
	}

	empty := reviewFrom(reviewFacts{Now: reviewNow, From: reviewNow.Add(-time.Hour), To: reviewNow, Service: "checkout", Scope: "the monitors of checkout"})
	if !strings.Contains(empty.text(false), "Nothing watches checkout: 'datadog coverage --service checkout'") {
		t.Errorf("an empty scope says what to do:\n%s", empty.text(false))
	}
}

func TestReviewHelpers(t *testing.T) {
	if got := longerWindow("avg(last_5m):avg:x{*} > 1"); got != "avg(last_10m):avg:x{*} > 1" {
		t.Errorf("longerWindow: %q", got)
	}
	for v, want := range map[float64]string{0.0329: "0.0329", 42.11: "42.11", 2195: "2195", 16912: "16.9k", 155500: "156k", 37200: "37.2k", 20000: "20k", 1.25e6: "1.2M"} {
		if got := fmtVal(v); got != want {
			t.Errorf("fmtVal(%v) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[float64]float64{0.00612: 0.0061, 1234: 1200, 0.8: 0.8} {
		if got := roundTo(v, 2); got != want {
			t.Errorf("roundTo(%v, 2) = %v, want %v", v, got, want)
		}
	}
	if fmtLong(50*time.Hour) != "2 days" || fmtLong(90*time.Minute) != "1h30" {
		t.Errorf("fmtLong: %q %q", fmtLong(50*time.Hour), fmtLong(90*time.Minute))
	}
	m := datadog.Monitor{Query: "avg(last_1m):avg:x{*} > 1"}
	if dataSpan(m) != 24*time.Hour {
		t.Errorf("1-minute windows read a day: %v", dataSpan(m))
	}
	m.Query = "avg(last_5m):avg:x{*} > 1"
	if dataSpan(m) != 7*24*time.Hour {
		t.Errorf("5-minute windows read a week: %v", dataSpan(m))
	}
}
