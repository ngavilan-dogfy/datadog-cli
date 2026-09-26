package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/selfupdate"
	"github.com/ngavilan-dogfy/datadog-cli/internal/uiprefs"

	"github.com/spf13/cobra"
)

// check is one line of 'datadog doctor': what was checked, how it went and,
// when something's off, how to fix it.
type check struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warn, fail, info
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

func printCheck(c check) {
	switch c.Status {
	case "ok":
		sayOK(c.Detail)
		printFix(c.Fix)
	case "warn":
		sayWarn(c.Detail, c.Fix)
	case "fail":
		sayFail(c.Detail, c.Fix)
	default:
		sayInfo(c.Detail)
		printFix(c.Fix)
	}
}

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that everything works, with a fix for each problem",
	Long: `Check this install from top to bottom and explain how to fix anything
that's off: the binary and your PATH, the Datadog site, both keys, what the
keys can read (monitors, dashboards, logs, metrics…), and the optional
pieces datadog ui uses (chart style, Claude Code, iTerm2).

Output:
  TTY → a checklist with fixes · --json → {"ok": bool, "checks": [...]}
  Exit code 1 when a check fails (warnings don't count).

Examples:
  datadog doctor
  datadog doctor --json | jq '.checks[] | select(.status != "ok")'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDoctor(cfg, doctorJSON)
	},
}

var errChecksFailed = errors.New("some checks failed")

func runDoctor(p *config.Profile, jsonOut bool) error {
	var all []check
	section := ""
	emit := func(c check) {
		all = append(all, c)
		if jsonOut {
			return
		}
		if c.Section != section {
			section = c.Section
			fmt.Println()
			fmt.Println("  " + wzBold.Render(section))
		}
		printCheck(c)
	}
	spin := func(label string, fn func()) {
		if jsonOut {
			fn()
			return
		}
		_ = withSpinner(label, func() error { fn(); return nil })
	}

	if !jsonOut {
		fmt.Println()
		fmt.Println(wzAccent.Render("  datadog doctor"))
	}
	for _, c := range installChecks() {
		emit(c)
	}
	var upd check
	hasUpd := false
	spin("Looking for updates", func() { upd, hasUpd = updateCheckLine() })
	if hasUpd {
		emit(upd)
	}
	for _, c := range datadogChecks(p, spin) {
		emit(c)
	}
	for _, c := range extraChecks() {
		emit(c)
	}

	failed, warned := 0, 0
	for _, c := range all {
		switch c.Status {
		case "fail":
			failed++
		case "warn":
			warned++
		}
	}
	if jsonOut {
		if err := printJSON(map[string]any{"ok": failed == 0, "checks": all}); err != nil {
			return err
		}
	} else {
		fmt.Println()
		switch {
		case failed > 0:
			fmt.Println("  " + wzFail.Render(plural(failed, "problem")+" to fix") + wzMuted.Render(" — see the hints above each one"))
		case warned > 0:
			fmt.Println("  " + wzOK.Render("datadog works.") + wzMuted.Render(" "+plural(warned, "warning")+" above, worth a look."))
		default:
			fmt.Println("  " + wzOK.Render("Everything works."))
		}
		fmt.Println()
	}
	if failed > 0 {
		return quietError{errChecksFailed} // the checklist already explains it
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ─── this install ────────────────────────────────────────────────

func installChecks() []check {
	const sec = "This install"
	b := currentBuild()
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	out := []check{{Section: sec, Name: "version", Status: "ok",
		Detail: fmt.Sprintf("datadog %s · %s · %s", b.Short(), b.Platform, tildePath(exe))}}
	onPath, err := lookTool("datadog")
	if err != nil {
		dir := filepath.Dir(exe)
		return append(out, check{Section: sec, Name: "path", Status: "warn",
			Detail: "Typing 'datadog' doesn't find this binary: " + tildePath(dir) + " isn't in your PATH",
			Fix:    pathFix(dir)})
	}
	if r, err := filepath.EvalSymlinks(onPath); err == nil {
		onPath = r
	}
	if onPath != exe {
		out = append(out, check{Section: sec, Name: "path", Status: "warn",
			Detail: "Typing 'datadog' runs a different copy: " + tildePath(onPath),
			Fix: "An older copy of this CLI? Reinstall over it. Another tool? Put " +
				tildePath(filepath.Dir(exe)) + " before it in your PATH."})
	}
	return out
}

func pathFix(dir string) string {
	shell := filepath.Base(os.Getenv("SHELL"))
	home, _ := os.UserHomeDir()
	shown := dir
	if strings.HasPrefix(dir, home+"/") {
		shown = "$HOME" + dir[len(home):]
	}
	switch shell {
	case "fish":
		return "Run: fish_add_path " + shown
	case "zsh":
		return "Run: echo 'export PATH=\"" + shown + ":$PATH\"' >> ~/.zshrc  and open a new terminal"
	case "bash":
		rc := "~/.bashrc"
		if runtime.GOOS == "darwin" {
			rc = "~/.bash_profile"
		}
		return "Run: echo 'export PATH=\"" + shown + ":$PATH\"' >> " + rc + "  and open a new terminal"
	}
	return "Add " + shown + " to your PATH in your shell's config file."
}

func updateCheckLine() (check, bool) {
	t := updateTool()
	if !selfupdate.IsRelease(t.Current) {
		return check{}, false
	}
	rel, err := selfupdate.Latest(t)
	if err != nil {
		return check{}, false
	}
	if selfupdate.Newer(rel.Tag, t.Current) {
		return check{Section: "This install", Name: "update", Status: "info",
			Detail: rel.Tag + " is out (you have " + t.Current + ")", Fix: "Run: datadog update"}, true
	}
	return check{Section: "This install", Name: "update", Status: "ok", Detail: "Up to date"}, true
}

// ─── datadog ─────────────────────────────────────────────────────

func datadogChecks(p *config.Profile, spin func(string, func())) []check {
	const sec = "Datadog"
	if p == nil {
		return []check{{Section: sec, Name: "profile", Status: "fail", Detail: "Not connected to Datadog yet", Fix: "Run: datadog setup"}}
	}
	var out []check
	if p.Name == "env" {
		out = append(out, check{Section: sec, Name: "profile", Status: "ok",
			Detail: "Using DD_API_KEY, DD_APP_KEY and DD_SITE from the environment"})
	} else {
		path := filepath.Join(config.ProfileDir(), p.Name+".yaml")
		c := check{Section: sec, Name: "profile", Status: "ok", Detail: fmt.Sprintf("Profile %q · %s", p.Name, tildePath(path))}
		if fi, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
			c.Status = "warn"
			c.Detail += " — other users on this machine can read it"
			c.Fix = "It holds your keys. Run: chmod 600 " + tildePath(path)
		}
		out = append(out, c)
	}
	if !p.IsAuthenticated() {
		return append(out, check{Section: sec, Name: "keys", Status: "fail", Detail: "No keys configured", Fix: "Run: datadog setup"})
	}
	site := p.Site
	if site == "" {
		site = "datadoghq.com"
	}
	var err error
	spin("Reaching "+site, func() { err = siteProbe(site) })
	if err != nil {
		return append(out, check{Section: sec, Name: "site", Status: "fail", Detail: err.Error(), Fix: "Check your connection or VPN."})
	}
	out = append(out, check{Section: sec, Name: "site", Status: "ok", Detail: site + " (" + config.SiteLabel(site) + ") is reachable"})

	var valid bool
	spin("Checking the API key", func() { valid, err = validateAPIKey(site, p.APIKey) })
	switch {
	case err != nil:
		return append(out, check{Section: sec, Name: "api_key", Status: "fail", Detail: "Couldn't check the API key: " + rootCause(err)})
	case !valid:
		fix := "Create a new one and run: datadog setup"
		var other string
		spin("Trying the other sites", func() { other = siteForKey(p.APIKey, site) })
		if other != "" {
			fix = "It belongs to " + other + " (" + config.SiteLabel(other) + "). Run: datadog config set site " + other
		}
		return append(out, check{Section: sec, Name: "api_key", Status: "fail", Detail: "The API key " + config.Mask(p.APIKey) + " isn't valid on " + site, Fix: fix})
	}
	out = append(out, check{Section: sec, Name: "api_key", Status: "ok", Detail: "API key " + config.Mask(p.APIKey) + " is valid"})

	var id identity
	spin("Checking the application key", func() { id, err = whoAmI(p) })
	if err != nil {
		return append(out, check{Section: sec, Name: "app_key", Status: "fail",
			Detail: "The application key " + config.Mask(p.AppKey) + " doesn't work: " + rootCause(err),
			Fix:    "Create one under Personal Settings → Application Keys and run: datadog setup"})
	}
	who := id.name
	if id.org != "" {
		who += " · " + id.org
	}
	out = append(out, check{Section: sec, Name: "app_key", Status: "ok", Detail: "Signed in as " + who})

	var access []check
	spin("Checking what the keys can read", func() { access = accessChecks(buildClient(p)) })
	return append(out, access...)
}

// accessChecks tries one cheap read per area, in parallel.
func accessChecks(c *datadog.Client) []check {
	now := time.Now()
	from := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	to := strconv.FormatInt(now.Unix(), 10)
	areas := []struct {
		name, scope string
		run         func() error
	}{
		{"monitors", "monitors_read", func() error { return c.Probe("/api/v1/monitor?page=0&page_size=1") }},
		{"dashboards", "dashboards_read", func() error { return c.Probe("/api/v1/dashboard?count=1") }},
		{"metrics", "timeseries_query", func() error {
			return c.Probe("/api/v1/query?query=" + url.QueryEscape("avg:datadog.agent.running{*}") + "&from=" + from + "&to=" + to)
		}},
		{"logs", "logs_read_data", func() error {
			_, err := c.SearchLogs("*", "now-15m", "now", 1)
			return err
		}},
		{"incidents", "incident_read", func() error { return c.Probe("/api/v2/incidents?page[size]=1") }},
		{"SLOs", "slos_read", func() error { return c.Probe("/api/v1/slo?limit=1") }},
	}
	out := make([]check, len(areas))
	var wg sync.WaitGroup
	for i := range areas {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := areas[i]
			ch := check{Section: "Datadog", Name: a.name, Status: "ok", Detail: "Can read " + a.name}
			if err := a.run(); err != nil {
				ch.Status = "warn"
				ch.Detail = "Can't read " + a.name + ": " + rootCause(err)
				ch.Fix = "The application key may be scoped: add " + a.scope + ", or create one without scopes."
			}
			out[i] = ch
		}(i)
	}
	wg.Wait()
	return out
}

// ─── extras ──────────────────────────────────────────────────────

const extrasSection = "Extras for datadog ui (optional)"

// probeTool runs a command with a timeout; lookTool finds one. Both are
// swapped in tests (no real claude).
var (
	probeTool = func(timeout time.Duration, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	lookTool = exec.LookPath
)

func extraChecks() []check {
	var out []check
	add := func(name, status, detail, fix string) {
		out = append(out, check{Section: extrasSection, Name: name, Status: status, Detail: detail, Fix: fix})
	}
	style := uiprefs.ChartStyle()
	fix := "Change it in datadog ui with B, or run: datadog setup"
	if style == "blocks" {
		fix = "Braille charts are finer if your font has braille glyphs. " + fix
	}
	add("charts", "ok", "Charts drawn with "+style, fix)

	if _, err := lookTool("claude"); err != nil {
		add("claude", "info", "Claude Code isn't installed — needed to investigate alerts with C",
			"Install it from https://claude.com/claude-code")
	} else {
		v, _ := probeTool(10*time.Second, "claude", "--version")
		if i := strings.IndexByte(v, '\n'); i >= 0 {
			v = v[:i]
		}
		add("claude", "ok", "Claude Code "+strings.TrimSuffix(v, " (Claude Code)")+" — C investigates an alert or service", "")
	}
	switch {
	case os.Getenv("TERM_PROGRAM") == "iTerm.app":
		add("terminal", "ok", "iTerm2 — investigations open in new tabs", "")
	case runtime.GOOS == "darwin":
		add("terminal", "info", "Not iTerm2 — C copies the command to start Claude, for you to paste in a new tab",
			"Opening tabs automatically needs iTerm2 (https://iterm2.com)")
	default:
		add("terminal", "info", "C copies the command to start Claude, for you to paste in a new tab", "")
	}
	return out
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(doctorCmd)
}
