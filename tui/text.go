package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// All width math goes through x/ansi: it skips escape sequences and counts
// wide runes as two cells. Never use len() on display strings — summaries
// are full of accents and a byte slice can split a rune in half.

func sw(s string) int { return ansi.StringWidth(s) }

// trunc shortens s to at most w cells, ending in an ellipsis when cut.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if sw(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, gEllipsis)
}

// truncLeft keeps the end of s, which is the telling part of a path or a
// long metric name.
func truncLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := sw(s); n > w {
		return gEllipsis + ansi.TruncateLeft(s, n-(w-1), "")
	}
	return s
}

// fit truncates or right-pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = trunc(s, w)
	if n := sw(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// fitRight truncates or left-pads s to exactly w cells (right-aligned).
func fitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = trunc(s, w)
	if n := sw(s); n < w {
		s = strings.Repeat(" ", w-n) + s
	}
	return s
}

// spread places left and right on one line of exactly w cells, truncating
// left first when they don't fit.
func spread(left, right string, w int) string {
	rw := sw(right)
	if rw >= w {
		return fit(right, w)
	}
	return fit(left, w-rw) + right
}

// frame normalizes a block to exactly h lines of exactly w cells, so views
// can be stacked and composited without drifting.
func frame(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	blank := strings.Repeat(" ", max(0, w))
	for len(lines) < h {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

// wrapPlain word-wraps unstyled text to w cells. Words longer than the line
// are hard-broken.
func wrapPlain(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			for sw(word) > w {
				// Hard-break an overlong word, filling the current line first.
				room := w - sw(line)
				if line != "" {
					room--
				}
				if room <= 0 {
					out = append(out, line)
					line = ""
					continue
				}
				head := ansi.Truncate(word, room, "")
				if head == "" {
					if line != "" {
						out = append(out, line)
						line = ""
						continue
					}
					// Not even one (wide) rune fits: emit it anyway.
					head = string([]rune(word)[:1])
				}
				if line != "" {
					line += " "
				}
				out = append(out, line+head)
				line = ""
				word = strings.TrimPrefix(word, head)
			}
			if word == "" {
				continue
			}
			switch {
			case line == "":
				line = word
			case sw(line)+1+sw(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// ─── backgrounds ─────────────────────────────────────────────────

// bgSeq returns the SGR sequence that sets c as background in the current
// color profile ("" when colors are off).
func bgSeq(c lipgloss.TerminalColor) string {
	col, ok := c.(lipgloss.Color)
	if !ok {
		return ""
	}
	tc := lipgloss.ColorProfile().Color(string(col))
	if tc == nil {
		return ""
	}
	seq := tc.Sequence(true)
	if seq == "" {
		return ""
	}
	return "\x1b[" + seq + "m"
}

// paintBg applies a background to an already styled line. Inner segments end
// with a full reset, which would punch holes in a plain lipgloss background;
// re-arming the background after every reset keeps it continuous.
func paintBg(s string, c lipgloss.TerminalColor) string {
	seq := bgSeq(c)
	if seq == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+seq)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+seq)
	return seq + s + "\x1b[0m"
}

// ─── time ────────────────────────────────────────────────────────

var now = time.Now

// age is a compact relative time for dense columns: 5m, 3h, 2d, 3w, Aug 10.
func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 8*7*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	case t.Year() == now().Year():
		return t.Format("Jan 02")
	}
	return t.Format("2006")
}

// ago is the long form: "5m ago", "3d ago", "Aug 10".
func ago(t time.Time) string {
	a := age(t)
	switch {
	case a == "":
		return ""
	case a == "now":
		return "just now"
	case strings.HasSuffix(a, "m"), strings.HasSuffix(a, "h"), strings.HasSuffix(a, "d"), strings.HasSuffix(a, "w"):
		return a + " ago"
	}
	return a
}

// longDate renders an absolute date with its relative age: "Aug 10, 2026 · 3w ago".
func longDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2, 2006 15:04") + " " + gDot + " " + ago(t)
}

// ─── names ───────────────────────────────────────────────────────

func firstName(full string) string {
	if parts := strings.Fields(full); len(parts) > 0 {
		return parts[0]
	}
	return full
}

// shortName is "First L." — distinguishes people sharing a first name
// without spending a whole column on surnames.
func shortName(full string) string {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	last := []rune(parts[1])
	return parts[0] + " " + string(last[0]) + "."
}

// ─── search ──────────────────────────────────────────────────────

// fold lowercases and strips the diacritics common in Spanish/Latin text,
// so "migracion" finds "migración".
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'á', 'à', 'ä', 'â', 'ã':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô', 'õ':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		case 'ñ':
			r = 'n'
		case 'ç':
			r = 'c'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// highlight styles every occurrence of the query terms inside s (plain
// text). Matching is accent-insensitive; the original characters are kept.
func highlight(s string, terms []string, base, hl lipgloss.Style) string {
	if len(terms) == 0 || s == "" {
		return base.Render(s)
	}
	rs := []rune(s)
	folded := []rune(fold(s))
	if len(folded) != len(rs) {
		return base.Render(s)
	}
	mark := make([]bool, len(rs))
	for _, t := range terms {
		tr := []rune(t)
		if len(tr) == 0 {
			continue
		}
		for i := 0; i+len(tr) <= len(folded); i++ {
			if string(folded[i:i+len(tr)]) == t {
				for j := i; j < i+len(tr); j++ {
					mark[j] = true
				}
			}
		}
	}
	var b strings.Builder
	start := 0
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || mark[i] != mark[start] {
			seg := string(rs[start:i])
			if mark[start] {
				b.WriteString(hl.Render(seg))
			} else {
				b.WriteString(base.Render(seg))
			}
			start = i
		}
	}
	return b.String()
}

// queryTerms splits a filter query into folded terms.
func queryTerms(q string) []string {
	var out []string
	for _, f := range strings.Fields(fold(q)) {
		out = append(out, f)
	}
	return out
}

// isPrintable reports whether a key string is a single printable rune.
func isPrintable(s string) bool {
	r := []rune(s)
	return len(r) == 1 && unicode.IsPrint(r[0])
}
