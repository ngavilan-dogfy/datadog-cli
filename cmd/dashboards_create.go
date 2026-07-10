package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"datadog-cli/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	dashCreateTemplate string
	dashCreateSet      []string
	dashCreateFile     string
	dashCreateName     string
	dashCreateJSON     bool
	dashCreateDryRun   bool
)

var dashboardsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a dashboard from a template or a file",
	Long: `Create a new dashboard.

There are three input modes (mutually exclusive):
  --template <name>   Load ~/.config/datadog-cli/templates/dashboards/<name>.yaml,
                      substitute {{vars}} from --set, and POST.
  --file <path>       Read a dashboard JSON/YAML file (same as 'import').
  (stdin via '-')     Same, but read from stdin.

Examples:
  datadog dashboards create --template service-health \
    --set service=web-store --set env=prod
  datadog dashboards create --template incident-investigation \
    --set service=api --set env=prod --set notify=slack-alerts
  datadog dashboards create --template service-health --set service=api --set env=prod --dry-run
  datadog dashboards create --file ./dashboards/api-health.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var payload map[string]interface{}
		var err error

		switch {
		case dashCreateTemplate != "":
			vars, e := parseSetFlags(dashCreateSet)
			if e != nil {
				return e
			}
			payload, err = loadDashboardTemplate(dashCreateTemplate, vars)
			if err != nil {
				return err
			}
		case dashCreateFile != "":
			payload, err = readDashboardFile(dashCreateFile)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("either --template or --file is required")
		}

		if dashCreateName != "" {
			payload["title"] = dashCreateName
		}

		if u := findUnresolvedDashboard(payload); u != "" {
			return fmt.Errorf("unresolved placeholder %q — pass it with --set %s=VALUE",
				u, strings.Trim(u, "{}"))
		}

		if dashCreateDryRun {
			return printJSON(payload)
		}

		created, err := client.CreateDashboard(payload)
		if err != nil {
			return err
		}
		id, _ := created["id"].(string)
		title, _ := created["title"].(string)

		if dashCreateJSON {
			return printJSON(map[string]interface{}{
				"id":    id,
				"title": title,
				"url":   client.DashboardURL(id),
			})
		}
		if !isTTY() {
			fmt.Println(id)
			return nil
		}
		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  created dashboard %s", id)))
		fmt.Println(ui.Dimmed.Render("  " + title))
		fmt.Println(ui.Dimmed.Render("  " + client.DashboardURL(id)))
		return nil
	},
}

// loadDashboardTemplate reads <name>.yaml from the templates dir, substitutes
// vars, and decodes into a map.
func loadDashboardTemplate(name string, vars map[string]string) (map[string]interface{}, error) {
	path := name
	if !filepath.IsAbs(path) && !strings.Contains(path, "/") {
		hadExt := false
		for _, ext := range []string{".yaml", ".yml", ".json"} {
			if strings.HasSuffix(strings.ToLower(path), ext) {
				hadExt = true
				break
			}
		}
		if !hadExt {
			path += ".yaml"
		}
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "datadog-cli", "templates", "dashboards", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dashboard template not found: %s (looked in %s): %w", name, path, err)
	}
	body := applyVars(string(raw), vars)

	var payload map[string]interface{}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal([]byte(body), &payload); err != nil {
			return nil, fmt.Errorf("template YAML invalid: %w", err)
		}
	} else {
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			return nil, fmt.Errorf("template JSON invalid: %w", err)
		}
	}
	return payload, nil
}

// findUnresolvedDashboard walks the payload recursively looking for any {{...}}
// that doesn't match a known Datadog runtime variable. Returns the first.
func findUnresolvedDashboard(node interface{}) string {
	switch v := node.(type) {
	case string:
		i := 0
		for {
			open := strings.Index(v[i:], "{{")
			if open < 0 {
				return ""
			}
			open += i
			close := strings.Index(v[open:], "}}")
			if close < 0 {
				return ""
			}
			tok := v[open : open+close+2]
			inner := strings.TrimSpace(tok[2 : len(tok)-2])
			if !isDDTemplateVar(inner) {
				return tok
			}
			i = open + close + 2
		}
	case map[string]interface{}:
		for _, val := range v {
			if u := findUnresolvedDashboard(val); u != "" {
				return u
			}
		}
	case []interface{}:
		for _, val := range v {
			if u := findUnresolvedDashboard(val); u != "" {
				return u
			}
		}
	}
	return ""
}

func init() {
	dashboardsCreateCmd.Flags().StringVar(&dashCreateTemplate, "template", "", "Template name (e.g. service-health, incident-investigation)")
	dashboardsCreateCmd.Flags().StringSliceVar(&dashCreateSet, "set", nil, "Template variables (--set service=api --set env=prod)")
	dashboardsCreateCmd.Flags().StringVarP(&dashCreateFile, "file", "f", "", "Dashboard JSON/YAML file to create from")
	dashboardsCreateCmd.Flags().StringVar(&dashCreateName, "name", "", "Override the dashboard title")
	dashboardsCreateCmd.Flags().BoolVar(&dashCreateJSON, "json", false, "Output as JSON")
	dashboardsCreateCmd.Flags().BoolVar(&dashCreateDryRun, "dry-run", false, "Print the payload that would be POSTed and exit")

	dashboardsCmd.AddCommand(dashboardsCreateCmd)
}
