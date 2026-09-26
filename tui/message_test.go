package tui

import "testing"

func TestMonitorMessageFollowsTheState(t *testing.T) {
	msg := "{{#is_alert}}CPU is at {{value}}.{{/is_alert}}\n{{#is_warning}}CPU is climbing.{{/is_warning}}\n" +
		"{{^is_recovery}}Check the deploys.{{/is_recovery}}\n{{#is_recovery}}Back to normal.{{/is_recovery}}\n" +
		"{{#is_priority 'P1'}}@pagerduty-platform{{/is_priority}}\n@slack-alerts"
	p1 := 1
	for _, c := range []struct {
		state string
		prio  *int
		want  string
	}{
		{"Alert", &p1, "CPU is at {{value}}.\nCheck the deploys.\n@pagerduty-platform\n@slack-alerts"},
		{"Warn", nil, "CPU is climbing.\nCheck the deploys.\n@slack-alerts"},
		{"OK", nil, "Back to normal.\n@slack-alerts"},
	} {
		if got := cleanMonitorMessage(msg, c.state, c.prio); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.state, got, c.want)
		}
	}
	// A message that only speaks of alerts still reads on an OK monitor.
	if got := cleanMonitorMessage("{{#is_alert}}Disk full{{/is_alert}}", "OK", nil); got != "Disk full" {
		t.Errorf("OK monitor with alert-only message: %q", got)
	}
}
