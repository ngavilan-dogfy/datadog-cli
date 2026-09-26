package tui

import (
	"encoding/json"
	"testing"
)

func TestSubstituteTemplateVars(t *testing.T) {
	tv := []tvar{{Name: "env", Prefix: "env", Value: "prod"}, {Name: "environment", Prefix: "environment", Value: "*"},
		{Name: "service", Prefix: "service", Value: "*"}, {Name: "project_id", Prefix: "project_id", Value: "acme-prod"}}
	cases := map[string]string{
		"avg:system.cpu.user{$env}":                          "avg:system.cpu.user{env:prod}",
		"avg:system.cpu.user{$env,$service} by {host}":       "avg:system.cpu.user{env:prod} by {host}",
		"sum:trace.hits{$service,$environment}.as_count()":   "sum:trace.hits{*}.as_count()",
		"service:api env:$env.value status:error":            "service:api env:prod status:error",
		"avg:gcp.run.cpu{project_id:$project_id.value,$env}": "avg:gcp.run.cpu{project_id:acme-prod,env:prod}",
		"no variables {host:a}":                              "no variables {host:a}",
	}
	for in, want := range cases {
		if got := substitute(in, tv); got != want {
			t.Errorf("substitute(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustParse(t *testing.T, js string) *dashboard {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return parseDashboard(m)
}

func TestLayoutGravity(t *testing.T) {
	// Five query values at y=0 and a timeseries that claims y=0 too: the
	// timeseries must fall below them instead of overlapping.
	d := mustParse(t, `{"title":"x","widgets":[
		{"id":1,"definition":{"type":"query_value","title":"a"},"layout":{"x":0,"y":0,"width":2,"height":2}},
		{"id":2,"definition":{"type":"query_value","title":"b"},"layout":{"x":2,"y":0,"width":2,"height":2}},
		{"id":3,"definition":{"type":"timeseries","title":"c"},"layout":{"x":0,"y":0,"width":4,"height":3}},
		{"id":4,"definition":{"type":"timeseries","title":"d"},"layout":{"x":4,"y":0,"width":8,"height":3}},
		{"id":5,"definition":{"type":"group","title":"g","widgets":[
			{"id":6,"definition":{"type":"note","content":"hi"},"layout":{"x":0,"y":0,"width":12,"height":1}},
			{"id":7,"definition":{"type":"timeseries","title":"e"},"layout":{"x":0,"y":1,"width":6,"height":3}}]},
		 "layout":{"x":0,"y":3,"width":12,"height":5}}]}`)
	h := layoutWidgets(d.Widgets, 0, 0, 120, 4)
	a, b, c, dd, g := d.Widgets[0], d.Widgets[1], d.Widgets[2], d.Widgets[3], d.Widgets[4]
	if a.y != 0 || b.y != 0 || b.x != 20 {
		t.Errorf("query values: a=(%d,%d) b=(%d,%d)", a.x, a.y, b.x, b.y)
	}
	if c.y != a.y+a.h+rowGap {
		t.Errorf("timeseries c should fall below the query values: y=%d", c.y)
	}
	if dd.y != 0 || dd.x != 40 {
		t.Errorf("timeseries d has free columns above it: (%d,%d)", dd.x, dd.y)
	}
	if g.y < c.y+c.h || g.w != 120 {
		t.Errorf("group placed at y=%d w=%d", g.y, g.w)
	}
	note, e := g.Children[0], g.Children[1]
	if note.y != g.y+1 || e.y != note.y+note.h+rowGap || e.w != 60-colGap {
		t.Errorf("group children: note y=%d, e y=%d w=%d (group y=%d)", note.y, e.y, e.w, g.y)
	}
	if h != g.y+g.h+rowGap {
		t.Errorf("height %d, want %d", h, g.y+g.h+rowGap)
	}
	// Collapsing the group shrinks it to its title.
	g.collapsed = true
	layoutWidgets(d.Widgets, 0, 0, 120, 4)
	if g.h != 1 || len(flatten(d.Widgets)) != 5 {
		t.Errorf("collapsed group: h=%d, visible=%d", g.h, len(flatten(d.Widgets)))
	}
}

func TestLayoutAutoFlow(t *testing.T) {
	d := mustParse(t, `{"widgets":[
		{"definition":{"type":"timeseries"}},{"definition":{"type":"timeseries"}},{"definition":{"type":"timeseries"}},
		{"definition":{"type":"timeseries"}},{"definition":{"type":"query_value"}}]}`)
	layoutWidgets(d.Widgets, 0, 0, 120, 4)
	ws := d.Widgets
	if ws[0].y != 0 || ws[1].y != 0 || ws[2].y != 0 || ws[1].x != 40 {
		t.Errorf("first row: %+v %+v %+v", *ws[0], *ws[1], *ws[2])
	}
	if ws[3].y <= 0 || ws[3].x != 0 || ws[4].x != 40 {
		t.Errorf("second row: (%d,%d) (%d,%d)", ws[3].x, ws[3].y, ws[4].x, ws[4].y)
	}
}
