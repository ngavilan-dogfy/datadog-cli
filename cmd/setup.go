package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"datadog-cli/config"
	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// sanitizeKey strips any non-printable / non-ASCII characters from a key.
// term.ReadPassword can capture invisible control chars in some terminals.
var reNonPrint = regexp.MustCompile(`[^a-zA-Z0-9]`)

func sanitizeKey(raw []byte) string {
	return reNonPrint.ReplaceAllString(strings.TrimSpace(string(raw)), "")
}

// readKey tries term.ReadPassword first; if the result looks dirty
// (contains non-hex chars), falls back to plain stdin reading.
func readKey(reader *bufio.Reader) (string, error) {
	// Try hidden input first
	raw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err == nil {
		key := sanitizeKey(raw)
		if key != "" {
			return key, nil
		}
	}

	// Fallback: read from stdin as plain text
	fmt.Print("  (retry, input visible) Key: ")
	line, _ := reader.ReadString('\n')
	key := sanitizeKey([]byte(line))
	if key == "" {
		return "", fmt.Errorf("key cannot be empty")
	}
	return key, nil
}

var loginProfile string

var loginCmd = &cobra.Command{
	Use:     "login",
	Aliases: []string{"setup", "auth"},
	Short:   "Authenticate with Datadog",
	Long: `Set up your Datadog API key and Application key.

You can get your keys from:
  API Key: Organization Settings → API Keys
  App Key: Organization Settings → Application Keys

The CLI stores credentials in ~/.config/datadog-cli/profiles/<name>.yaml

Examples:
  datadog login                     # interactive setup
  datadog login -p production       # setup for specific profile`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)
		profileName := loginProfile
		if profileName == "" {
			profileName = config.ActiveName()
		}

		existing, _ := config.Load(profileName)

		fmt.Println(ui.Title.Render(" Datadog CLI Login"))
		fmt.Println(ui.Dimmed.Render("  Profile: " + profileName))
		fmt.Println()

		// Site selection
		defaultSite := "datadoghq.eu"
		if existing != nil && existing.Site != "" {
			defaultSite = existing.Site
		}

		fmt.Println(ui.Subtitle.Render("  Datadog Sites:"))
		for i, s := range config.KnownSites {
			marker := "  "
			if s.Site == defaultSite {
				marker = ui.SuccessStyle.Render("* ")
			}
			fmt.Printf("    %s%s  %s\n",
				marker,
				ui.Subtitle.Render(fmt.Sprintf("%d.", i+1)),
				fmt.Sprintf("%s  %s", ui.Key.Render(s.Site), ui.Dimmed.Render(s.Label)))
		}
		fmt.Println()

		siteInput := prompt(reader, "Site", defaultSite)
		site := parseSiteInput(siteInput)
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  API: https://api.%s", site)))
		fmt.Println()

		// API Key
		fmt.Println(ui.Dimmed.Render("  Get your API key from: Organization Settings → API Keys"))
		fmt.Println()

		fmt.Print("  API Key: ")
		apiKey, err := readKey(reader)
		if err != nil {
			return fmt.Errorf("failed to read API key: %w", err)
		}
		if apiKey == "" {
			return fmt.Errorf("API key cannot be empty")
		}

		// App Key
		fmt.Println()
		fmt.Println(ui.Dimmed.Render("  Get your Application key from: Organization Settings → Application Keys"))
		fmt.Println()

		fmt.Print("  App Key: ")
		appKey, err := readKey(reader)
		if err != nil {
			return fmt.Errorf("failed to read App key: %w", err)
		}
		if appKey == "" {
			return fmt.Errorf("App key cannot be empty")
		}

		// Validate
		fmt.Println()
		fmt.Print(ui.Dimmed.Render("  Validating... "))

		p := &config.Profile{
			Name:       profileName,
			APIKey:     apiKey,
			AppKey:     appKey,
			Site:       site,
			MaxResults: 25,
		}
		if existing != nil && existing.MaxResults > 0 {
			p.MaxResults = existing.MaxResults
		}

		testClient := datadog.NewClient(p.APIURL(), p.AppURL(), p.APIKey, p.AppKey)
		result, err := testClient.Validate()
		if err != nil {
			fmt.Println(ui.ErrorStyle.Render("FAIL"))
			return fmt.Errorf("validation failed: %w", err)
		}
		if !result.Valid {
			fmt.Println(ui.ErrorStyle.Render("FAIL"))
			return fmt.Errorf("invalid API key")
		}
		fmt.Println(ui.SuccessStyle.Render("OK"))

		// Save
		if err := config.Save(p); err != nil {
			return fmt.Errorf("failed to save: %w", err)
		}
		config.SetActive(profileName)

		fmt.Println()
		fmt.Println(ui.SuccessStyle.Render("  Done!"))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Site: %s · Profile: %s", site, profileName)))

		return nil
	},
}

var logoutProfile string

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear credentials from profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		name := logoutProfile
		if name == "" {
			name = config.ActiveName()
		}

		p, err := config.Load(name)
		if err != nil {
			return err
		}

		p.APIKey = ""
		p.AppKey = ""

		if err := config.Save(p); err != nil {
			return fmt.Errorf("failed to save: %w", err)
		}

		fmt.Println(ui.SuccessStyle.Render("  Logged out"))
		fmt.Println(ui.Dimmed.Render("  Credentials cleared from profile: " + name))

		return nil
	},
}

// --- helpers ---

func parseSiteInput(input string) string {
	input = strings.TrimSpace(input)

	// Accept numeric selection from the known sites list
	idx := 0
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(config.KnownSites) {
		return config.KnownSites[idx-1].Site
	}

	// Strip protocol if pasted
	input = strings.TrimPrefix(input, "https://")
	input = strings.TrimPrefix(input, "http://")
	input = strings.TrimPrefix(input, "api.")
	input = strings.TrimPrefix(input, "app.")
	input = strings.TrimRight(input, "/")

	if input == "" {
		return "datadoghq.eu"
	}
	return input
}

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("  %s: ", label)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

func openURL(u string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", u).Start()
	case "linux":
		exec.Command("xdg-open", u).Start()
	}
}

func init() {
	loginCmd.Flags().StringVarP(&loginProfile, "profile", "p", "", "Target profile (default: active)")
	logoutCmd.Flags().StringVarP(&logoutProfile, "profile", "p", "", "Target profile (default: active)")
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}
