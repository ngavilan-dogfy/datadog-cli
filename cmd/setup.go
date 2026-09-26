package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/internal/uiprefs"
	"github.com/ngavilan-dogfy/datadog-cli/viz"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var (
	setupProfile   string
	setupSite      string
	setupKeysStdin bool
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	Aliases: []string{"login", "auth", "init"},
	Short:   "Connect datadog to your Datadog organization, step by step",
	Long: `A guided setup that connects this CLI to your Datadog organization. It
takes about two minutes and you can re-run it any time: current values are
the defaults, so you only change what you want.

  Step 1  Your Datadog site     the region your organization lives in
  Step 2  API key               identifies your organization
  Step 3  Application key       acts as you, with your permissions
  Step 4  Extras                chart style, read-only or not, the Claude Code skill

In steps 2 and 3 you don't have to paste: click Copy on the key in
Datadog and setup takes it from your clipboard (and clears it after saving).

Settings are saved in ~/.config/datadog-cli/ (only readable by you).

Without a terminal (scripts, CI, agents) nothing is asked: pass the keys in
DD_API_KEY and DD_APP_KEY (or two lines on stdin with --keys-stdin):

  DD_API_KEY=… DD_APP_KEY=… datadog setup --site eu
  printf '%s\n%s\n' "$API" "$APP" | datadog setup --site eu --keys-stdin

Or skip setup entirely: DD_API_KEY, DD_APP_KEY and DD_SITE are enough.

Output:
  Guided: questions and a summary. Non-interactive: one line saying what was
  configured; errors explain what's wrong and how to fix it.

Examples:
  datadog setup                     # guided
  datadog setup --profile staging   # a second profile (switch: 'datadog profile use staging')`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := setupProfile
		if profile == "" {
			profile = config.ActiveName()
		}
		if setupKeysStdin || !interactive() {
			existing, _ := config.Load(profile)
			return setupNonInteractive(profile, existing)
		}
		return runSetup(profile, "")
	},
}

// runSetup runs the guided setup. then is the command that triggered it on
// a first run: it carries on afterwards, so the wizard doesn't offer the UI.
func runSetup(profile, then string) error {
	existing, _ := config.Load(profile)
	w := &wizard{profile: profile, existing: existing, then: then}
	err := w.run()
	if errors.Is(err, errCancelled) {
		fmt.Println()
		fmt.Println(wzMuted.Render("  Setup cancelled — nothing was changed. Run 'datadog setup' whenever you're ready."))
		if then != "" {
			return fmt.Errorf("datadog isn't set up yet — run 'datadog setup'")
		}
		return nil
	}
	return err
}

// offerSetup is the first-run prompt for a command that needs Datadog.
func offerSetup(then string) error {
	fmt.Println()
	fmt.Println(wzAccent.Render("  datadog") + wzMuted.Render(" isn't connected to your Datadog yet."))
	yes := true
	err := ask(huh.NewConfirm().
		Title("Set it up now? It takes about two minutes.").
		Description("Afterwards '" + then + "' runs as you asked.").
		Affirmative("Yes, set it up").Negative("Not now").Value(&yes))
	if err != nil || !yes {
		return fmt.Errorf("datadog isn't set up yet — run 'datadog setup' when you're ready")
	}
	return runSetup(config.ActiveName(), then)
}

// welcome is what a bare 'datadog' shows before setup.
func welcome() error {
	fmt.Println()
	fmt.Println(wzAccent.Render("  datadog") + "  Datadog from your terminal — for you and your AI agents")
	fmt.Println()
	fmt.Println("  You're not connected to Datadog yet. Setup takes about two minutes:")
	fmt.Println("  pick your site and paste two keys from Datadog's settings.")
	fmt.Println()
	if !interactive() {
		fmt.Println("  Run " + cmdHint("datadog setup") + " in a terminal, or set DD_API_KEY, DD_APP_KEY and DD_SITE.")
		fmt.Println("  " + cmdHint("datadog --help") + " lists every command.")
		return nil
	}
	yes := true
	if err := ask(huh.NewConfirm().Title("Set it up now?").Affirmative("Yes").Negative("Not now").Value(&yes)); err != nil || !yes {
		fmt.Println(wzMuted.Render("  Run 'datadog setup' when you're ready · 'datadog --help' lists every command."))
		return nil
	}
	return runSetup(config.ActiveName(), "")
}

// ─── the guided flow ─────────────────────────────────────────────

const setupSteps = 4

type wizard struct {
	profile  string
	existing *config.Profile
	then     string

	site   string
	apiKey string
	p      *config.Profile
	who    identity
}

type identity struct {
	name, handle, org string
}

func (w *wizard) run() error {
	fmt.Println()
	fmt.Println(wzAccent.Render("  datadog setup"))
	fmt.Println(wzMuted.Render("  Connect this CLI to your Datadog organization — about two minutes."))
	fmt.Println(wzMuted.Render("  Nothing is saved until the end. Ctrl+C cancels at any point."))

	if w.existing != nil && w.existing.IsAuthenticated() && w.then == "" {
		next, err := w.askReconfigure()
		if err != nil || next == "done" {
			return err
		}
	}
	if err := w.stepSite(); err != nil {
		return err
	}
	if err := w.stepAPIKey(); err != nil {
		return err
	}
	if err := w.stepAppKey(); err != nil {
		return err
	}
	if err := w.save(); err != nil {
		return err
	}
	if err := w.stepExtras(); err != nil && !errors.Is(err, errCancelled) {
		return err
	}
	return w.done()
}

func (w *wizard) askReconfigure() (string, error) {
	e := w.existing
	fmt.Println()
	sayOK(fmt.Sprintf("Profile %q is set up: %s (%s) · API key %s", w.profile, e.Site, config.SiteLabel(e.Site), config.Mask(e.APIKey)))
	fmt.Println()
	choice := "check"
	if err := ask(huh.NewSelect[string]().
		Title("What would you like to do?").
		Options(
			huh.NewOption("Check that everything works", "check"),
			huh.NewOption("Set it up again (site, keys…)", "all"),
			huh.NewOption("Nothing, leave it as it is", "done"),
		).Value(&choice)); err != nil {
		return "", err
	}
	switch choice {
	case "check":
		return "done", runDoctor(e, false)
	case "done":
		return "done", nil
	}
	return choice, nil
}

// ─── step 1: site ────────────────────────────────────────────────

// siteProbe answers whether a site's API is reachable; swappable in tests.
var siteProbe = func(site string) error {
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Get((&config.Profile{Site: site}).APIURL() + "/api/v1/validate")
	if err != nil {
		return fmt.Errorf("can't reach api.%s: %s", site, rootCause(err))
	}
	resp.Body.Close()
	return nil // any answer (403 without a key) means the site exists
}

func (w *wizard) stepSite() error {
	stepHeader(1, setupSteps, "Your Datadog site",
		"Datadog keeps each region separate. Look at the address you use to open Datadog\nin your browser and pick the same one.")
	site := setupSite
	if s, ok := config.ParseSite(site); ok {
		site = s
	} else if w.existing != nil && w.existing.Site != "" {
		site = w.existing.Site
	} else {
		site = "datadoghq.eu"
	}
	for {
		choice := site
		var opts []huh.Option[string]
		for _, s := range config.KnownSites {
			host := strings.TrimPrefix((&config.Profile{Site: s.Site}).AppURL(), "https://")
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-22s %s", host, wzMuted.Render(strings.TrimSuffix(s.Label, " (default)"))), s.Site))
		}
		opts = append(opts, huh.NewOption("Not sure — I'll paste a link from Datadog", "paste"))
		if err := ask(huh.NewSelect[string]().Title("Which address do you use for Datadog?").Options(opts...).Value(&choice)); err != nil {
			return err
		}
		if choice == "paste" {
			link := ""
			if err := ask(huh.NewInput().
				Title("Paste any link from Datadog").
				Placeholder("https://app.datadoghq.eu/dashboard/abc-123").
				Value(&link).
				Validate(func(s string) error {
					if _, ok := config.ParseSite(s); !ok {
						return fmt.Errorf("that isn't a Datadog address (…datadoghq.com, …datadoghq.eu, …ddog-gov.com)")
					}
					return nil
				})); err != nil {
				return err
			}
			choice, _ = config.ParseSite(link)
		}
		err := withSpinner("Reaching "+choice, func() error { return siteProbe(choice) })
		if err == nil {
			sayOK(fmt.Sprintf("Site %s %s", wzBold.Render(choice), wzMuted.Render("("+config.SiteLabel(choice)+")")))
			w.site = choice
			return nil
		}
		sayFail(err.Error(), "Check your connection or VPN, then try again.")
		site = choice
	}
}

// ─── step 2: API key ─────────────────────────────────────────────

var (
	reAPIKey = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reAppKey = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// cleanKey drops spaces, line breaks and invisible characters a paste can bring.
func cleanKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func keyShapeHint(key string, api bool) string {
	switch {
	case api && reAppKey.MatchString(key):
		return "That looks like an application key (40 characters) — the API key has 32. The application key comes next."
	case !api && reAPIKey.MatchString(key):
		return "That looks like an API key (32 characters) — the application key has 40."
	case api && !reAPIKey.MatchString(key):
		return fmt.Sprintf("API keys are 32 characters of 0-9 and a-f; this one has %d. Copy it again with the Copy button.", len(key))
	case !api && !reAppKey.MatchString(key):
		return fmt.Sprintf("Application keys are 40 characters of 0-9 and a-f; this one has %d. Copy it again with the Copy button.", len(key))
	}
	return ""
}

// validateAPIKey checks a key against a site; swappable in tests.
var validateAPIKey = func(site, key string) (bool, error) {
	p := &config.Profile{Site: site}
	c := datadog.NewClient(p.APIURL(), p.AppURL(), key, "")
	r, err := c.Validate()
	if err != nil {
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "403") {
			return false, nil
		}
		return false, err
	}
	return r.Valid, nil
}

// siteForKey tries a key on every other site: keys only work on the site
// that issued them, and picking the wrong region is the most common mistake.
func siteForKey(key, except string) string {
	var mu sync.Mutex
	found := ""
	var wg sync.WaitGroup
	for _, s := range config.KnownSites {
		if s.Site == except {
			continue
		}
		wg.Add(1)
		go func(site string) {
			defer wg.Done()
			if ok, _ := validateAPIKey(site, key); ok {
				mu.Lock()
				found = site
				mu.Unlock()
			}
		}(s.Site)
	}
	wg.Wait()
	return found
}

func (w *wizard) stepAPIKey() error {
	app := (&config.Profile{Site: w.site}).AppURL()
	page := app + "/organization-settings/api-keys"
	stepHeader(2, setupSteps, "API key",
		"The API key identifies your organization to Datadog.")
	printSteps(
		"Your browser opens "+wzBold.Render(strings.TrimPrefix(page, "https://")),
		"Click a key you can use, or "+wzBold.Render("+ New Key")+" and call it "+wzBold.Render(keyName()),
		"Click "+wzBold.Render("Copy")+" on the key: setup picks it up from your clipboard",
	)
	printFix("Can't see or create API keys? That takes Datadog admin rights: ask an admin for one.\nAny key of your organization works; this CLI only reads with it unless you allow changes.")
	fmt.Println()
	open := true
	if err := ask(huh.NewConfirm().Title("Open that page now?").Affirmative("Yes, open it").Negative("I already have one").Value(&open)); err != nil {
		return err
	}
	if open {
		openURL(page)
		sayInfo("Opened " + page)
	}
	fmt.Println()
	tried := map[string]bool{}
	for {
		if len(tried) > 0 {
			fmt.Println()
		}
		key, err := readKey(keyPrompt{Title: "Your API key", Label: "API key",
			Hint:  "32 characters. Hidden while you type, saved only on this machine.",
			Clean: cleanKey, Match: reAPIKey.MatchString, Skip: tried})
		if err != nil {
			return err
		}
		tried[key] = true
		if hint := keyShapeHint(key, true); hint != "" && reAppKey.MatchString(key) {
			sayFail("That isn't an API key", hint)
			continue
		}
		var ok bool
		err = withSpinner("Checking the key with "+w.site, func() error { var e error; ok, e = validateAPIKey(w.site, key); return e })
		if err != nil {
			sayFail("Couldn't check the key: "+rootCause(err), "Check your connection or VPN and try again.")
			continue
		}
		if ok {
			sayOK("API key accepted " + wzMuted.Render(config.Mask(key)))
			w.apiKey = key
			return nil
		}
		var other string
		_ = withSpinner("Datadog didn't accept it here — trying the other sites", func() error { other = siteForKey(key, w.site); return nil })
		if other != "" {
			sayWarn("That key belongs to "+other+" ("+config.SiteLabel(other)+"), not "+w.site, "")
			use := true
			if err := ask(huh.NewConfirm().Title("Use " + other + " as your site?").Affirmative("Yes").Negative("No, let me paste another key").Value(&use)); err != nil {
				return err
			}
			if use {
				w.site = other
				sayOK("Site " + wzBold.Render(other) + " · API key accepted " + wzMuted.Render(config.Mask(key)))
				w.apiKey = key
				return nil
			}
			continue
		}
		fix := "• Copy the whole key with the Copy button in Datadog\n• The key may have been revoked: create a new one on that page"
		if hint := keyShapeHint(key, true); hint != "" {
			fix = "• " + hint + "\n" + fix
		}
		sayFail("Datadog didn't accept that API key on any site", fix)
	}
}

// ─── step 3: application key ─────────────────────────────────────

// whoAmI identifies the application key's owner; swappable in tests.
var whoAmI = func(p *config.Profile) (identity, error) {
	c := buildClient(p)
	u, err := c.CurrentUser()
	if err != nil {
		return identity{}, err
	}
	id := identity{name: u.Name, handle: u.Handle}
	if org, err := c.OrgName(); err == nil {
		id.org = org
	}
	return id, nil
}

func (w *wizard) stepAppKey() error {
	app := (&config.Profile{Site: w.site}).AppURL()
	page := app + "/personal-settings/application-keys"
	stepHeader(3, setupSteps, "Application key",
		"The application key lets the CLI act as you: it sees exactly what you can see.")
	printSteps(
		"Your browser opens "+wzBold.Render(strings.TrimPrefix(page, "https://"))+wzMuted.Render(" (anyone can create one)"),
		"Click "+wzBold.Render("+ New Key")+" and call it "+wzBold.Render(keyName()),
		"Leave the scopes empty (it reads what you can see) and create it",
		"Click "+wzBold.Render("Copy")+" — it's shown only once — setup picks it up from your clipboard",
	)
	fmt.Println()
	open := true
	if err := ask(huh.NewConfirm().Title("Open that page now?").Affirmative("Yes, open it").Negative("I already have one").Value(&open)); err != nil {
		return err
	}
	if open {
		openURL(page)
		sayInfo("Opened " + page)
	}
	fmt.Println()
	tried := map[string]bool{w.apiKey: true}
	for {
		if len(tried) > 1 {
			fmt.Println()
		}
		key, err := readKey(keyPrompt{Title: "Your application key", Label: "application key",
			Hint:  "40 characters. Hidden while you type, saved only on this machine.",
			Clean: cleanKey, Match: reAppKey.MatchString, Skip: tried})
		if err != nil {
			return err
		}
		tried[key] = true
		if key == w.apiKey {
			sayFail("That's the API key again", "The application key is a different one, created under Personal Settings.")
			continue
		}
		p := &config.Profile{Name: w.profile, Site: w.site, APIKey: w.apiKey, AppKey: key, MaxResults: 25}
		if w.existing != nil && w.existing.MaxResults > 0 {
			p.MaxResults = w.existing.MaxResults
		}
		var id identity
		err = withSpinner("Checking the application key", func() error { var e error; id, e = whoAmI(p); return e })
		if err == nil {
			who := id.name
			if who == "" {
				who = id.handle
			}
			line := "Signed in as " + wzBold.Render(who)
			if id.org != "" {
				line += " · " + wzBold.Render(id.org)
			}
			sayOK(line)
			w.p, w.who = p, id
			w.showAccess()
			return nil
		}
		fix := "• Check it's an application key from the same organization as the API key\n• Keys created by someone else only work if they're shared with you"
		if hint := keyShapeHint(key, false); hint != "" {
			fix = "• " + hint + "\n" + fix
		}
		sayFail("Datadog didn't accept that application key: "+rootCause(err), fix)
	}
}

// showAccess reports which parts of Datadog the keys can read — scoped
// application keys silently break single areas otherwise.
func (w *wizard) showAccess() {
	var checks []check
	_ = withSpinner("Checking what the keys can read", func() error { checks = accessChecks(buildClient(w.p)); return nil })
	var ok, missing []string
	for _, c := range checks {
		if c.Status == "ok" {
			ok = append(ok, c.Name)
		} else {
			missing = append(missing, c.Name)
		}
	}
	if len(missing) == 0 {
		sayOK("Can read " + strings.Join(ok, ", "))
		return
	}
	sayWarn("Can't read "+strings.Join(missing, ", "), "The application key is probably scoped. Create one without scopes to use every command.")
}

func (w *wizard) save() error {
	if err := config.Save(w.p); err != nil {
		return fmt.Errorf("couldn't save your settings: %w", err)
	}
	if err := config.SetActive(w.profile); err != nil {
		return err
	}
	sayOK("Saved to " + tildePath(config.ProfileDir()+"/"+w.profile+".yaml") + wzMuted.Render(" (only readable by you)"))
	clearTakenKeys()
	return nil
}

// keyName is what to call the keys in Datadog, so whoever reviews them
// later knows where they're used.
func keyName() string {
	host, _ := os.Hostname()
	host = strings.TrimSuffix(strings.TrimSuffix(host, ".local"), ".lan")
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	if host == "" {
		return "datadog-cli"
	}
	return "datadog-cli · " + strings.ToLower(host)
}

// ─── step 4: extras ──────────────────────────────────────────────

func (w *wizard) stepExtras() error {
	stepHeader(4, setupSteps, "Extras (optional)",
		"How charts are drawn, whether this CLI may change things, and Claude Code.")
	style := uiprefs.ChartStyle()
	fmt.Println("  " + wzMuted.Render("A. braille — finer, needs a font with braille"))
	for _, l := range viz.Sample(viz.Braille, 44, 4) {
		fmt.Println("    " + l)
	}
	fmt.Println("  " + wzMuted.Render("B. blocks — works with every font"))
	for _, l := range viz.Sample(viz.Blocks, 44, 4) {
		fmt.Println("    " + l)
	}
	fmt.Println()
	if err := ask(huh.NewSelect[string]().
		Title("Which chart looks right in your terminal?").
		Description("If A shows faint dots, empty boxes or question marks, pick B. Change it any time in datadog ui with B.").
		Options(huh.NewOption("A. braille", "braille"), huh.NewOption("B. blocks", "blocks")).
		Value(&style)); err != nil {
		return err
	}
	if err := uiprefs.SetChartStyle(style); err == nil {
		sayOK("Charts: " + style)
	}

	fmt.Println()
	mode := "write"
	if w.p.ReadOnly {
		mode = "read"
	}
	if err := ask(huh.NewSelect[string]().
		Title("May this CLI change things in Datadog?").
		Description("Reading is always on. You can change this later: read_only in "+tildePath(config.ProfileDir()+"/"+w.profile+".yaml")).
		Options(
			huh.NewOption("Yes: mute monitors, create dashboards and monitors (agents ask you first)", "write"),
			huh.NewOption("No, read-only: every change is refused — the safest for AI agents", "read"),
		).Value(&mode)); err != nil {
		return err
	}
	if ro := mode == "read"; ro != w.p.ReadOnly {
		w.p.ReadOnly = ro
		if err := config.Save(w.p); err != nil {
			return fmt.Errorf("couldn't save your settings: %w", err)
		}
	}
	if w.p.ReadOnly {
		sayOK("Read-only: nothing in Datadog can be changed from this profile")
	} else {
		sayOK("Read and write " + wzMuted.Render("(DATADOG_READ_ONLY=1 makes a single session read-only)"))
	}

	if _, err := lookTool("claude"); err == nil {
		if err := offerSkill(); err != nil {
			return err
		}
	}
	for _, c := range extraChecks() {
		if c.Name == "terminal" || (c.Name == "claude" && c.Status != "ok") {
			printCheck(c)
		}
	}
	return nil
}

// offerSkill installs (or refreshes) the /datadog skill for Claude Code,
// if the user wants it.
func offerSkill() error {
	dest, err := skillPath()
	if err != nil {
		return nil
	}
	current, _ := os.ReadFile(dest)
	if string(current) == string(skillMD) {
		sayOK("Claude Code knows this CLI " + wzMuted.Render("(/datadog skill installed)"))
		return nil
	}
	yes := true
	title := "Teach Claude Code to use this CLI? (/datadog skill)"
	if len(current) > 0 {
		title = "Refresh the /datadog skill for Claude Code to this version?"
	}
	fmt.Println()
	if err := ask(huh.NewConfirm().Title(title).
		Description("Then ask Claude about alerts, a slow service, a trace or a dashboard link,\nand it investigates with these commands — asking before it changes anything.").
		Affirmative("Yes").Negative("No").Value(&yes)); err != nil || !yes {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, skillMD, 0o644); err != nil {
		return err
	}
	sayOK("Claude Code skill installed " + wzMuted.Render(tildePath(dest)))
	return nil
}

func (w *wizard) done() error {
	fmt.Println()
	fmt.Println("  " + wzOK.Render("●") + " " + wzBold.Render("All set!"))
	fmt.Println()
	who := w.who.name
	if w.who.handle != "" && w.who.handle != who {
		who += wzMuted.Render(" · " + w.who.handle)
	}
	rows := [][2]string{{"Site", w.p.Site + wzMuted.Render(" ("+config.SiteLabel(w.p.Site)+")")}}
	if w.who.org != "" {
		rows = append(rows, [2]string{"Organization", w.who.org})
	}
	rows = append(rows, [2]string{"Signed in as", who}, [2]string{"Profile", w.profile})
	for _, r := range rows {
		fmt.Printf("    %s %s\n", wzMuted.Render(fmt.Sprintf("%-13s", r[0])), r[1])
	}
	fmt.Println()
	if w.then != "" {
		fmt.Println(wzMuted.Render("  Now running '" + w.then + "'…"))
		fmt.Println()
		return nil
	}
	fmt.Println("  Try these:")
	tries := [][2]string{
		{"datadog status", "what's alerting right now"},
		{"datadog ui", "dashboards, monitors and logs in your terminal (? for help)"},
		{"datadog coverage", "which services have no monitors, and the ones to add"},
		{"datadog doctor", "check that everything works"},
		{"datadog --help", "every command"},
	}
	if skillInstalled() {
		tries = append(tries, [2]string{"/datadog", "in Claude Code: \"why is api slow since 10:00?\""})
	}
	for _, c := range tries {
		fmt.Printf("    %s %s\n", cmdHint(fmt.Sprintf("%-16s", c[0])), wzMuted.Render(c[1]))
	}
	fmt.Println()
	open := true
	if err := ask(huh.NewConfirm().Title("Open datadog ui now?").Affirmative("Yes").Negative("Not now").Value(&open)); err != nil || !open {
		return nil
	}
	cfg, client = w.p, buildClient(w.p)
	return uiCmd.RunE(uiCmd, nil)
}

// ─── non-interactive ─────────────────────────────────────────────

func setupNonInteractive(profile string, existing *config.Profile) error {
	api, app := os.Getenv("DD_API_KEY"), os.Getenv("DD_APP_KEY")
	if setupKeysStdin {
		sc := bufio.NewScanner(io.LimitReader(os.Stdin, 8<<10))
		var lines []string
		for sc.Scan() {
			if l := cleanKey(sc.Text()); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) != 2 {
			return fmt.Errorf("--keys-stdin expects two lines: the API key, then the application key")
		}
		api, app = lines[0], lines[1]
	}
	api, app = cleanKey(api), cleanKey(app)
	site := setupSite
	if site == "" {
		site = os.Getenv("DD_SITE")
	}
	if site == "" && existing != nil {
		site = existing.Site
	}
	var missing []string
	if site == "" {
		missing = append(missing, "--site (or DD_SITE)")
	}
	if api == "" {
		missing = append(missing, "the API key (DD_API_KEY)")
	}
	if app == "" {
		missing = append(missing, "the application key (DD_APP_KEY)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("no terminal to ask questions in, so these are needed: %s\n"+
			"example: DD_API_KEY=… DD_APP_KEY=… datadog setup --site eu", strings.Join(missing, ", "))
	}
	s, ok := config.ParseSite(site)
	if !ok {
		return fmt.Errorf("%q isn't a Datadog site — use eu, us1, us3, us5, ap1, ap2, gov or paste a Datadog link", site)
	}
	if err := siteProbe(s); err != nil {
		return err
	}
	ok, err := validateAPIKey(s, api)
	if err != nil {
		return err
	}
	if !ok {
		if other := siteForKey(api, s); other != "" {
			return fmt.Errorf("that API key belongs to %s (%s), not %s — run again with --site %s", other, config.SiteLabel(other), s, other)
		}
		return fmt.Errorf("Datadog didn't accept that API key on any site")
	}
	p := &config.Profile{Name: profile, Site: s, APIKey: api, AppKey: app, MaxResults: 25}
	if existing != nil && existing.MaxResults > 0 {
		p.MaxResults = existing.MaxResults
	}
	id, err := whoAmI(p)
	if err != nil {
		return fmt.Errorf("Datadog didn't accept the application key: %s", rootCause(err))
	}
	if err := config.Save(p); err != nil {
		return err
	}
	if err := config.SetActive(profile); err != nil {
		return err
	}
	who := id.name
	if id.org != "" {
		who += " (" + id.org + ")"
	}
	fmt.Printf("configured profile %q: %s as %s\n", profile, s, who)
	return nil
}

// ─── logout ──────────────────────────────────────────────────────

var logoutProfile string

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the keys from a profile",
	Long: `Remove the API and application keys from a profile (default: the active
one). The keys stay valid in Datadog: revoke them there if they leaked.

Examples:
  datadog logout
  datadog logout --profile staging`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := logoutProfile
		if name == "" {
			name = config.ActiveName()
		}
		p, err := config.Load(name)
		if err != nil {
			return err
		}
		p.APIKey, p.AppKey = "", ""
		if err := config.Save(p); err != nil {
			return fmt.Errorf("failed to save: %w", err)
		}
		sayOK("Keys removed from profile " + name)
		fmt.Println(wzMuted.Render("  They still work in Datadog — revoke them in Organization Settings if they leaked."))
		return nil
	},
}

// ─── helpers ─────────────────────────────────────────────────────

func rootCause(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && !strings.Contains(s[i:], " — ") {
		s = s[i+2:]
	}
	return s
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// openURL opens a page in the default browser; swappable in tests.
var openURL = func(u string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", u).Start()
	case "linux":
		exec.Command("xdg-open", u).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
}

func init() {
	f := setupCmd.Flags()
	f.StringVarP(&setupProfile, "profile", "p", "", "Profile to create or update (default: the active one)")
	f.StringVar(&setupSite, "site", "", "Datadog site: eu, us1, us3, us5, ap1, ap2, gov, or any Datadog link")
	f.BoolVar(&setupKeysStdin, "keys-stdin", false, "Read the API key and the application key from stdin (two lines)")
	logoutCmd.Flags().StringVarP(&logoutProfile, "profile", "p", "", "Profile (default: the active one)")
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(logoutCmd)
}
