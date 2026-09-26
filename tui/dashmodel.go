package tui

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// A dashboard as the viewer needs it: widgets with their definitions kept
// as Datadog sent them (so every query shape works), template variables,
// and a layout computed for the terminal.

type dashboard struct {
	ID, Title, Description string
	LayoutType, ReflowType string
	TVars                  []tvar
	Widgets                []*widget
}

type tvar struct {
	Name, Prefix string
	Value        string   // the current value; "*" means all
	Available    []string // values the dashboard suggests
}

type widget struct {
	ID       int64
	Type     string
	Title    string
	Def      map[string]any
	Layout   *gridBox // position on Datadog's 12-column grid; nil = auto
	Children []*widget

	// placement in terminal cells, relative to the dashboard canvas
	x, y, w, h int
	collapsed  bool
	parent     *widget
}

type gridBox struct{ X, Y, W, H int }

// ─── parsing ─────────────────────────────────────────────────────

func parseDashboard(m map[string]any) *dashboard {
	d := &dashboard{
		ID:          str(m["id"]),
		Title:       str(m["title"]),
		Description: str(m["description"]),
		LayoutType:  str(m["layout_type"]),
		ReflowType:  str(m["reflow_type"]),
	}
	for _, raw := range list(m["template_variables"]) {
		t := obj(raw)
		tv := tvar{Name: str(t["name"]), Prefix: str(t["prefix"]), Value: "*"}
		if defs := list(t["defaults"]); len(defs) > 0 {
			tv.Value = str(defs[0])
		} else if def := str(t["default"]); def != "" {
			tv.Value = def
		}
		for _, v := range list(t["available_values"]) {
			tv.Available = append(tv.Available, str(v))
		}
		d.TVars = append(d.TVars, tv)
	}
	d.Widgets = parseWidgets(list(m["widgets"]), nil)
	return d
}

func parseWidgets(raw []any, parent *widget) []*widget {
	var out []*widget
	for _, r := range raw {
		wm := obj(r)
		def := obj(wm["definition"])
		w := &widget{
			ID:     int64(num(wm["id"])),
			Type:   str(def["type"]),
			Title:  cleanTitle(str(def["title"])),
			Def:    def,
			parent: parent,
		}
		if l := obj(wm["layout"]); l != nil {
			w.Layout = &gridBox{X: int(num(l["x"])), Y: int(num(l["y"])), W: int(num(l["width"])), H: int(num(l["height"]))}
		}
		if w.Type == "group" {
			w.Children = parseWidgets(list(def["widgets"]), w)
		}
		out = append(out, w)
	}
	return out
}

// cleanTitle drops markdown emphasis some titles carry.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "**", "")
	return s
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		b, _ := json.Marshal(x)
		return string(b)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// ─── template variables ──────────────────────────────────────────

var reEmptyScope = regexp.MustCompile(`\{\s*\*?\s*(,\s*\*\s*)*\}`)

// applyTVars returns a deep copy of v with $var and $var.value replaced by
// the selected values, and wildcards tidied out of metric scopes.
func applyTVars(v any, tvars []tvar) any {
	switch x := v.(type) {
	case string:
		return substitute(x, tvars)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = applyTVars(vv, tvars)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = applyTVars(vv, tvars)
		}
		return out
	}
	return v
}

func substitute(s string, tvars []tvar) string {
	if !strings.Contains(s, "$") {
		return s
	}
	sorted := append([]tvar(nil), tvars...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i].Name) > len(sorted[j].Name) })
	for _, t := range sorted {
		re := regexp.MustCompile(`\$` + regexp.QuoteMeta(t.Name) + `(\.value)?\b`)
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			all := t.Value == "" || t.Value == "*"
			if strings.HasSuffix(m, ".value") {
				if all {
					return "*"
				}
				return t.Value
			}
			switch {
			case all:
				return "*"
			case t.Prefix == "":
				return t.Value
			}
			return t.Prefix + ":" + t.Value
		})
	}
	return tidyScopes(s)
}

// tidyScopes removes "*" from metric scopes that have other tags:
// {service:api,*} → {service:api}, {*,*} → {*}.
func tidyScopes(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i+1])
		var keep []string
		for _, part := range strings.Split(s[i+1:i+j], ",") {
			if p := strings.TrimSpace(part); p != "" && p != "*" {
				keep = append(keep, p)
			}
		}
		if len(keep) == 0 {
			b.WriteString("*")
		} else {
			b.WriteString(strings.Join(keep, ","))
		}
		b.WriteByte('}')
		s = s[i+j+1:]
	}
	return b.String()
}

// ─── layout ──────────────────────────────────────────────────────

// defaultSize is a widget's size on the 12-column grid when the dashboard
// doesn't say (auto-flow dashboards).
func defaultSize(t string) (w, h int) {
	switch t {
	case "query_value", "check_status", "alert_value":
		return 2, 2
	case "note", "free_text":
		return 12, 1
	case "group", "powerpack":
		return 12, 0
	case "list_stream", "log_stream", "event_stream", "manage_status", "slo_list":
		return 6, 4
	case "hostmap", "geomap", "treemap", "sunburst":
		return 6, 3
	}
	return 4, 2
}

const (
	colGap = 2 // blank columns between widgets side by side
	rowGap = 1 // blank rows between stacked widgets
)

// layoutWidgets places widgets inside a box of width cells starting at
// (x0, y0), and returns the height used. Every widget falls to the lowest
// free row across the grid columns it spans ("gravity", like Datadog's
// ordered layout): arrangements survive, overlaps disappear, and groups grow
// to fit their contents.
func layoutWidgets(ws []*widget, x0, y0, width, rowsPerUnit int) int {
	var bottom [12]int // per grid column, the next free row (relative)
	cursor := 0        // next grid column for widgets without a position
	col := func(u int) int { return u * width / 12 }
	for _, w := range ws {
		gw, gh := defaultSize(w.Type)
		gx := -1
		if w.Layout != nil {
			if w.Layout.W > 0 {
				gw = w.Layout.W
			}
			if w.Layout.H > 0 {
				gh = w.Layout.H
			}
			gx = w.Layout.X
		}
		gw = max(1, min(12, gw))
		if gx < 0 || gx+gw > 12 {
			if cursor+gw > 12 {
				cursor = 0
			}
			gx = cursor
		}
		cursor = gx + gw
		top := 0
		for c := gx; c < gx+gw; c++ {
			top = max(top, bottom[c])
		}
		w.x = x0 + col(gx)
		w.w = col(gx+gw) - col(gx)
		if gx+gw < 12 {
			w.w -= colGap
		}
		w.w = max(1, w.w)
		w.y = y0 + top
		switch {
		case w.Type == "group" && w.collapsed:
			w.h = 1
		case w.Type == "group":
			w.h = 1 + layoutWidgets(w.Children, w.x, w.y+1, w.w, rowsPerUnit)
		default:
			w.h = max(3, gh*rowsPerUnit-rowGap)
		}
		for c := gx; c < gx+gw; c++ {
			bottom[c] = top + w.h + rowGap
		}
	}
	h := 0
	for _, b := range bottom {
		h = max(h, b)
	}
	return h
}

// flatten lists widgets in reading order, groups before their children,
// skipping the children of collapsed groups.
func flatten(ws []*widget) []*widget {
	var out []*widget
	for _, w := range ws {
		out = append(out, w)
		if w.Type == "group" && !w.collapsed {
			out = append(out, flatten(w.Children)...)
		}
	}
	return out
}
