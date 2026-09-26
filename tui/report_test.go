package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/internal/demo"
)

func TestReadDashboardDescribesEveryWidget(t *testing.T) {
	api := demo.New()
	raw, err := api.GetDashboard("a2c-9xk-4pd")
	if err != nil {
		t.Fatal(err)
	}
	rep := ReadDashboard(api, raw, ReadOptions{Span: 4 * time.Hour})
	if rep.Title == "" || len(rep.Widgets) < 8 {
		t.Fatalf("report too thin: %q, %d widgets", rep.Title, len(rep.Widgets))
	}
	for _, w := range rep.Widgets {
		if w.Type == "group" || w.Skipped != "" {
			continue
		}
		if w.Error != "" {
			t.Errorf("%s (%s) failed: %s", w.Title, w.Path, w.Error)
		}
		shown := len(w.Series) + len(w.Values) + len(w.Patterns)
		if w.Monitors != nil || w.Text != "" {
			shown++
		}
		if shown == 0 && len(w.Problems) == 0 {
			t.Errorf("%s (%s, %s) says nothing", w.Title, w.Type, w.Path)
		}
		if !strings.HasPrefix(w.Path, "widgets[") {
			t.Errorf("bad path %q", w.Path)
		}
	}
	// Template variables resolve into the queries it lists.
	for _, w := range rep.Widgets {
		for _, q := range w.Queries {
			if strings.Contains(q, "$env") {
				t.Errorf("unresolved variable in %q", q)
			}
		}
	}
}

func TestReadDashboardFindsProblems(t *testing.T) {
	const src = `{"title": "Draft", "template_variables": [{"name": "region", "prefix": "region", "default": "*"}],
	"widgets": [
	  {"definition": {"type": "timeseries", "title": "Hits", "requests": [{"queries": [{"data_source": "metrics", "name": "q", "query": "sum:trace.http.request.hits{env:prod,service:checkout}.as_rate()"}], "formulas": [{"formula": "q"}]}]}},
	  {"definition": {"type": "timeseries", "title": "Hits again", "requests": [{"queries": [{"data_source": "metrics", "name": "q", "query": "sum:trace.http.request.hits{env:prod,service:checkout}.as_rate()"}], "formulas": [{"formula": "q"}]}]}},
	  {"definition": {"type": "query_value", "requests": [{"response_format": "scalar", "queries": [{"data_source": "metrics", "name": "q", "query": "avg:trace.http.request.duration{env:prod,service:checkout}", "aggregator": "avg"}], "formulas": [{"formula": "q"}]}]}},
	  {"definition": {"type": "timeseries", "title": "Nothing", "requests": [{"queries": [{"data_source": "metrics", "name": "q", "query": "avg:no.such.metric{env:prod}"}], "formulas": [{"formula": "q"}]}]}},
	  {"definition": {"type": "group", "title": "More", "widgets": [
	    {"definition": {"type": "note", "content": "Read me first"}},
	    {"definition": {"type": "image", "url": "https://example.com/x.png"}}
	  ]}}
	]}`
	var raw map[string]any
	if err := json.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatal(err)
	}
	rep := ReadDashboard(demo.New(), raw, ReadOptions{Span: time.Hour})
	by := map[string]WidgetReport{}
	for _, w := range rep.Widgets {
		by[w.Path] = w
	}
	if w := by["widgets[2]"]; !has(w.Problems, "untitled") || len(w.Values) != 1 {
		t.Errorf("untitled query value: %+v", w)
	}
	if w := by["widgets[3]"]; !has(w.Problems, "no data") {
		t.Errorf("a metric without data should say so: %+v", w.Problems)
	}
	if w := by["widgets[4].definition.widgets[0]"]; w.Text != "Read me first" || w.Group != "More" {
		t.Errorf("note in a group: %+v", w)
	}
	if w := by["widgets[4].definition.widgets[1]"]; w.Skipped == "" {
		t.Errorf("images aren't read, and should say so: %+v", w)
	}
	for _, want := range []string{"same queries: Hits = Hits again", "hardcode env:prod", "hardcode service:checkout"} {
		if !has(rep.Problems, want) {
			t.Errorf("dashboard problems lack %q: %v", want, rep.Problems)
		}
	}
}

func has(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
