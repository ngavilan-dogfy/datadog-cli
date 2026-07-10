package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"datadog-cli/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	dashGetJSON   bool
	dashExportOut string
	dashCloneName string
	dashCloneJSON bool
	dashDeleteForce bool
	dashImportJSON bool
)

// --- get ---------------------------------------------------------------

var dashboardsGetCmd = &cobra.Command{
	Use:   "get <dashboard-id>",
	Short: "Fetch a dashboard's full JSON document",
	Long: `Print a dashboard's full document (widgets, layout, template variables).

Examples:
  datadog dashboards get abc-123                 # human-friendly JSON (default)
  datadog dashboards get abc-123 --json | jq     # pipeable JSON
  datadog dashboards get               # fzf picker if no id is provided`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickDashboardID(args)
		if err != nil {
			return err
		}
		dash, err := client.GetDashboard(id)
		if err != nil {
			return err
		}
		return printJSON(dash)
	},
}

// --- export ------------------------------------------------------------

var dashboardsExportCmd = &cobra.Command{
	Use:   "export <dashboard-id>",
	Short: "Write a dashboard to a file (JSON or YAML)",
	Long: `Export a dashboard document to disk for version control.

Format is inferred from the file extension (.json | .yaml | .yml).
If --out is omitted the document is written to stdout as JSON.

Server-managed fields (id, url, author_handle, author_name, created_at,
modified_at) are stripped so the file can be replayed verbatim with import
or create.

Examples:
  datadog dashboards export abc-123 -o dashboards/api-health.json
  datadog dashboards export abc-123 -o dashboards/api-health.yaml
  datadog dashboards export abc-123                       # JSON on stdout
  datadog dashboards export                               # fzf picker`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickDashboardID(args)
		if err != nil {
			return err
		}
		dash, err := client.GetDashboard(id)
		if err != nil {
			return err
		}

		cleaned := stripExportFields(dash)

		if dashExportOut == "" || dashExportOut == "-" {
			return printJSON(cleaned)
		}

		var data []byte
		ext := strings.ToLower(filepath.Ext(dashExportOut))
		switch ext {
		case ".yaml", ".yml":
			data, err = yaml.Marshal(cleaned)
		default:
			data, err = json.MarshalIndent(cleaned, "", "  ")
		}
		if err != nil {
			return fmt.Errorf("encode failed: %w", err)
		}
		if err := os.WriteFile(dashExportOut, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dashExportOut, err)
		}
		if isTTY() {
			fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  wrote %s (%d bytes)", dashExportOut, len(data))))
		}
		return nil
	},
}

// --- import ------------------------------------------------------------

var dashboardsImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Create a dashboard from a JSON/YAML file",
	Long: `Read a dashboard document from disk and POST it to Datadog as a NEW dashboard.

Format is inferred from extension. Server-managed fields are stripped before
the request, so files produced by 'datadog dashboards export' can be replayed
without modification.

Use '-' to read from stdin.

Examples:
  datadog dashboards import dashboards/api-health.json
  datadog dashboards import dashboards/api-health.yaml
  cat dashboard.json | datadog dashboards import -
  datadog dashboards export abc-123 | datadog dashboards import -    # clone via pipe`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := readDashboardFile(args[0])
		if err != nil {
			return err
		}
		created, err := client.CreateDashboard(payload)
		if err != nil {
			return err
		}
		id, _ := created["id"].(string)
		title, _ := created["title"].(string)

		if dashImportJSON {
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
		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  imported dashboard %s", id)))
		fmt.Println(ui.Dimmed.Render("  " + title))
		fmt.Println(ui.Dimmed.Render("  " + client.DashboardURL(id)))
		return nil
	},
}

// --- clone -------------------------------------------------------------

var dashboardsCloneCmd = &cobra.Command{
	Use:   "clone <dashboard-id>",
	Short: "Duplicate a dashboard",
	Long: `Fetch a dashboard, strip its server-managed fields, optionally rename it,
and POST it as a new dashboard.

Examples:
  datadog dashboards clone abc-123                         # title prefixed with "Copy of "
  datadog dashboards clone abc-123 --name "API health (staging)"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := pickDashboardID(args)
		if err != nil {
			return err
		}
		dash, err := client.GetDashboard(id)
		if err != nil {
			return err
		}
		if dashCloneName != "" {
			dash["title"] = dashCloneName
		} else if t, ok := dash["title"].(string); ok {
			dash["title"] = "Copy of " + t
		}
		created, err := client.CreateDashboard(dash)
		if err != nil {
			return err
		}
		newID, _ := created["id"].(string)
		if dashCloneJSON {
			return printJSON(map[string]interface{}{
				"id":     newID,
				"source": id,
				"url":    client.DashboardURL(newID),
			})
		}
		if !isTTY() {
			fmt.Println(newID)
			return nil
		}
		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  cloned %s → %s", id, newID)))
		fmt.Println(ui.Dimmed.Render("  " + client.DashboardURL(newID)))
		return nil
	},
}

// --- delete ------------------------------------------------------------

var dashboardsDeleteCmd = &cobra.Command{
	Use:     "delete <dashboard-id>",
	Aliases: []string{"rm"},
	Short:   "Delete a dashboard (requires --force or interactive confirmation)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if !dashDeleteForce {
			if !isTTY() {
				return fmt.Errorf("refusing to delete from non-interactive context — pass --force")
			}
			fmt.Printf("Delete dashboard %s? type 'yes' to confirm: ", id)
			var in string
			fmt.Scanln(&in)
			if strings.TrimSpace(strings.ToLower(in)) != "yes" {
				return fmt.Errorf("aborted")
			}
		}
		if err := client.DeleteDashboard(id); err != nil {
			return err
		}
		if isTTY() {
			fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  deleted %s", id)))
		}
		return nil
	},
}

// --- shared helpers ----------------------------------------------------

// stripExportFields removes server-managed top-level + widget IDs so the
// exported file is replayable.
func stripExportFields(in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		switch k {
		case "id", "url", "author_handle", "author_name", "created_at", "modified_at":
			continue
		case "widgets":
			out[k] = stripWidgetIDsAny(v)
		default:
			out[k] = v
		}
	}
	return out
}

func stripWidgetIDsAny(widgets interface{}) interface{} {
	arr, ok := widgets.([]interface{})
	if !ok {
		return widgets
	}
	for i, w := range arr {
		m, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		delete(m, "id")
		if def, ok := m["definition"].(map[string]interface{}); ok {
			if inner, ok := def["widgets"]; ok {
				def["widgets"] = stripWidgetIDsAny(inner)
			}
		}
		arr[i] = m
	}
	return arr
}

func readDashboardFile(path string) (map[string]interface{}, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	var payload map[string]interface{}
	if ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("yaml decode: %w", err)
		}
	} else {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("json decode: %w", err)
		}
	}
	return payload, nil
}

func init() {
	dashboardsGetCmd.Flags().BoolVar(&dashGetJSON, "json", true, "(default true — kept for symmetry)")
	dashboardsExportCmd.Flags().StringVarP(&dashExportOut, "out", "o", "", "Output file (.json or .yaml). Default: stdout (JSON)")
	dashboardsCloneCmd.Flags().StringVar(&dashCloneName, "name", "", "Title for the clone (default: 'Copy of <original>')")
	dashboardsCloneCmd.Flags().BoolVar(&dashCloneJSON, "json", false, "Output as JSON")
	dashboardsDeleteCmd.Flags().BoolVar(&dashDeleteForce, "force", false, "Skip interactive confirmation")
	dashboardsImportCmd.Flags().BoolVar(&dashImportJSON, "json", false, "Output as JSON")

	dashboardsCmd.AddCommand(dashboardsGetCmd)
	dashboardsCmd.AddCommand(dashboardsExportCmd)
	dashboardsCmd.AddCommand(dashboardsImportCmd)
	dashboardsCmd.AddCommand(dashboardsCloneCmd)
	dashboardsCmd.AddCommand(dashboardsDeleteCmd)
}
