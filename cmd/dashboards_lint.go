package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

// tmplVarRe matches `$name` template variable references in queries.
// `\$` is the literal dollar; the name must start with a letter and may
// contain letters, digits, or underscores.
var tmplVarRe = regexp.MustCompile(`\$([a-zA-Z][a-zA-Z0-9_]*)`)

// queryRefRe matches identifiers that look like query names in a formula
// expression (lowercase letters/digits/underscores, not preceded by '$' or
// a metric prefix like 'sum:'). Used to decide if a `/` formula is dividing
// two query references vs. a numeric unit conversion.
var queryRefRe = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

var (
	lintJSON      bool
	lintShowOnly  string // "error" / "warning" / "info"
	lintExitNonZero bool
)

var dashboardsLintCmd = &cobra.Command{
	Use:   "lint <dashboard-id>",
	Short: "Check a dashboard for common smells",
	Long: `Apply opinionated rules to a dashboard and report findings.

Rules checked:
  - Anonymous widgets (no title)
  - Group widgets that are empty
  - Timeseries / query_value with no query at all
  - Queries that ignore env / service (often a sign of cross-env leakage)
  - Ratio queries with no denominator
  - Markers/thresholds missing on error-rate / latency widgets
  - Template variables declared but unused in any widget query
  - Dashboard with empty description
  - Note widgets with placeholder text ("Lorem ipsum", "TODO")

Output:
  • TTY    → grouped findings with colored severity
  • Piped  → TSV (severity, widget_index, rule, message)
  • --json → array of findings

Exit codes:
  0  no findings, or --exit-nonzero not set
  1  --exit-nonzero set AND there are warnings/errors

Examples:
  datadog dashboards lint abc-123
  datadog dashboards lint abc-123 --only error --exit-nonzero    # CI mode
  datadog dashboards lint abc-123 --json | jq '.[] | select(.severity=="error")'`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickDashboardID(args)
		if err != nil {
			return err
		}
		dash, err := client.GetDashboard(id)
		if err != nil {
			return err
		}

		findings := lintDashboard(dash)
		if lintShowOnly != "" {
			filtered := findings[:0]
			for _, f := range findings {
				if strings.EqualFold(f.Severity, lintShowOnly) {
					filtered = append(filtered, f)
				}
			}
			findings = filtered
		}

		// Stable sort: severity asc (error,warning,info), then widget_index
		severityRank := map[string]int{"error": 0, "warning": 1, "info": 2}
		sort.SliceStable(findings, func(i, j int) bool {
			ri := severityRank[findings[i].Severity]
			rj := severityRank[findings[j].Severity]
			if ri != rj {
				return ri < rj
			}
			return findings[i].WidgetIndex < findings[j].WidgetIndex
		})

		if lintJSON {
			if err := printJSON(findings); err != nil {
				return err
			}
		} else if !isTTY() {
			fmt.Println("SEVERITY\tWIDGET\tRULE\tMESSAGE")
			for _, f := range findings {
				fmt.Printf("%s\t%s\t%s\t%s\n", f.Severity, f.WidgetPath, f.Rule, f.Message)
			}
		} else {
			printLintTTY(dash, findings)
		}

		if lintExitNonZero {
			for _, f := range findings {
				if f.Severity == "error" || f.Severity == "warning" {
					return fmt.Errorf("found %d lint findings (warnings or errors)", len(findings))
				}
			}
		}
		return nil
	},
}

type lintFinding struct {
	Severity    string `json:"severity"` // error | warning | info
	Rule        string `json:"rule"`
	Message     string `json:"message"`
	WidgetIndex int    `json:"widget_index"`
	WidgetPath  string `json:"widget_path"`
}

// lintDashboard returns findings for a dashboard map. Pure function — easy to test.
func lintDashboard(dash map[string]interface{}) []lintFinding {
	var findings []lintFinding

	// Rule: empty description
	if s, _ := dash["description"].(string); strings.TrimSpace(s) == "" {
		findings = append(findings, lintFinding{
			Severity: "info",
			Rule:     "empty-description",
			Message:  "Dashboard has no description. Future-you (or oncall) will thank present-you for one.",
		})
	}

	// Template variables: declared but unused?
	templateVars, _ := dash["template_variables"].([]interface{})
	declared := map[string]bool{}
	for _, tv := range templateVars {
		m, _ := tv.(map[string]interface{})
		name, _ := m["name"].(string)
		if name != "" {
			declared[name] = true
		}
	}
	usedVars := map[string]bool{}

	// Walk widgets
	widgets, _ := dash["widgets"].([]interface{})
	walkWidgets(widgets, "widgets", &findings, usedVars)

	for v := range declared {
		if !usedVars[v] {
			findings = append(findings, lintFinding{
				Severity: "warning",
				Rule:     "unused-template-variable",
				Message:  fmt.Sprintf("Template variable $%s is declared but never referenced in any widget query.", v),
			})
		}
	}

	return findings
}

// walkWidgets descends into widgets, applying per-widget rules.
func walkWidgets(widgets []interface{}, path string, findings *[]lintFinding, usedVars map[string]bool) {
	for idx, w := range widgets {
		m, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		def, _ := m["definition"].(map[string]interface{})
		if def == nil {
			continue
		}
		wType, _ := def["type"].(string)
		wPath := fmt.Sprintf("%s[%d]", path, idx)
		title, _ := def["title"].(string)

		// Skip pure note widgets (titles aren't expected) and recurse into groups
		if wType == "group" {
			if title == "" {
				*findings = append(*findings, lintFinding{
					Severity: "info", Rule: "group-no-title",
					WidgetIndex: idx, WidgetPath: wPath,
					Message: "Group widget has no title.",
				})
			}
			inner, _ := def["widgets"].([]interface{})
			if len(inner) == 0 {
				*findings = append(*findings, lintFinding{
					Severity: "warning", Rule: "empty-group",
					WidgetIndex: idx, WidgetPath: wPath,
					Message: "Group widget is empty.",
				})
			}
			walkWidgets(inner, wPath+".widgets", findings, usedVars)
			continue
		}

		// Note widget: check for placeholder text
		if wType == "note" {
			content, _ := def["content"].(string)
			low := strings.ToLower(content)
			for _, marker := range []string{"lorem ipsum", "todo:", "tbd"} {
				if strings.Contains(low, marker) {
					*findings = append(*findings, lintFinding{
						Severity: "info", Rule: "note-placeholder",
						WidgetIndex: idx, WidgetPath: wPath,
						Message: fmt.Sprintf("Note contains placeholder text (%q).", marker),
					})
				}
			}
			continue
		}

		// Anonymous widget — most widget types should have a title, but
		// streams/timelines are self-explanatory and rarely titled.
		if title == "" && !widgetTypeAllowsNoTitle(wType) {
			*findings = append(*findings, lintFinding{
				Severity: "info", Rule: "widget-no-title",
				WidgetIndex: idx, WidgetPath: wPath,
				Message: fmt.Sprintf("%s widget has no title.", wType),
			})
		}

		// Collect queries and check rules
		queries := extractQueries(def)
		if len(queries) == 0 && needsQuery(wType) {
			*findings = append(*findings, lintFinding{
				Severity: "error", Rule: "widget-no-query",
				WidgetIndex: idx, WidgetPath: wPath,
				Message: fmt.Sprintf("%s widget has no query.", wType),
			})
		}

		hasEnv := false
		hasService := false
		for _, q := range queries {
			if strings.Contains(q, "env:") || strings.Contains(q, "$env") {
				hasEnv = true
			}
			if strings.Contains(q, "service:") || strings.Contains(q, "$service") {
				hasService = true
			}
			// Track template variable usage — match `$name` anywhere in the query.
			for _, m := range tmplVarRe.FindAllStringSubmatch(q, -1) {
				usedVars[m[1]] = true
			}
		}
		if len(queries) > 0 && !hasEnv {
			*findings = append(*findings, lintFinding{
				Severity: "warning", Rule: "query-no-env",
				WidgetIndex: idx, WidgetPath: wPath,
				Message: "Widget query does not filter by env — risk of cross-env data leak.",
			})
		}
		if len(queries) > 0 && !hasService && !looksInfraWidget(wType, title) {
			*findings = append(*findings, lintFinding{
				Severity: "info", Rule: "query-no-service",
				WidgetIndex: idx, WidgetPath: wPath,
				Message: "Widget query does not filter by service.",
			})
		}

		// Ratio-style formulas: detect "errs / hits" patterns (two query refs
		// divided) and ensure both queries exist. Plain `q / 1000000` unit
		// conversions are NOT ratios and are ignored.
		formulas := extractFormulas(def)
		queryNames := extractQueryNames(def)
		for _, f := range formulas {
			if !looksLikeQueryRatio(f, queryNames) {
				continue
			}
			if len(queries) < 2 {
				*findings = append(*findings, lintFinding{
					Severity: "warning", Rule: "ratio-missing-denominator",
					WidgetIndex: idx, WidgetPath: wPath,
					Message: fmt.Sprintf("Formula %q looks like a ratio but the widget has fewer than 2 queries.", f),
				})
			}
		}

		// Error / latency widgets without thresholds (markers). Markers only
		// make sense on timeseries / query_value / topology widgets.
		titleLow := strings.ToLower(title)
		isErrLatencyWidget := strings.Contains(titleLow, "error") || strings.Contains(titleLow, "latency") ||
			strings.Contains(titleLow, "p95") || strings.Contains(titleLow, "p99") ||
			strings.Contains(titleLow, "5xx")
		if isErrLatencyWidget && (wType == "timeseries" || wType == "query_value") && !hasMarkers(def) {
			*findings = append(*findings, lintFinding{
				Severity: "info", Rule: "missing-threshold-marker",
				WidgetIndex: idx, WidgetPath: wPath,
				Message: "Error/latency widget has no threshold markers — readers can't tell what's bad at a glance.",
			})
		}
	}
}

// extractQueries pulls all the query strings out of a widget definition
// across the various widget shapes (timeseries, query_value, toplist, etc.).
func extractQueries(def map[string]interface{}) []string {
	var out []string
	reqs, _ := def["requests"].([]interface{})
	for _, r := range reqs {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		// v1 shape: r.q
		if q, ok := rm["q"].(string); ok && q != "" {
			out = append(out, q)
		}
		// v2 shape: r.queries[].query
		qs, _ := rm["queries"].([]interface{})
		for _, q := range qs {
			qm, ok := q.(map[string]interface{})
			if !ok {
				continue
			}
			if s, ok := qm["query"].(string); ok && s != "" {
				out = append(out, s)
			}
			if s, ok := qm["search"].(map[string]interface{}); ok {
				if q, ok := s["query"].(string); ok && q != "" {
					out = append(out, q)
				}
			}
		}
	}
	// Log stream and event widgets store the query at the top level
	for _, k := range []string{"query"} {
		if q, ok := def[k].(string); ok && q != "" {
			out = append(out, q)
		}
	}
	return out
}

// extractFormulas pulls formula expressions out of v2 requests.
func extractFormulas(def map[string]interface{}) []string {
	var out []string
	reqs, _ := def["requests"].([]interface{})
	for _, r := range reqs {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		fs, _ := rm["formulas"].([]interface{})
		for _, f := range fs {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			if s, ok := fm["formula"].(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func hasMarkers(def map[string]interface{}) bool {
	m, ok := def["markers"].([]interface{})
	return ok && len(m) > 0
}

// extractQueryNames returns the `name` attribute of each query in v2 requests.
// These are the identifiers a formula can reference.
func extractQueryNames(def map[string]interface{}) map[string]bool {
	out := map[string]bool{}
	reqs, _ := def["requests"].([]interface{})
	for _, r := range reqs {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		qs, _ := rm["queries"].([]interface{})
		for _, q := range qs {
			qm, ok := q.(map[string]interface{})
			if !ok {
				continue
			}
			if name, ok := qm["name"].(string); ok && name != "" {
				out[name] = true
			}
		}
	}
	return out
}

// looksLikeQueryRatio returns true when the formula contains a `/` AND both
// the left and right operands of (at least one) division are query name
// references — not numeric constants. So "errs / hits" is a ratio; "p95 /
// 1000000" or "(throttled) / 10" is not.
func looksLikeQueryRatio(formula string, queryNames map[string]bool) bool {
	if !strings.Contains(formula, "/") {
		return false
	}
	// Walk through each `/` and inspect the immediate neighbors.
	for i, ch := range formula {
		if ch != '/' {
			continue
		}
		left := strings.TrimSpace(beforeIdent(formula[:i]))
		right := strings.TrimSpace(afterIdent(formula[i+1:]))
		if isQueryRef(left, queryNames) && isQueryRef(right, queryNames) {
			return true
		}
	}
	return false
}

// beforeIdent returns the last identifier-like token in s.
func beforeIdent(s string) string {
	all := queryRefRe.FindAllString(s, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// afterIdent returns the first identifier-like token in s.
func afterIdent(s string) string {
	m := queryRefRe.FindString(s)
	return m
}

func isQueryRef(tok string, queryNames map[string]bool) bool {
	if tok == "" {
		return false
	}
	if _, ok := queryNames[tok]; ok {
		return true
	}
	return false
}

// needsQuery returns true for widget types that don't make sense without one.
func needsQuery(t string) bool {
	switch t {
	case "note", "free_text", "image", "iframe", "group", "powerpack":
		return false
	}
	return true
}

// widgetTypeAllowsNoTitle returns true for widgets that are conventionally
// rendered without a title (stream-style content explains itself).
func widgetTypeAllowsNoTitle(t string) bool {
	switch t {
	case "free_text", "note",
		"event_timeline", "event_stream",
		"log_stream", "query_stream",
		"image", "iframe":
		return true
	}
	return false
}

// looksInfraWidget returns true for widgets that legitimately span the whole
// fleet rather than a single service (e.g. host map, cluster view, k8s).
func looksInfraWidget(t, title string) bool {
	switch t {
	case "hostmap", "check_status", "manage_status", "trace_service":
		return true
	}
	low := strings.ToLower(title)
	for _, k := range []string{"host map", "cluster", "node count", "kubernetes overview"} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// tokenize splits a query into tokens at whitespace/braces — good enough to
// spot `$var` template references.
func tokenize(q string) []string {
	q = strings.ReplaceAll(q, "{", " ")
	q = strings.ReplaceAll(q, "}", " ")
	q = strings.ReplaceAll(q, ",", " ")
	q = strings.ReplaceAll(q, "(", " ")
	q = strings.ReplaceAll(q, ")", " ")
	return strings.Fields(q)
}

func printLintTTY(dash map[string]interface{}, findings []lintFinding) {
	title, _ := dash["title"].(string)
	id, _ := dash["id"].(string)
	fmt.Println(ui.Title.Render(fmt.Sprintf(" lint · %s · %s", id, title)))

	if len(findings) == 0 {
		fmt.Println(ui.SuccessStyle.Render("  OK  no findings"))
		return
	}

	byRule := map[string][]lintFinding{}
	for _, f := range findings {
		byRule[f.Rule] = append(byRule[f.Rule], f)
	}
	// Print grouped by severity then rule
	for _, severity := range []string{"error", "warning", "info"} {
		var inSev []lintFinding
		for _, f := range findings {
			if f.Severity == severity {
				inSev = append(inSev, f)
			}
		}
		if len(inSev) == 0 {
			continue
		}
		badge := ""
		switch severity {
		case "error":
			badge = ui.ErrorStyle.Render(strings.ToUpper(severity))
		case "warning":
			badge = ui.MonitorStateBadge("Warn")
		default:
			badge = ui.Dimmed.Render(strings.ToUpper(severity))
		}
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  %s · %d", strings.Title(severity), len(inSev))))
		for _, f := range inSev {
			fmt.Printf("  %s  %s\n      %s\n      %s\n",
				badge, f.Message,
				ui.Dimmed.Render(fmt.Sprintf("rule: %s", f.Rule)),
				ui.Dimmed.Render(fmt.Sprintf("path: %s", f.WidgetPath)))
		}
	}
	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d findings · %s", len(findings), time.Now().Format("15:04:05"))))
}

func init() {
	dashboardsLintCmd.Flags().BoolVar(&lintJSON, "json", false, "Output findings as JSON")
	dashboardsLintCmd.Flags().StringVar(&lintShowOnly, "only", "", "Show only findings of severity: error | warning | info")
	dashboardsLintCmd.Flags().BoolVar(&lintExitNonZero, "exit-nonzero", false, "Exit 1 if any warning or error finding (for CI)")
	dashboardsCmd.AddCommand(dashboardsLintCmd)
}
