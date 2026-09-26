package tui

import (
	"testing"

	"github.com/ngavilan-dogfy/datadog-cli/internal/demo"
)

// 'datadog ui --demo' walks through every screen with the made-up org.
func TestDemoOrgEveryScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {140, 44}, {203, 61}} {
		h := newHarnessWith(t, demo.New(), Options{Site: "demo · acme shop", Profile: "demo", Demo: true}, size[0], size[1])
		h.expect("DEMO", "Alerting", "Needs attention", "Checkout p95 latency is above 800 ms")
		h.keys("C") // no hand-off to Claude with made-up data
		if h.clipboard != "" {
			t.Fatalf("the demo copied an investigation: %q", h.clipboard)
		}
		h.keys("2")
		h.expect("Checkout · service health", "Platform · on-call")
		h.keys("<enter>")
		h.expect("Golden signals", "Requests / s", "p95 latency", "Error rate", "$service checkout")
		h.keys("o")
		if len(h.opened) != 1 || h.opened[0] != "" {
			t.Fatalf("o should ask for an empty (demo) link: %q", h.opened)
		}
		h.keys("jj<enter>")
		h.screen()
		h.keys("<esc><esc><esc>")
		h.keys("j<enter>") // the on-call dashboard
		h.screen()
		h.keys("<esc>3")
		h.expect("2 Alert", "3 Warn", "1 No Data", "muted")
		h.keys("<enter>")
		h.expect("Checkout p95 latency is above 800 ms", "critical 800ms")
		h.keys("<esc>4")
		h.expect("status:error")
		h.keys("<enter>")
		h.screen()
		h.keys("<esc>5trace.http.request.du<tab><enter>")
		h.screen()
	}
}
