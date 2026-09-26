// Package logpattern groups log messages into patterns: the message with its
// variable parts (ids, numbers, emails, times…) replaced by placeholders, so
// thousands of logs read as a handful of lines with counts.
package logpattern

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	reUUID  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reEmail = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
	reURL   = regexp.MustCompile(`\b(https?://[^\s/?#"']+)[^\s"']*`)
	reIP    = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	reTime  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	reHex   = regexp.MustCompile(`\b(?:0x)?[0-9a-fA-F]{12,}\b`)
	reNum   = regexp.MustCompile(`[-+]?\b\d+(?:[.,]\d+)*\b`)
	reDec   = regexp.MustCompile(`\b\d+(?:[.,]\d+)+`)
	reLead  = regexp.MustCompile(`^\d+`)
	reToken = regexp.MustCompile(`[A-Za-z0-9_-]*\d[A-Za-z0-9_-]*`)
	reQuote = regexp.MustCompile(`"[^"]{1,80}"|'[^']{1,80}'`)
	reSpace = regexp.MustCompile(`\s+`)
)

// Normalize replaces the variable parts of a message with placeholders:
// <uuid>, <email>, <url>, <ip>, <time>, <hex>, <id>, <num>. Only the first
// line counts (stack traces vary below it), up to 400 characters.
func Normalize(msg string) string {
	msg = strings.TrimSpace(msg)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if r := []rune(msg); len(r) > 400 {
		msg = string(r[:400])
	}
	msg = reUUID.ReplaceAllString(msg, "<uuid>")
	msg = reEmail.ReplaceAllString(msg, "<email>")
	msg = reURL.ReplaceAllString(msg, "$1/<path>")
	msg = reIP.ReplaceAllString(msg, "<ip>")
	msg = reTime.ReplaceAllString(msg, "<time>")
	msg = reHex.ReplaceAllStringFunc(msg, func(s string) string {
		if strings.IndexFunc(s, unicode.IsDigit) >= 0 {
			return "<hex>"
		}
		return s
	})
	msg = reDec.ReplaceAllString(msg, "<num>")
	// Tokens mixing letters and digits: ids (K7QX2M9ZPA, req-7f3a9b2c) when
	// long, numbers with a unit (250ms) otherwise; "v1", "utf8" stay.
	msg = reToken.ReplaceAllStringFunc(msg, func(tok string) string {
		digits, letters := 0, 0
		for _, r := range tok {
			switch {
			case unicode.IsDigit(r):
				digits++
			case unicode.IsLetter(r):
				letters++
			}
		}
		switch {
		case letters == 0:
			return reNum.ReplaceAllString(tok, "<num>")
		case len(tok) >= 8 && (digits >= 2 || strings.ToUpper(tok) == tok):
			return "<id>" // M1TJ2C2LZI, ABCDEFG7HJ, req-7f3a9b2c
		case unicode.IsDigit(rune(tok[0])):
			return reLead.ReplaceAllString(tok, "<num>")
		}
		return tok
	})
	msg = reNum.ReplaceAllString(msg, "<num>")
	msg = reQuote.ReplaceAllStringFunc(msg, func(s string) string {
		if strings.Count(s, " ") > 3 {
			return s // a quoted sentence is part of the message
		}
		return s[:1] + "<str>" + s[len(s)-1:]
	})
	return strings.TrimSpace(reSpace.ReplaceAllString(msg, " "))
}

// Group is one pattern and the messages that share it.
type Group struct {
	Key      string // what the caller groups by besides the text (service, status)
	Template string
	Count    int
	Example  string // one original message
	First    int64  // unix ms
	Last     int64
	tokens   []string
}

// Clusterer folds normalized messages into groups, merging templates that
// differ in a few words (names, codes the rules above can't know) into one
// with <*> where they differ.
type Clusterer struct {
	groups map[string][]*Group // by key + token count
	all    []*Group
}

func NewClusterer() *Clusterer { return &Clusterer{groups: map[string][]*Group{}} }

// Add files a message under key (e.g. "api|error") at time t.
func (c *Clusterer) Add(key, message string, t int64) *Group {
	tpl := Normalize(message)
	toks := strings.Fields(tpl)
	bucket := key + "\x00" + string(rune(len(toks)))
	for _, g := range c.groups[bucket] {
		if merged, ok := merge(g.tokens, toks); ok {
			g.tokens = merged
			g.Template = strings.Join(merged, " ")
			g.add(message, t)
			return g
		}
	}
	g := &Group{Key: key, Template: tpl, tokens: toks, First: t, Last: t}
	g.add(message, t)
	c.groups[bucket] = append(c.groups[bucket], g)
	c.all = append(c.all, g)
	return g
}

func (g *Group) add(message string, t int64) {
	g.Count++
	if g.Example == "" {
		g.Example = strings.TrimSpace(message)
	}
	if t < g.First {
		g.First = t
	}
	if t > g.Last {
		g.Last = t
	}
}

// Match finds the group a message belongs to without adding it.
func (c *Clusterer) Match(key, message string) *Group {
	toks := strings.Fields(Normalize(message))
	for _, g := range c.groups[key+"\x00"+string(rune(len(toks)))] {
		if _, ok := merge(g.tokens, toks); ok {
			return g
		}
	}
	return nil
}

// Groups returns the groups, biggest first.
func (c *Clusterer) Groups() []*Group {
	out := append([]*Group(nil), c.all...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// wildcard is <*> keeping what both words share around it: orderId=AB12,
// and orderId=CD34, → orderId=<*>,
func wildcard(a, b string) string {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	q := 0
	for q < len(a)-p && q < len(b)-p && a[len(a)-1-q] == b[len(b)-1-q] {
		q++
	}
	prefix, suffix := a[:p], a[len(a)-q:]
	// Cut back to punctuation: don't keep half a word.
	if i := strings.LastIndexFunc(prefix, isPunct); i < len(prefix)-1 {
		prefix = prefix[:i+1]
	}
	if i := strings.IndexFunc(suffix, isPunct); i > 0 {
		suffix = suffix[i:]
	} else if i < 0 {
		suffix = ""
	}
	if strings.HasSuffix(prefix, "<*>") || strings.HasPrefix(suffix, "<*>") {
		return prefix + suffix
	}
	return prefix + "<*>" + suffix
}

func isPunct(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '<' && r != '>' && r != '*'
}

// merge says whether two templates of the same length are the same pattern:
// short ones must match exactly; longer ones may differ in up to a quarter
// of their words, never in the first one (usually the event's name).
func merge(a, b []string) ([]string, bool) {
	if len(a) != len(b) {
		return nil, false
	}
	diff := 0
	out := make([]string, len(a))
	for i := range a {
		switch {
		case a[i] == b[i]:
			out[i] = a[i]
		case i == 0:
			return nil, false
		default:
			diff++
			out[i] = wildcard(a[i], b[i])
		}
	}
	if diff == 0 {
		return out, true
	}
	if len(a) < 4 || diff*4 > len(a) {
		return nil, false
	}
	return out, true
}
