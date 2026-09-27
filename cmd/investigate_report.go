package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
)

// An investigation reads top-down: the answer first (summary), then when
// (timeline), why (leads), the evidence (findings), what was ruled out,
// what couldn't be checked, what to improve, what to run next — and every
// claim's reference, a link to the exact view in Datadog.

var findingAreas = []struct{ key, title string }{
	{"errors", "Errors"}, {"latency", "Latency"}, {"traffic", "Traffic"}, {"endpoints", "Endpoints"},
	{"dependencies", "Dependencies"}, {"logs", "Logs"}, {"changes", "Changes"}, {"alerting", "Alerting"},
}

func (inv *investigation) title() string {
	return fmt.Sprintf("Investigation · %s · %s", strings.Join(inv.Scope, ", "), fmtWindow(inv.From, inv.To))
}

func (inv *investigation) subtitle() string {
	s := fmt.Sprintf("status: %s", inv.Status)
	if inv.Onset != nil {
		s += " · started " + inv.at(*inv.Onset)
	}
	return s + fmt.Sprintf(" · compared with %s before (%s)", inv.Compare, fmtWindow(inv.BaseFrom, inv.BaseTo))
}

func (inv *investigation) timelineClock(t time.Time) string {
	if inv.To.Sub(inv.From) > 20*time.Hour || t.Before(inv.From) && inv.From.YearDay() != t.YearDay() {
		return t.Local().Format("Jan 2 15:04")
	}
	return t.Local().Format("15:04")
}

// ─── markdown ────────────────────────────────────────────────────

func (inv *investigation) markdown() string {
	var b strings.Builder
	refs := inv.References
	cite := func(ns []int) string { return citeMD(refs, ns...) }

	b.WriteString("# " + inv.title() + "\n\n")
	if inv.Question != "" {
		b.WriteString("> " + inv.Question + "\n\n")
	}
	b.WriteString("_" + capitalize(inv.subtitle()) + "._\n\n")
	for _, n := range inv.Notes {
		b.WriteString("_" + capitalize(n) + "._\n\n")
	}

	b.WriteString("## Summary\n\n" + inv.Summary + "\n\n")

	if len(inv.Timeline) > 0 {
		fmt.Fprintf(&b, "## Timeline (%s)\n\n| Time | | What |\n|---|---|---|\n", zoneName())
		for _, e := range inv.Timeline {
			what := mdCell(e.What)
			if e.Kind == "onset" {
				what = "**" + what + "**"
			}
			fmt.Fprintf(&b, "| %s | %s | %s%s |\n", inv.timelineClock(e.At), e.Kind, what, cite(e.Refs))
		}
		b.WriteString("\n")
	}

	if len(inv.Leads) > 0 {
		b.WriteString("## Leads\n\n")
		for i, l := range inv.Leads {
			fmt.Fprintf(&b, "%d. **%s** — %s confidence%s\n", i+1, l.Title, l.Confidence, cite(l.Refs))
			for _, e := range l.For {
				b.WriteString("   - For: " + e + "\n")
			}
			for _, e := range l.Against {
				b.WriteString("   - Against: " + e + "\n")
			}
			if l.Verify != "" {
				b.WriteString("   - Check: `" + l.Verify + "`\n")
			}
		}
		b.WriteString("\n")
	}

	if len(inv.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, area := range findingAreas {
			var lines []string
			for _, fi := range inv.Findings {
				if fi.Area == area.key {
					lines = append(lines, fmt.Sprintf("- **%s** · %s%s", fi.Severity, fi.Text, cite(fi.Refs)))
				}
			}
			if len(lines) > 0 {
				b.WriteString("### " + area.title + "\n\n" + strings.Join(lines, "\n") + "\n\n")
			}
		}
	}

	if len(inv.Normal) > 0 {
		b.WriteString("## Checked, nothing unusual\n\n")
		for _, n := range inv.Normal {
			b.WriteString("- " + n.Text + cite(n.Refs) + "\n")
		}
		b.WriteString("\n")
	}
	if len(inv.Gaps) > 0 {
		b.WriteString("## Couldn't check\n\n")
		for _, g := range inv.Gaps {
			b.WriteString("- " + g + "\n")
		}
		b.WriteString("\n")
	}
	if len(inv.Suggestions) > 0 {
		b.WriteString("## Suggested improvements\n\n")
		for _, s := range inv.Suggestions {
			b.WriteString("- **" + s.What + "** — " + s.Why + cite(s.Refs) + "\n")
			if s.Command != "" {
				b.WriteString("  ```sh\n  " + s.Command + "\n  ```\n")
			}
		}
		b.WriteString("\n")
	}
	if len(inv.Next) > 0 {
		b.WriteString("## Next steps\n\n")
		for _, n := range inv.Next {
			cmd, why, _ := strings.Cut(n, "   # ")
			line := "- `" + cmd + "`"
			if why != "" {
				line += " — " + why
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if len(refs) > 0 {
		b.WriteString("## References\n\n")
		for _, r := range refs {
			fmt.Fprintf(&b, "%d. [%s](%s)", r.N, mdLinkText(r.Title), r.URL)
			if r.Query != "" {
				b.WriteString(" — `" + strings.ReplaceAll(r.Query, "`", "'") + "`")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// mdLinkText escapes the brackets that would end a link's text early.
func mdLinkText(s string) string { return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s) }

// ─── terminal ────────────────────────────────────────────────────

func (inv *investigation) text(tty bool) string {
	var b strings.Builder
	width := termWidth(110)
	style := func(render func(...string) string, s string) string {
		if tty {
			return render(s)
		}
		return s
	}
	head := func(s string) { b.WriteString("\n " + style(ui.SectionHeader.Render, s) + "\n") }
	dim := func(s string) string { return style(ui.Dimmed.Render, s) }

	b.WriteString(style(ui.Title.Render, " "+inv.title()) + "\n")
	b.WriteString(" " + dim(inv.subtitle()) + "\n")
	if inv.Question != "" {
		b.WriteString(" " + dim("“"+inv.Question+"”") + "\n")
	}
	for _, n := range inv.Notes {
		b.WriteString(" " + dim(n) + "\n")
	}

	head("Summary")
	b.WriteString(wrapIndent(inv.Summary, width, "   ") + "\n")

	if len(inv.Timeline) > 0 {
		head("Timeline (" + zoneName() + ")")
		for _, e := range inv.Timeline {
			what := e.What + cite(e.Refs...)
			if e.Kind == "onset" && tty {
				what = ui.ErrorStyle.Render(what)
			}
			clock := inv.timelineClock(e.At)
			prefix := "   " + clock + "  " + dim(fmt.Sprintf("%-8s", e.Kind)) + " "
			b.WriteString(wrapIndent(what, width, prefix, strings.Repeat(" ", 3+len(clock)+2+8+1)) + "\n")
		}
	}
	if len(inv.Leads) > 0 {
		head("Leads")
		for i, l := range inv.Leads {
			b.WriteString(wrapIndent(fmt.Sprintf("%d. [%s] %s%s", i+1, l.Confidence, l.Title, cite(l.Refs...)), width, "   ", "      ") + "\n")
			for _, e := range l.For {
				b.WriteString(wrapIndent("+ "+e, width, "      ", "        ") + "\n")
			}
			for _, e := range l.Against {
				b.WriteString(wrapIndent("− "+e, width, "      ", "        ") + "\n")
			}
			if l.Verify != "" {
				b.WriteString("      " + dim("→ "+l.Verify) + "\n")
			}
		}
	}
	if len(inv.Findings) > 0 {
		head("Findings")
		for _, area := range findingAreas {
			for _, fi := range inv.Findings {
				if fi.Area != area.key {
					continue
				}
				mark := map[string]string{"high": "●", "medium": "◐", "low": "○", "info": "·"}[fi.Severity]
				if tty {
					switch fi.Severity {
					case "high":
						mark = ui.ErrorStyle.Render(mark)
					case "medium":
						mark = lipgloss.NewStyle().Foreground(ui.Warning).Render(mark)
					}
				}
				b.WriteString(wrapIndent(fi.Text+cite(fi.Refs...), width, "   "+mark+" "+dim(fmt.Sprintf("%-12s", area.key))+" ", strings.Repeat(" ", 3+1+1+12+1)) + "\n")
			}
		}
	}
	if len(inv.Normal) > 0 {
		head("Checked, nothing unusual")
		for _, n := range inv.Normal {
			b.WriteString(wrapIndent(style(ui.SuccessStyle.Render, "✓")+" "+n.Text+cite(n.Refs...), width, "   ", "     ") + "\n")
		}
	}
	if len(inv.Gaps) > 0 {
		head("Couldn't check")
		for _, g := range inv.Gaps {
			b.WriteString(wrapIndent("! "+g, width, "   ", "     ") + "\n")
		}
	}
	if len(inv.Suggestions) > 0 {
		head("Suggested improvements")
		for _, s := range inv.Suggestions {
			b.WriteString(wrapIndent("→ "+s.What+" — "+s.Why, width, "   ", "     ") + "\n")
			if s.Command != "" {
				b.WriteString("     " + dim(s.Command) + "\n")
			}
		}
	}
	if len(inv.Next) > 0 {
		head("Next steps")
		for _, n := range inv.Next {
			b.WriteString("   " + n + "\n")
		}
	}
	if len(inv.References) > 0 {
		head("References")
		for _, r := range inv.References {
			fmt.Fprintf(&b, "   [%d] %s\n       %s\n", r.N, r.Title, dim(r.URL))
		}
	}
	b.WriteString("\n " + dim("Markdown for a ticket: --md · everything for an agent: --json") + "\n")
	return b.String()
}

// wrapIndent wraps text at width, the first line after first and the rest
// after rest (first again when rest isn't given). Styled text is measured
// by its visible length.
func wrapIndent(s string, width int, first string, rest ...string) string {
	next := first
	if len(rest) > 0 {
		next = rest[0]
	}
	var lines []string
	line, indent := first, first
	lineLen := visibleLen(first)
	for _, w := range strings.Fields(s) {
		wl := visibleLen(w)
		if lineLen > visibleLen(indent) && lineLen+1+wl > width {
			lines = append(lines, line)
			line, indent, lineLen = next+w, next, visibleLen(next)+wl
			continue
		}
		if lineLen > visibleLen(indent) {
			line += " "
			lineLen++
		}
		line += w
		lineLen += wl
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

// visibleLen is a string's length without its ANSI styling.
func visibleLen(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			n++
		}
	}
	return n
}
