package cmd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

// find turns the words people use ("checkout", "the website", "orders")
// into what exists in Datadog: services, endpoints, monitors, dashboards,
// SLOs, metrics, hosts and logs that mention them — so a question in plain
// language becomes a scope to investigate.

var (
	findWin  windowFlags
	findJSON bool
	findMD   bool
	findEnv  string
)

// findMatch is one thing the words may refer to.
type findMatch struct {
	Kind    string  `json:"kind"` // service, endpoint, monitor, dashboard, slo, metric, host, logs, incident
	Name    string  `json:"name"`
	ID      string  `json:"id,omitempty"`
	Service string  `json:"service,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	Score   float64 `json:"score"`
	URL     string  `json:"url,omitempty"`
	Next    string  `json:"next,omitempty"` // the command to look closer
	weight  int     // activity, to break ties
}

type findReport struct {
	Query   string            `json:"query"`
	Env     string            `json:"env,omitempty"`
	From    time.Time         `json:"from"`
	To      time.Time         `json:"to"`
	Best    *findMatch        `json:"best,omitempty"`
	Matches []findMatch       `json:"matches"`
	Failed  map[string]string `json:"failed,omitempty"`
}

var findCmd = &cobra.Command{
	Use:   "find <words…>",
	Short: "Find what your words refer to: services, endpoints, monitors, dashboards…",
	Long: `Turn the words of a question into what exists in Datadog. "Why is checkout
slow?" — which service is checkout? Is it an endpoint? Which monitors and
dashboards watch it? find looks everywhere at once:

  services     with APM spans or logs (and their Service Catalog entry)
  endpoints    APM resources, like "POST /orders"
  monitors, dashboards, SLOs, incidents   by name
  metrics      whose name contains a word
  hosts        whose name contains a word
  logs         that mention the words, and which services write them

Matching forgives case, plurals, word order and small typos. Each match
says what to run next; the best one comes first.

Output:
  Terminal: matches grouped by kind · --md · --json: {"best", "matches"}

Examples:
  datadog find checkout
  datadog find "web store"
  datadog find orders --json | jq '.best'`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := findWin.resolve()
		if err != nil {
			return err
		}
		rep := runFind(strings.Join(args, " "), findEnv, from, to)
		switch {
		case findJSON:
			return printJSON(rep)
		case findMD:
			fmt.Print(rep.markdown())
			return nil
		}
		fmt.Print(rep.text(isTTY()))
		return nil
	},
}

func init() {
	findWin.register(findCmd, 24*time.Hour)
	findCmd.Flags().StringVar(&findEnv, "env", "", "Only services and endpoints in this env (default: every env)")
	findCmd.Flags().BoolVar(&findJSON, "json", false, "Output as JSON")
	findCmd.Flags().BoolVar(&findMD, "md", false, "Output as markdown")
	rootCmd.AddCommand(findCmd)
}

// ─── matching ────────────────────────────────────────────────────

// stopWords carry no meaning in a search, in English and Spanish.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "in": true, "on": true, "for": true, "and": true, "to": true,
	"el": true, "la": true, "los": true, "las": true, "de": true, "del": true, "en": true, "y": true, "un": true, "una": true,
	"service": true, "servicio": true, "app": true,
}

// words splits text into lowercase words, camelCase and punctuation apart.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
				flush()
			}
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// queryWords are the meaningful words of a search.
func queryWords(q string) []string {
	var out []string
	for _, w := range words(q) {
		if !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

func stem(w string) string {
	for _, suf := range []string{"ies", "es", "s"} {
		if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
			if suf == "ies" {
				return w[:len(w)-3] + "y"
			}
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// editDistance is the Levenshtein distance, for small typos.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// wordScore is how well one search word matches a name's words.
func wordScore(w string, tokens []string, joined string) float64 {
	best := 0.0
	for _, t := range tokens {
		switch {
		case t == w:
			return 1
		case stem(t) == stem(w):
			best = max(best, 0.9)
		case len(w) >= 3 && strings.HasPrefix(t, w):
			best = max(best, 0.8)
		case len(t) >= 4 && 4*len(t) >= 3*len(w) && strings.HasPrefix(w, t):
			best = max(best, 0.6)
		case len(w) >= 5 && editDistance(t, w) <= 1, len(w) >= 8 && editDistance(t, w) <= 2:
			best = max(best, 0.6)
		}
	}
	if best == 0 && len(w) >= 4 && strings.Contains(joined, w) {
		best = 0.6
	}
	return best
}

// matchScore is how well the search words match a name, 0 to 1. Every
// word has to match something for a good score.
func matchScore(q []string, name string) float64 {
	if len(q) == 0 {
		return 0
	}
	tokens := words(name)
	joined := strings.Join(tokens, "")
	if strings.Join(q, "") == joined {
		return 1
	}
	total, missed := 0.0, 0
	for _, w := range q {
		s := wordScore(w, tokens, joined)
		if s == 0 {
			missed++
		}
		total += s
	}
	score := total / float64(len(q))
	if missed > 0 {
		score *= 0.5
	}
	return score
}

// endpointScore matches an endpoint by its own name; with several words,
// the service's name can supply some ("api orders").
func endpointScore(q []string, service, resource string) float64 {
	s := matchScore(q, resource)
	if len(q) > 1 && s > 0 {
		s = max(s, matchScore(q, service+" "+resource)*0.9)
	}
	return s
}

// ─── searching ───────────────────────────────────────────────────

const findThreshold = 0.5

func runFind(query, env string, from, to time.Time) *findReport {
	q := queryWords(query)
	rep := &findReport{Query: query, Env: env, From: from, To: to, Failed: map[string]string{}}
	f, t := from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)
	scope := "*"
	if env != "" {
		scope = "env:" + env
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(m findMatch) {
		if m.Score < findThreshold {
			return
		}
		mu.Lock()
		rep.Matches = append(rep.Matches, m)
		mu.Unlock()
	}
	run := func(source string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				mu.Lock()
				rep.Failed[source] = err.Error()
				mu.Unlock()
			}
		}()
	}
	services := map[string]*findMatch{}
	var svcMu sync.Mutex
	service := func(name string) *findMatch {
		svcMu.Lock()
		defer svcMu.Unlock()
		s := services[name]
		if s == nil {
			s = &findMatch{Kind: "service", Name: name, Score: matchScore(q, name), URL: serviceLink(name, env, "", from, to),
				Next: "datadog investigate " + name}
			services[name] = s
		}
		return s
	}
	var spanCounts, logCounts sync.Map

	run("apm", func() error {
		// Every span: how busy each service is, and its operations — the
		// requests it serves and what it calls (mongodb.query, http.request…).
		buckets, err := client.AggregateSpans(scope, f, t, []string{"service", "operation_name"}, 100)
		if err != nil {
			return err
		}
		for _, b := range buckets {
			svc, op := b.By["service"], b.By["operation_name"]
			if svc == "" {
				continue
			}
			n, _ := spanCounts.LoadOrStore(svc, new(int))
			*n.(*int) += b.Count
			if s := matchScore(q, op); s >= findThreshold && op != "" {
				add(findMatch{Kind: "operation", Name: op, Service: svc, Score: s * 0.95, weight: b.Count,
					Detail: fmtCount(b.Count) + " spans",
					URL:    spansLink(fmt.Sprintf("service:%s operation_name:%s", svc, op), from, to),
					Next:   fmt.Sprintf("datadog investigate %s", svc)})
			}
		}
		return nil
	})
	run("apm endpoints", func() error {
		// Endpoints are the resources of the requests a service serves;
		// client spans' resources are its calls (hosts, queries).
		buckets, err := client.AggregateSpans(scope+" @span.kind:server", f, t, []string{"service", "resource_name"}, 100)
		if err != nil {
			return err
		}
		for _, b := range buckets {
			svc, res := b.By["service"], b.By["resource_name"]
			if svc == "" || res == "" {
				continue
			}
			if s := endpointScore(q, svc, res); s >= findThreshold {
				add(findMatch{Kind: "endpoint", Name: res, Service: svc, Score: s, weight: b.Count,
					Detail: fmtCount(b.Count) + " requests",
					URL:    spansLink(fmt.Sprintf("service:%s resource_name:%q", svc, res), from, to),
					Next:   fmt.Sprintf("datadog investigate %s --resource %q", svc, res)})
			}
		}
		return nil
	})
	run("logs", func() error {
		res, err := client.AggregateLogs(scope, f, t, []string{"service"}, 100)
		if err != nil {
			return err
		}
		for _, b := range res.Data.Buckets {
			if svc := b.By["service"]; svc != "" {
				n, _ := logCounts.LoadOrStore(svc, new(int))
				*n.(*int) += bucketCount(b)
			}
		}
		return nil
	})
	if len(q) > 0 {
		run("log mentions", func() error {
			text := strings.Join(q, " ")
			search := text
			if scope != "*" {
				search = scope + " " + text
			}
			res, err := client.AggregateLogs(search, f, t, []string{"service"}, 10)
			if err != nil {
				return err
			}
			for _, b := range res.Data.Buckets {
				n := bucketCount(b)
				if n == 0 {
					continue
				}
				svc := b.By["service"]
				search := text
				if svc != "" {
					search = "service:" + svc + " " + text
				}
				add(findMatch{Kind: "logs", Name: fmt.Sprintf("%s logs of %s mention %q", fmtCount(n), orDash(svc), text), Service: svc,
					Score: 0.55, weight: n, URL: logsLink(search, from, to),
					Next: fmt.Sprintf("datadog logs patterns %q --since %s", search, fmtDuration(to.Sub(from).Round(time.Minute)))})
			}
			return nil
		})
	}
	run("service catalog", func() error {
		entries, err := client.ListServices()
		if err != nil {
			return err
		}
		for _, e := range entries {
			sc := e.Attributes.Schema
			if sc.DDService == "" {
				continue
			}
			s := service(sc.DDService)
			score := max(matchScore(q, sc.DDService), matchScore(q, sc.Team)*0.8, matchScore(q, sc.Application)*0.8, matchScore(q, sc.Description)*0.7)
			svcMu.Lock()
			s.Score = max(s.Score, score)
			if sc.Team != "" {
				s.Detail = strings.TrimSpace(s.Detail + " team " + sc.Team)
			}
			svcMu.Unlock()
		}
		return nil
	})
	run("monitors", func() error {
		res, err := client.SearchMonitorsRich(strings.Join(q, " "), 50)
		if err != nil {
			return err
		}
		for _, m := range res.Monitors {
			s := matchScore(q, m.Name)
			for _, tag := range m.Tags {
				if v, ok := strings.CutPrefix(tag, "service:"); ok {
					s = max(s, matchScore(q, v)*0.9)
				}
			}
			add(findMatch{Kind: "monitor", Name: m.Name, ID: strconv.FormatInt(m.ID, 10), Score: s, Detail: m.Status,
				URL: monitorLink(m.ID, time.Time{}, time.Time{}), Next: fmt.Sprintf("datadog monitors explain %d", m.ID)})
		}
		return nil
	})
	run("dashboards", func() error {
		dashes, err := client.ListDashboards()
		if err != nil {
			return err
		}
		for _, d := range dashes {
			add(findMatch{Kind: "dashboard", Name: d.Title, ID: d.ID, Score: max(matchScore(q, d.Title), matchScore(q, d.Description)*0.7),
				URL: dashboardLink(d.ID, time.Time{}, time.Time{}), Next: "datadog dashboards read " + d.ID})
		}
		return nil
	})
	run("slos", func() error {
		slos, err := client.ListSLOs("")
		if err != nil {
			return err
		}
		for _, s := range slos {
			score := matchScore(q, s.Name)
			for _, tag := range s.Tags {
				if v, ok := strings.CutPrefix(tag, "service:"); ok {
					score = max(score, matchScore(q, v)*0.9)
				}
			}
			add(findMatch{Kind: "slo", Name: s.Name, ID: s.ID, Score: score, URL: sloLink(s.ID), Next: "datadog slos show " + s.ID})
		}
		return nil
	})
	run("incidents", func() error {
		incs, err := client.ListIncidents()
		if err != nil {
			return err
		}
		for _, inc := range incs {
			add(findMatch{Kind: "incident", Name: inc.Attributes.Title, ID: inc.ID, Score: matchScore(q, inc.Attributes.Title),
				Detail: inc.Attributes.Status + " · " + inc.Attributes.Created, URL: incidentLink(inc.ID), Next: "datadog incidents show " + inc.ID})
		}
		return nil
	})
	for _, w := range q {
		if len(w) < 3 {
			continue
		}
		w := w
		run("metrics "+w, func() error {
			names, err := client.SearchMetrics(w)
			if err != nil {
				return err
			}
			for i, m := range names {
				if i >= 25 {
					break
				}
				add(findMatch{Kind: "metric", Name: m, Score: matchScore(q, m) * 0.9,
					URL: metricLink("avg:"+m+"{*}", from, to), Next: fmt.Sprintf("datadog metrics describe \"avg:%s{*}\"", m)})
			}
			return nil
		})
		run("hosts "+w, func() error {
			hosts, err := client.ListHosts(w, 10)
			if err != nil {
				return err
			}
			for _, h := range hosts.HostList {
				add(findMatch{Kind: "host", Name: h.Name, ID: h.Name, Score: max(0.55, matchScore(q, h.Name)*0.9),
					URL: hostLink(h.Name), Next: "datadog read " + hostLink(h.Name)})
			}
			return nil
		})
	}
	wg.Wait()

	// Services: every one with spans or logs, scored by name.
	add2 := func(name string) {
		s := service(name)
		spans, logs := 0, 0
		if n, ok := spanCounts.Load(name); ok {
			spans = *n.(*int)
		}
		if n, ok := logCounts.Load(name); ok {
			logs = *n.(*int)
		}
		var parts []string
		if spans > 0 {
			parts = append(parts, fmtCount(spans)+" spans")
		}
		if logs > 0 {
			parts = append(parts, fmtCount(logs)+" logs")
		}
		if s.Detail != "" {
			parts = append(parts, s.Detail)
		}
		s.Detail, s.weight = strings.Join(parts, " · "), spans+logs
		add(*s)
	}
	seen := map[string]bool{}
	for _, m := range []*sync.Map{&spanCounts, &logCounts} {
		m.Range(func(k, _ any) bool {
			if name := k.(string); !seen[name] {
				seen[name] = true
				add2(name)
			}
			return true
		})
	}
	for name := range services {
		if !seen[name] {
			add2(name)
		}
	}

	order := map[string]int{"service": 0, "endpoint": 1, "operation": 2, "monitor": 3, "slo": 4, "dashboard": 5, "incident": 6, "logs": 7, "metric": 8, "host": 9}
	sort.SliceStable(rep.Matches, func(i, j int) bool {
		a, b := rep.Matches[i], rep.Matches[j]
		if a.Kind != b.Kind {
			return order[a.Kind] < order[b.Kind]
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		return a.Name < b.Name
	})
	rep.Best = bestMatch(rep.Matches)
	if len(rep.Failed) == 0 {
		rep.Failed = nil
	}
	if rep.Matches == nil {
		rep.Matches = []findMatch{}
	}
	return rep
}

// bestMatch is the match to start from: the best-scored service or
// endpoint (what investigate takes), else the best of anything; ties go to
// the busier one.
func bestMatch(ms []findMatch) *findMatch {
	pick := func(ok func(findMatch) bool) *findMatch {
		var best *findMatch
		for i := range ms {
			m := &ms[i]
			if !ok(*m) {
				continue
			}
			if best == nil || m.Score > best.Score+1e-9 || (math.Abs(m.Score-best.Score) <= 1e-9 && m.weight > best.weight) {
				best = m
			}
		}
		return best
	}
	best := pick(func(m findMatch) bool { return m.Kind == "service" || m.Kind == "endpoint" || m.Kind == "operation" })
	if best == nil {
		best = pick(func(findMatch) bool { return true })
	}
	if best == nil {
		return nil
	}
	b := *best
	return &b
}

// ─── output ──────────────────────────────────────────────────────

var findKindTitle = map[string]string{
	"service": "Services", "endpoint": "Endpoints", "operation": "Operations (what a service does or calls)", "monitor": "Monitors", "slo": "SLOs", "dashboard": "Dashboards",
	"incident": "Incidents", "logs": "Logs that mention it", "metric": "Metrics", "host": "Hosts",
}

func (r *findReport) text(tty bool) string {
	var b strings.Builder
	title := fmt.Sprintf("find · %q · %s", r.Query, fmtWindow(r.From, r.To))
	if tty {
		title = ui.Title.Render(title)
	}
	b.WriteString(title + "\n")
	if len(r.Matches) == 0 {
		b.WriteString("\n  Nothing matches. Try other words, a longer --since, or: datadog services\n")
	}
	if r.Best != nil {
		fmt.Fprintf(&b, "\n  Best match: %s %s → %s\n", r.Best.Kind, r.Best.label(), r.Best.Next)
	}
	kind := ""
	for _, m := range r.Matches {
		if m.Kind != kind {
			kind = m.Kind
			head := findKindTitle[kind]
			if tty {
				head = ui.SectionHeader.Render(head)
			}
			b.WriteString("\n  " + head + "\n")
		}
		line := "    " + m.label()
		if m.Detail != "" {
			line += "  · " + m.Detail
		}
		b.WriteString(line + "\n")
		if m.Next != "" {
			if tty {
				b.WriteString("      " + ui.Dimmed.Render(m.Next) + "\n")
			} else {
				b.WriteString("      " + m.Next + "\n")
			}
		}
	}
	for src, err := range r.Failed {
		fmt.Fprintf(&b, "\n  ! %s: %s", src, err)
	}
	if len(r.Failed) > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

func (m findMatch) label() string {
	switch m.Kind {
	case "endpoint", "operation":
		return m.Service + " · " + m.Name
	case "monitor", "slo", "incident", "dashboard":
		return m.Name + " (" + m.ID + ")"
	}
	return m.Name
}

func (r *findReport) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**find \"%s\"** (%s)\n\n", r.Query, fmtWindow(r.From, r.To))
	if r.Best != nil {
		fmt.Fprintf(&b, "Best match: %s [%s](%s) → `%s`\n\n", r.Best.Kind, r.Best.label(), r.Best.URL, r.Best.Next)
	}
	kind := ""
	for _, m := range r.Matches {
		if m.Kind != kind {
			kind = m.Kind
			fmt.Fprintf(&b, "\n%s\n\n", findKindTitle[kind])
		}
		name := m.label()
		if m.URL != "" {
			name = "[" + name + "](" + m.URL + ")"
		}
		line := "- " + name
		if m.Detail != "" {
			line += " · " + m.Detail
		}
		if m.Next != "" {
			line += " → `" + m.Next + "`"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
