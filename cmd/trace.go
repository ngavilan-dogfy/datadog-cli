package cmd

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	traceWin  windowFlags
	traceJSON bool
	traceMD   bool
	traceLogs int
	traceMax  int
	traceRows int
)

type traceSpan struct {
	SpanID    string    `json:"span_id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Depth     int       `json:"depth"`
	Service   string    `json:"service"`
	Operation string    `json:"operation,omitempty"`
	Resource  string    `json:"resource"`
	Kind      string    `json:"type,omitempty"` // web, db, http, cache…
	Peer      string    `json:"peer,omitempty"` // the host or service it called
	Start     time.Time `json:"start"`
	Offset    float64   `json:"offset_ms"` // since the trace started
	Duration  float64   `json:"duration_ms"`
	Self      float64   `json:"self_ms"` // not spent waiting on child spans
	Error     bool      `json:"error,omitempty"`
	ErrorType string    `json:"error_type,omitempty"`
	ErrorMsg  string    `json:"error_message,omitempty"`
	HTTP      string    `json:"http,omitempty"` // "GET /api/x → 404"
	Critical  bool      `json:"critical_path,omitempty"`
	Orphan    bool      `json:"parent_not_indexed,omitempty"`
	children  []*traceSpan
	parent    *traceSpan
	end       time.Time
}

type traceLog struct {
	Time    time.Time `json:"time"`
	Status  string    `json:"status"`
	Service string    `json:"service"`
	Message string    `json:"message"`
}

// serviceTime is the own time of one service and operation (fastify.request,
// mongodb.query, http.request…): code vs database vs calls out.
type serviceTime struct {
	Service   string  `json:"service"`
	Operation string  `json:"operation,omitempty"`
	SelfMS    float64 `json:"self_ms"`
	Spans     int     `json:"spans"`
	Errors    int     `json:"errors,omitempty"`
}

// traceRepeat is one parent calling the same thing many times: the N+1
// signature.
type traceRepeat struct {
	Service  string  `json:"service"`
	Resource string  `json:"resource"`
	Count    int     `json:"count"`
	TotalMS  float64 `json:"total_ms"`
	Caller   string  `json:"caller"`
}

type traceReport struct {
	TraceID    string         `json:"trace_id"`
	Start      time.Time      `json:"start"`
	Duration   float64        `json:"duration_ms"`
	Services   []string       `json:"services"`
	ByService  []serviceTime  `json:"own_time"` // by service and operation
	Errors     int            `json:"errors"`
	Partial    bool           `json:"partial"`             // some parents weren't indexed (sampling)
	Truncated  bool           `json:"truncated,omitempty"` // more spans than --max-spans
	RetainedBy map[string]int `json:"retained_by,omitempty"`
	Repeats    []traceRepeat  `json:"repeated_calls,omitempty"`
	Spans      []*traceSpan   `json:"spans"`
	Logs       []traceLog     `json:"logs,omitempty"`
	LogsErr    string         `json:"logs_error,omitempty"`
	roots      []*traceSpan
}

var traceCmd = &cobra.Command{
	Use:   "trace <trace-id | trace link>",
	Short: "Follow one request across services: span tree, where time went, errors, logs",
	Long: `Rebuild one distributed trace from its indexed spans: which service called
which, how long each step took and how much of it was its own work, the
critical path, repeated calls (N+1), the errors with their likely origin, and
the logs written during the request.

The id can be decimal (dd.trace_id in logs), hex (16 or 32 digits), or a
Datadog trace link. Get one from 'datadog traces … --json' or a log.

Only indexed spans can be found: with sampling a trace may be partial (the
report says so; error spans are usually kept). It looks back 1 day, and 15
days when it isn't there (--since/--from to choose).

Output:
  Terminal: a waterfall · --md: markdown for prompts · --json: every span

Examples:
  datadog trace 68f2a1c40000000071d3b2e59a0c4f17
  datadog trace https://app.datadoghq.eu/apm/trace/8202096045573558039
  datadog trace 8202096045573558039 --md
  datadog traces "service:api status:error" -n 1 --json | jq -r '.[0].trace_id' | xargs datadog trace`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := traceIDFromArg(args[0])
		if err != nil {
			return err
		}
		from, to, err := traceWin.resolve()
		if err != nil {
			return err
		}
		rep, err := buildTrace(id, from, to, traceMax, traceLogs)
		if err == errNoSpans && !cmd.Flags().Changed("since") && !cmd.Flags().Changed("from") {
			from = to.Add(-15 * 24 * time.Hour)
			rep, err = buildTrace(id, from, to, traceMax, traceLogs)
		}
		if err == errNoSpans {
			return fmt.Errorf("no indexed spans for trace %s since %s: check the id, or it wasn't retained (sampling)", id, from.Local().Format("Jan 2 15:04"))
		}
		if err != nil {
			return err
		}
		switch {
		case traceJSON:
			return printJSON(rep)
		case traceMD:
			fmt.Print(rep.markdown(traceRows))
			return nil
		}
		fmt.Print(rep.text(traceRows, isTTY()))
		return nil
	},
}

var (
	reTraceURL = regexp.MustCompile(`/apm/trace/([0-9a-fA-Fx]+)`)
	errNoSpans = fmt.Errorf("no spans")
)

// traceIDFromArg accepts a trace id in any usual form, or a link to one, and
// returns a form span and log search understand: 32-digit hex or decimal.
func traceIDFromArg(arg string) (string, error) {
	s := strings.TrimSpace(arg)
	if m := reTraceURL.FindStringSubmatch(s); m != nil {
		s = m[1]
	} else if u, err := url.Parse(s); err == nil && u.Host != "" {
		q := u.Query()
		for _, k := range []string{"traceID", "trace_id", "traceId"} {
			if v := q.Get(k); v != "" {
				s = v
				break
			}
		}
	}
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	isDigits := s != "" && strings.Trim(s, "0123456789") == ""
	isHex := s != "" && strings.Trim(s, "0123456789abcdef") == ""
	switch {
	case isDigits:
		return s, nil
	case isHex && len(s) == 32:
		return s, nil
	case isHex && len(s) <= 16:
		v, _ := strconv.ParseUint(s, 16, 64)
		return strconv.FormatUint(v, 10), nil
	}
	return "", fmt.Errorf("%q isn't a trace id: use the decimal id, 16 or 32 hex digits, or a trace link", arg)
}

func buildTrace(id string, from, to time.Time, maxSpans, logs int) (*traceReport, error) {
	f, t := from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)
	var raw []datadog.SpanData
	truncated, cursor := false, ""
	for {
		page := min(1000, maxSpans-len(raw))
		resp, err := client.SearchSpansPage("trace_id:"+id, f, t, page, "timestamp", cursor)
		if err != nil {
			return nil, err
		}
		raw = append(raw, resp.Data...)
		cursor = resp.Meta.Page.After
		if cursor == "" || len(resp.Data) == 0 {
			break
		}
		if len(raw) >= maxSpans {
			truncated = true
			break
		}
	}
	if len(raw) == 0 {
		return nil, errNoSpans
	}
	rep := assembleTrace(id, raw)
	rep.Truncated = truncated
	if logs > 0 {
		pad := time.Minute
		end := rep.Start.Add(time.Duration(rep.Duration * float64(time.Millisecond)))
		lr, err := client.SearchLogs("trace_id:"+id, rep.Start.Add(-pad).UTC().Format(time.RFC3339), end.Add(pad).UTC().Format(time.RFC3339), logs)
		if err != nil {
			rep.LogsErr = err.Error()
		} else {
			for _, l := range lr.Data {
				t, _ := time.Parse(time.RFC3339Nano, l.Attributes.Timestamp)
				rep.Logs = append(rep.Logs, traceLog{Time: t, Status: l.Attributes.Status, Service: l.Attributes.Service, Message: oneLine(l.Attributes.Message, 240)})
			}
			sort.SliceStable(rep.Logs, func(i, j int) bool { return rep.Logs[i].Time.Before(rep.Logs[j].Time) })
		}
	}
	return rep, nil
}

// assembleTrace builds the tree and its analysis from a trace's spans.
func assembleTrace(id string, raw []datadog.SpanData) *traceReport {
	rep := &traceReport{TraceID: id, RetainedBy: map[string]int{}}
	byID := map[string]*traceSpan{}
	var spans []*traceSpan
	for _, d := range raw {
		s := spanFrom(d.Attributes)
		if byID[s.SpanID] != nil {
			continue
		}
		byID[s.SpanID] = s
		spans = append(spans, s)
		if d.Attributes.RetainedBy != "" {
			rep.RetainedBy[d.Attributes.RetainedBy]++
		}
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	start, end := spans[0].Start, spans[0].end
	services := map[string]*serviceTime{}
	for _, s := range spans {
		if s.end.After(end) {
			end = s.end
		}
		if p := byID[s.ParentID]; p != nil && p != s {
			s.parent = p
			p.children = append(p.children, s)
		} else {
			s.Orphan = s.ParentID != "" && s.ParentID != "0"
			rep.roots = append(rep.roots, s)
		}
	}
	rep.Start = start
	rep.Duration = ms(end.Sub(start))
	rep.Partial = len(rep.roots) > 1 || rep.roots[0].Orphan

	var walk func(s *traceSpan, depth int)
	walk = func(s *traceSpan, depth int) {
		s.Depth = depth
		s.Offset = ms(s.Start.Sub(start))
		s.Self = math.Max(0, s.Duration-ms(covered(s)))
		rep.Spans = append(rep.Spans, s)
		st := services[s.Service+"\x00"+s.Operation]
		if st == nil {
			st = &serviceTime{Service: s.Service, Operation: s.Operation}
			services[s.Service+"\x00"+s.Operation] = st
		}
		st.SelfMS += s.Self
		st.Spans++
		if s.Error {
			st.Errors++
			rep.Errors++
		}
		for _, c := range s.children {
			walk(c, depth+1)
		}
	}
	for _, r := range rep.roots {
		walk(r, 0)
	}
	seen := map[string]bool{}
	for _, st := range services {
		if !seen[st.Service] {
			seen[st.Service] = true
			rep.Services = append(rep.Services, st.Service)
		}
		rep.ByService = append(rep.ByService, *st)
	}
	sort.Strings(rep.Services)
	sort.Slice(rep.ByService, func(i, j int) bool { return rep.ByService[i].SelfMS > rep.ByService[j].SelfMS })

	// The critical path starts at the longest root.
	longest := rep.roots[0]
	for _, r := range rep.roots {
		if r.Duration > longest.Duration {
			longest = r
		}
	}
	markCritical(longest, longest.end)
	rep.Repeats = repeatedCalls(rep.Spans)
	return rep
}

// markCritical marks what the request waited on: walking back from the end
// of a span, the child that finished last, then the one that finished last
// before that one started, and so on — recursively.
func markCritical(s *traceSpan, until time.Time) {
	s.Critical = true
	const slack = time.Millisecond // timestamps are rounded
	t := until
	if s.end.Before(t) {
		t = s.end
	}
	done := map[*traceSpan]bool{}
	for {
		var next *traceSpan
		for _, c := range s.children {
			if done[c] || !c.Start.Before(t) || c.end.After(t.Add(slack)) {
				continue
			}
			if next == nil || c.end.After(next.end) {
				next = c
			}
		}
		if next == nil {
			return
		}
		done[next] = true
		markCritical(next, t)
		t = next.Start
	}
}

// covered is how long a span's children kept it busy (overlaps once).
func covered(s *traceSpan) time.Duration {
	type iv struct{ a, b time.Time }
	var ivs []iv
	for _, c := range s.children {
		a, b := c.Start, c.end
		if a.Before(s.Start) {
			a = s.Start
		}
		if b.After(s.end) {
			b = s.end
		}
		if b.After(a) {
			ivs = append(ivs, iv{a, b})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].a.Before(ivs[j].a) })
	var total time.Duration
	for i := 0; i < len(ivs); {
		a, b := ivs[i].a, ivs[i].b
		j := i + 1
		for ; j < len(ivs) && !ivs[j].a.After(b); j++ {
			if ivs[j].b.After(b) {
				b = ivs[j].b
			}
		}
		total += b.Sub(a)
		i = j
	}
	return total
}

// repeatedCalls finds parents calling the same service and resource 5+ times.
func repeatedCalls(spans []*traceSpan) []traceRepeat {
	type key struct{ parent, service, resource string }
	groups := map[key]*traceRepeat{}
	var order []key
	for _, s := range spans {
		if s.parent == nil {
			continue
		}
		k := key{s.parent.SpanID, s.Service, s.Resource}
		g := groups[k]
		if g == nil {
			g = &traceRepeat{Service: s.Service, Resource: s.Resource, Caller: s.parent.label()}
			groups[k] = g
			order = append(order, k)
		}
		g.Count++
		g.TotalMS += s.Duration
	}
	var out []traceRepeat
	for _, k := range order {
		if g := groups[k]; g.Count >= 5 {
			out = append(out, *g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TotalMS > out[j].TotalMS })
	return out
}

func spanFrom(a datadog.SpanAttributes) *traceSpan {
	s := &traceSpan{SpanID: a.SpanID, ParentID: a.ParentID, Service: a.Service, Operation: a.OperationName, Resource: a.ResourceName, Kind: a.Type}
	s.Start, _ = time.Parse(time.RFC3339Nano, a.StartTimestamp)
	s.end, _ = time.Parse(time.RFC3339Nano, a.EndTimestamp)
	if d, ok := numAt(a.Custom, "duration"); ok && d > 0 {
		s.Duration = d / 1e6
		s.end = s.Start.Add(time.Duration(d))
	} else {
		s.Duration = ms(s.end.Sub(s.Start))
	}
	for _, p := range [][]string{{"peer", "service"}, {"peer", "hostname"}, {"out", "host"}, {"network", "destination", "name"}} {
		if s.Peer = strAt(a.Custom, p...); s.Peer != "" {
			break
		}
	}
	s.Error = a.Status == "error"
	s.ErrorType = strAt(a.Custom, "error", "type")
	s.ErrorMsg = oneLine(strAt(a.Custom, "error", "message"), 200)
	method, code := strAt(a.Custom, "http", "method"), strAt(a.Custom, "http", "status_code")
	path := strAt(a.Custom, "http", "url_details", "path")
	if path == "" {
		path = strAt(a.Custom, "http", "url")
	}
	if method != "" || code != "" {
		s.HTTP = strings.TrimSpace(method + " " + path)
		if code != "" {
			s.HTTP += " → " + code
		}
	}
	return s
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func at(m map[string]interface{}, path ...string) interface{} {
	var cur interface{} = m
	for _, p := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func strAt(m map[string]interface{}, path ...string) string {
	switch v := at(m, path...).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func numAt(m map[string]interface{}, path ...string) (float64, bool) {
	switch v := at(m, path...).(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

func oneLine(s string, n int) string {
	return truncRunes(strings.Join(strings.Fields(s), " "), n)
}

func fmtMS(v float64) string {
	switch {
	case v >= 1000:
		return strconv.FormatFloat(v/1000, 'f', 2, 64) + "s"
	case v >= 10:
		return strconv.FormatFloat(v, 'f', 0, 64) + "ms"
	case v >= 1:
		return strconv.FormatFloat(v, 'f', 1, 64) + "ms"
	}
	return strconv.FormatFloat(v*1000, 'f', 0, 64) + "µs"
}

func (s *traceSpan) label() string {
	l := s.Service
	if s.Peer != "" && (s.Kind == "http" || s.Kind == "grpc" || s.Kind == "rpc") {
		l += " → " + s.Peer
	}
	l += " " + s.Resource
	if s.Operation != "" && s.Operation != s.Resource && !strings.Contains(s.Resource, s.Operation) {
		l += " (" + s.Operation + ")"
	}
	return l
}

func (s *traceSpan) detail() string {
	return strings.Trim(strings.TrimSpace(s.ErrorType+": "+s.ErrorMsg), ": ")
}

// traceRow is one line of the waterfall: a span, or a run of identical
// sibling leaf spans folded into one (×N).
type traceRow struct {
	span       *traceSpan
	n          int
	start, end time.Time
	total      float64
	err, crit  bool
}

func (r *traceReport) rows() []traceRow {
	var out []traceRow
	var visit func(s *traceSpan)
	visit = func(s *traceSpan) {
		out = append(out, traceRow{span: s, n: 1, start: s.Start, end: s.end, total: s.Duration, err: s.Error, crit: s.Critical})
		kids := s.children
		for i := 0; i < len(kids); {
			j := i + 1
			if len(kids[i].children) == 0 {
				for j < len(kids) && len(kids[j].children) == 0 && kids[j].Service == kids[i].Service && kids[j].Resource == kids[i].Resource {
					j++
				}
			}
			if j-i >= 3 {
				row := traceRow{span: kids[i], n: j - i, start: kids[i].Start, end: kids[i].end}
				for _, k := range kids[i:j] {
					row.total += k.Duration
					row.err = row.err || k.Error
					row.crit = row.crit || k.Critical
					if k.end.After(row.end) {
						row.end = k.end
					}
				}
				out = append(out, row)
			} else {
				for _, k := range kids[i:j] {
					visit(k)
				}
			}
			i = j
		}
	}
	for _, root := range r.roots {
		visit(root)
	}
	return out
}

// pick keeps at most limit rows: the critical path, errors and the top of
// the tree first, then the rest in order.
func pick(rows []traceRow, limit int) ([]traceRow, int) {
	if limit <= 0 || len(rows) <= limit {
		return rows, 0
	}
	keep := make([]bool, len(rows))
	n := 0
	for i, r := range rows {
		if (r.crit || r.err || r.span.Depth <= 1) && n < limit {
			keep[i] = true
			n++
		}
	}
	for i := range rows {
		if !keep[i] && n < limit {
			keep[i] = true
			n++
		}
	}
	var out []traceRow
	for i, r := range rows {
		if keep[i] {
			out = append(out, r)
		}
	}
	return out, len(rows) - len(out)
}

func (r *traceReport) summaryLine() string {
	s := fmt.Sprintf("trace %s · %s · %s · %s", r.TraceID, fmtMS(r.Duration), plural(len(r.Spans), "span"), strings.Join(r.Services, ", "))
	if r.Errors > 0 {
		s += " · " + plural(r.Errors, "error")
	}
	return s + " · " + r.Start.Local().Format("Jan 2 15:04:05") + " " + zoneName()
}

func (r *traceReport) notes() []string {
	var n []string
	if r.Partial {
		kept := ""
		if len(r.RetainedBy) > 0 {
			var parts []string
			for _, k := range sortedCountKeys(r.RetainedBy) {
				parts = append(parts, fmt.Sprintf("%s %d", k, r.RetainedBy[k]))
			}
			kept = " (kept by " + strings.Join(parts, ", ") + ")"
		}
		n = append(n, "partial: some parent spans weren't indexed"+kept+"; the tree starts where the data does")
	}
	if r.Truncated {
		n = append(n, fmt.Sprintf("truncated: only the first %d spans (--max-spans)", len(r.Spans)))
	}
	return n
}

func sortedCountKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || (m[keys[i]] == m[keys[j]] && keys[i] < keys[j]) })
	return keys
}

func (r *traceReport) bar(row traceRow, width int) string {
	if r.Duration <= 0 {
		return strings.Repeat("█", width)
	}
	off := ms(row.start.Sub(r.Start))
	a := int(math.Floor(off / r.Duration * float64(width)))
	b := int(math.Ceil((off + ms(row.end.Sub(row.start))) / r.Duration * float64(width)))
	a = max(0, min(width-1, a))
	b = max(a+1, min(width, b))
	return strings.Repeat("·", a) + strings.Repeat("█", b-a) + strings.Repeat("·", width-b)
}

func (row traceRow) text() string {
	s := row.span
	l := s.label()
	if row.n > 1 {
		return l + fmt.Sprintf(" ×%d (%s in total)", row.n, fmtMS(row.total))
	}
	if s.HTTP != "" {
		l += "  " + s.HTTP
	}
	if d := s.detail(); d != "" {
		l += "  — " + d
	}
	return l
}

func (r *traceReport) text(limit int, tty bool) string {
	var b strings.Builder
	style := func(st lipgloss.Style, s string) string {
		if tty {
			return st.Render(s)
		}
		return s
	}
	critStyle := lipgloss.NewStyle().Foreground(ui.Primary)
	b.WriteString(style(ui.Title, r.summaryLine()) + "\n")
	for _, n := range r.notes() {
		b.WriteString(style(ui.Dimmed, "  "+n) + "\n")
	}
	b.WriteString("\n")
	rows, hidden := pick(r.rows(), limit)
	for _, row := range rows {
		mark, barStyle, textStyle := " ", ui.Dimmed, lipgloss.NewStyle()
		switch {
		case row.err:
			mark, barStyle, textStyle = "✗", ui.ErrorStyle, ui.ErrorStyle
		case row.crit:
			mark, barStyle = "›", critStyle
		}
		fmt.Fprintf(&b, "  %s %s %8s %8s  %s%s\n", style(textStyle, mark), style(barStyle, r.bar(row, 24)),
			fmtMS(ms(row.start.Sub(r.Start))), fmtMS(ms(row.end.Sub(row.start))),
			strings.Repeat("  ", row.span.Depth), style(textStyle, row.text()))
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "  … %d more rows (--rows 0 shows all; --json has every span)\n", hidden)
	}
	b.WriteString(style(ui.Dimmed, "\n  timeline · start · duration · span   › critical path  ✗ error") + "\n")
	r.writeInsights(&b, "  ")
	return b.String()
}

func (r *traceReport) markdown(limit int) string {
	var b strings.Builder
	b.WriteString("### " + r.summaryLine() + "\n\n")
	for _, n := range r.notes() {
		b.WriteString("_" + n + "_\n\n")
	}
	b.WriteString("```\n  start    duration span   (› critical path, ✗ error)\n")
	rows, hidden := pick(r.rows(), limit)
	for _, row := range rows {
		mark := " "
		if row.err {
			mark = "✗"
		} else if row.crit {
			mark = "›"
		}
		fmt.Fprintf(&b, "%s +%-7s %-8s %s%s\n", mark, fmtMS(ms(row.start.Sub(r.Start))), fmtMS(ms(row.end.Sub(row.start))), strings.Repeat("  ", row.span.Depth), row.text())
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "… %d more rows\n", hidden)
	}
	b.WriteString("```\n")
	r.writeInsights(&b, "")
	return b.String()
}

// writeInsights says what an agent would look for: where the time went, the
// repeated calls, the errors with their likely origin, and the logs.
func (r *traceReport) writeInsights(b *strings.Builder, indent string) {
	// Shares of all the work done: async work can outlast the request, so
	// the trace's duration isn't the total.
	work := 0.0
	for _, st := range r.ByService {
		work += st.SelfMS
	}
	pct := func(v float64) float64 {
		if work <= 0 {
			return 0
		}
		return v / work * 100
	}
	b.WriteString("\n" + indent + "Own time by service and operation (share of all work):\n")
	for _, st := range r.ByService {
		name := st.Service
		if st.Operation != "" {
			name += " · " + st.Operation
		}
		line := fmt.Sprintf("%s  %-36s %8s %4.0f%%  %s", indent, name, fmtMS(st.SelfMS), pct(st.SelfMS), plural(st.Spans, "span"))
		if st.Errors > 0 {
			line += ", " + plural(st.Errors, "error")
		}
		b.WriteString(line + "\n")
	}
	slow := append([]*traceSpan(nil), r.Spans...)
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].Self > slow[j].Self })
	if len(slow) > 5 {
		slow = slow[:5]
	}
	b.WriteString("\n" + indent + "Slowest own work (not counting time waiting on child spans):\n")
	for _, s := range slow {
		fmt.Fprintf(b, "%s  %8s %4.0f%%  %s\n", indent, fmtMS(s.Self), pct(s.Self), s.label())
	}
	if len(r.Repeats) > 0 {
		b.WriteString("\n" + indent + "Repeated calls (possible N+1):\n")
		for _, rp := range r.Repeats {
			fmt.Fprintf(b, "%s  %s %s ×%d = %s, from %s\n", indent, rp.Service, oneLine(rp.Resource, 120), rp.Count, fmtMS(rp.TotalMS), rp.Caller)
		}
	}
	var errs []*traceSpan
	for _, s := range r.Spans {
		if s.Error {
			errs = append(errs, s)
		}
	}
	if len(errs) > 0 {
		// Errors bubble up: the deepest one is usually where it started.
		sort.SliceStable(errs, func(i, j int) bool { return errs[i].Depth > errs[j].Depth })
		b.WriteString("\n" + indent + "Errors (deepest first: the first is the likely origin):\n")
		for _, s := range errs {
			line := indent + "  " + s.label()
			if s.HTTP != "" {
				line += "  " + s.HTTP
			}
			if d := s.detail(); d != "" {
				line += "  — " + d
			}
			b.WriteString(line + "\n")
		}
	}
	switch {
	case r.LogsErr != "":
		b.WriteString("\n" + indent + "Logs: " + r.LogsErr + "\n")
	case len(r.Logs) > 0:
		fmt.Fprintf(b, "\n%sLogs of this trace (%d):\n", indent, len(r.Logs))
		for _, l := range r.Logs {
			fmt.Fprintf(b, "%s  %s %-5s %s  %s\n", indent, l.Time.Local().Format("15:04:05.000"), strings.ToUpper(l.Status), l.Service, l.Message)
		}
	}
}

func init() {
	traceWin.register(traceCmd, 24*time.Hour)
	traceCmd.Flags().BoolVar(&traceJSON, "json", false, "Output as JSON (every span)")
	traceCmd.Flags().BoolVar(&traceMD, "md", false, "Output as markdown")
	traceCmd.Flags().IntVar(&traceLogs, "logs", 50, "Logs of the trace to include (0 = none)")
	traceCmd.Flags().IntVar(&traceMax, "max-spans", 5000, "Spans to fetch at most")
	traceCmd.Flags().IntVar(&traceRows, "rows", 60, "Waterfall rows to show (0 = all)")
	rootCmd.AddCommand(traceCmd)
}
