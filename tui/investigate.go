package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

// Investigations hand an alert (or a log line) to Claude Code: a new
// iTerm2 tab opens in the background — in the service's repo when there's
// one, so Claude can read the code — with a first message saying what's
// wrong and how to dig with this CLI. Other terminals get the command on
// the clipboard.

type investigation struct {
	tab                string // tab title
	service            string // to find the repo
	promptES, promptEN string
}

type investigatedMsg struct {
	copied string
	dir    string
	err    error
}

// writeClipboard is swappable in tests.
var writeClipboard = clipboard.WriteAll

func spanish() bool {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return strings.HasPrefix(strings.ToLower(v), "es")
		}
	}
	return false
}

func (inv investigation) prompt() string {
	if spanish() {
		return inv.promptES
	}
	return inv.promptEN
}

func cmdInvestigate(ctx *appCtx, inv investigation) tea.Cmd {
	return func() tea.Msg {
		if ctx.demo {
			return toastMsg{text: "With your own Datadog, C hands this to Claude Code", kind: toastInfo}
		}
		if _, err := lookPath("claude"); err != nil {
			return toastMsg{text: "Claude Code isn't installed — https://claude.com/claude-code", kind: toastErr}
		}
		dir := repoFor(ctx.repos, inv.service)
		file, err := writePrompt(inv.tab, inv.prompt())
		if err != nil {
			return investigatedMsg{err: err}
		}
		command := "cd " + shellQuote(dir) + " && claude \"$(cat " + shellQuote(file) + ")\""
		if !inITerm() {
			if err := writeClipboard(command); err != nil {
				return investigatedMsg{err: err}
			}
			return investigatedMsg{copied: command, dir: dir}
		}
		if _, err := openInITerm(command, inv.tab); err != nil {
			return investigatedMsg{err: err}
		}
		return investigatedMsg{dir: dir}
	}
}

// investigatedToast reports how the launch went.
func investigatedToast(m investigatedMsg) tea.Cmd {
	switch {
	case m.err != nil:
		return toast("Couldn't start Claude: "+m.err.Error(), toastErr)
	case m.copied != "":
		return toast("Command copied — paste it in a new terminal tab", toastInfo)
	}
	return toast("Claude is investigating in a new tab ("+tildify(m.dir)+")", toastOK)
}

// repoFor is the checkout named like the service inside the repos folder,
// else the home folder.
func repoFor(root, service string) string {
	home, _ := os.UserHomeDir()
	if root == "" {
		root = guessReposRoot()
	}
	if root != "" && service != "" {
		for _, name := range []string{service, strings.TrimSuffix(service, "-service")} {
			p := filepath.Join(root, name)
			if fi, err := os.Stat(filepath.Join(p, ".git")); err == nil && fi.IsDir() {
				return p
			}
		}
	}
	return home
}

// guessReposRoot finds where the user keeps checkouts: $DATADOG_REPOS, or
// the first usual folder holding at least two git repos.
func guessReposRoot() string {
	if env := os.Getenv("DATADOG_REPOS"); env != "" {
		return expandHome(env)
	}
	home, _ := os.UserHomeDir()
	candidates, _ := filepath.Glob(filepath.Join(home, "*", "github"))
	for _, d := range []string{"repos", "code", "src", "dev", "projects", "git", "work", "workspace", "github"} {
		candidates = append(candidates, filepath.Join(home, d))
	}
	for _, d := range candidates {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		n := 0
		for _, e := range entries {
			if fi, err := os.Stat(filepath.Join(d, e.Name(), ".git")); err == nil && fi.IsDir() {
				n++
			}
		}
		if n >= 2 {
			return d
		}
	}
	return ""
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func tildify(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	if p == home {
		return "~"
	}
	return p
}

func writePrompt(name, text string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "datadog-cli", "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, name)
	f, err := os.CreateTemp(dir, safe+"-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return f.Name(), err
}

// ─── prompts ─────────────────────────────────────────────────────

func monitorInvestigation(site string, m monitorFacts) investigation {
	var es, en strings.Builder
	fmt.Fprintf(&es, "Investiga el monitor de Datadog «%s» (id %d), ahora en estado %s (sitio %s).\n\n", m.name, m.id, m.state, site)
	fmt.Fprintf(&en, "Investigate the Datadog monitor «%s» (id %d), currently %s (site %s).\n\n", m.name, m.id, m.state, site)
	if m.query != "" {
		fmt.Fprintf(&es, "- Consulta: `%s`\n", m.query)
		fmt.Fprintf(&en, "- Query: `%s`\n", m.query)
	}
	if m.thresholds != "" {
		fmt.Fprintf(&es, "- Umbrales: %s\n", m.thresholds)
		fmt.Fprintf(&en, "- Thresholds: %s\n", m.thresholds)
	}
	if len(m.groups) > 0 {
		fmt.Fprintf(&es, "- Grupos que disparan: %s\n", strings.Join(m.groups, ", "))
		fmt.Fprintf(&en, "- Triggering groups: %s\n", strings.Join(m.groups, ", "))
	}
	svc := ""
	if m.service != "" {
		svc = fmt.Sprintf(", y `datadog services context %s --since 2h --json`", m.service)
	}
	fmt.Fprintf(&es, "\nUsa la CLI `datadog` (ya está configurada): empieza por `datadog monitors show %d --json` y `datadog triage --since 2h --json`%s. "+
		"Logs con `datadog logs \"<consulta>\" --since 1h --json`; cambios recientes con `datadog audit --since 24h --json` y los eventos de despliegue. "+
		"Si estás en el repo del servicio, cruza lo que veas con el código.\n\n"+
		"Dime la causa más probable, las pruebas que la sostienen y qué hacer ahora. "+
		"No cambies nada en Datadog (silenciar, editar, borrar) sin preguntarme antes.\n", m.id, svc)
	fmt.Fprintf(&en, "\nUse the `datadog` CLI (it's configured): start with `datadog monitors show %d --json` and `datadog triage --since 2h --json`%s. "+
		"Logs with `datadog logs \"<query>\" --since 1h --json`; recent changes with `datadog audit --since 24h --json` and deploy events. "+
		"If you're in the service's repo, check what you find against the code.\n\n"+
		"Tell me the most likely cause, the evidence for it, and what to do next. "+
		"Don't change anything in Datadog (mute, edit, delete) without asking me first.\n", m.id, svc)
	return investigation{tab: fmt.Sprintf("DD %d · Claude", m.id), service: m.service, promptES: es.String(), promptEN: en.String()}
}

type monitorFacts struct {
	id                                      int64
	name, state, query, thresholds, service string
	groups                                  []string
}

func logInvestigation(site, service, status, when, message string, attrs []string) investigation {
	short := strings.Join(strings.Fields(message), " ")
	if len([]rune(short)) > 600 {
		short = string([]rune(short)[:600]) + "…"
	}
	var es, en strings.Builder
	fmt.Fprintf(&es, "Investiga este log (%s) del servicio %s, de las %s (Datadog, sitio %s):\n\n> %s\n\n", status, service, when, site, short)
	fmt.Fprintf(&en, "Investigate this %s log from service %s at %s (Datadog, site %s):\n\n> %s\n\n", status, service, when, site, short)
	if len(attrs) > 0 {
		es.WriteString("Atributos: " + strings.Join(attrs, ", ") + "\n\n")
		en.WriteString("Attributes: " + strings.Join(attrs, ", ") + "\n\n")
	}
	fmt.Fprintf(&es, "Con la CLI `datadog`: `datadog logs \"service:%s status:error\" --since 2h --json` para ver si se repite, "+
		"`datadog services context %s --since 2h --json` para el contexto del servicio y `datadog triage --since 1h --json`. "+
		"Si estás en su repo, busca en el código dónde nace el error. Dime causa probable, pruebas y siguiente paso; "+
		"no cambies nada en Datadog sin preguntarme.\n", service, service)
	fmt.Fprintf(&en, "With the `datadog` CLI: `datadog logs \"service:%s status:error\" --since 2h --json` to see if it repeats, "+
		"`datadog services context %s --since 2h --json` for the service's context and `datadog triage --since 1h --json`. "+
		"If you're in its repo, find where the error comes from in the code. Tell me the likely cause, the evidence and the next step; "+
		"don't change anything in Datadog without asking me.\n", service, service)
	return investigation{tab: "log · " + service + " · Claude", service: service, promptES: es.String(), promptEN: en.String()}
}
