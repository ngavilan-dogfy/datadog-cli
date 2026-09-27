package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	createMonType     string
	createMonQuery    string
	createMonMessage  string
	createMonTags     string
	createMonPriority int
	createMonJSON     bool
	createMonTemplate string
	createMonSet      []string
	createMonDryRun   bool

	editMonName     string
	editMonQuery    string
	editMonMessage  string
	editMonTags     string
	editMonPriority int

	deleteMonForce bool

	importMonFile string
)

var monitorsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new monitor",
	Long: `Create a Datadog monitor.

The monitor type and query are required. Common types:
  metric alert      - threshold on a metric
  query alert       - threshold on a query
  service check     - service check status
  log alert         - threshold on log events
  process alert     - process liveness check
  synthetics alert  - synthetic test status

Examples:
  datadog monitors create "High CPU" --type "metric alert" --query "avg(last_5m):avg:system.cpu.user{*} > 90"
  datadog monitors create "Error rate" --type "query alert" --query "..." --message "@slack-alerts" --tags "env:prod,team:platform"
  datadog monitors create "Disk space" --type "metric alert" --query "..." --priority 2`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Two paths: from --template (loads YAML, applies --set vars, flags override)
		// or from raw --type/--query flags.
		var req datadog.CreateMonitorRequest

		if createMonTemplate != "" {
			vars, err := parseSetFlags(createMonSet)
			if err != nil {
				return err
			}
			tpl, err := loadMonitorTemplate(createMonTemplate, vars)
			if err != nil {
				return err
			}
			req = *tpl
			if len(args) > 0 && args[0] != "" {
				req.Name = args[0]
			}
			// CLI flags override template fields
			if createMonType != "" {
				req.Type = createMonType
			}
			if createMonQuery != "" {
				req.Query = createMonQuery
			}
			if createMonMessage != "" {
				req.Message = createMonMessage
			}
			if createMonTags != "" {
				req.Tags = strings.Split(createMonTags, ",")
			}
			if createMonPriority > 0 && createMonPriority <= 5 {
				req.Priority = &createMonPriority
			}
		} else {
			if len(args) == 0 {
				return fmt.Errorf("monitor name is required (positional arg) when --template is not used")
			}
			req = datadog.CreateMonitorRequest{
				Name:    args[0],
				Type:    createMonType,
				Query:   createMonQuery,
				Message: createMonMessage,
			}
			if createMonTags != "" {
				req.Tags = strings.Split(createMonTags, ",")
			}
			if createMonPriority > 0 && createMonPriority <= 5 {
				req.Priority = &createMonPriority
			}
		}

		if req.Type == "" {
			return fmt.Errorf("--type is required (or set it in the template)")
		}
		if req.Query == "" {
			return fmt.Errorf("--query is required (or set it in the template)")
		}
		if req.Name == "" {
			return fmt.Errorf("monitor name is required")
		}

		// Reject unresolved {{placeholders}} so we don't post broken queries.
		if u := findUnresolved(req); u != "" {
			return fmt.Errorf("unresolved placeholder %q — pass it with --set %s=VALUE", u, strings.Trim(u, "{}"))
		}

		if createMonDryRun {
			return printJSON(req)
		}

		monitor, err := client.CreateMonitor(req)
		if err != nil {
			return err
		}

		if createMonJSON {
			return printJSON(monitor)
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Created monitor %d", monitor.ID)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", monitor.Name)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/monitors/%d", monitor.ID)))))
		return nil
	},
}

// findUnresolved scans the request for `{{...}}` placeholders that weren't
// substituted. Datadog's own message-template variables (e.g. {{value}},
// {{hostname}}, {{is_alert}}, {{event.*}}) are evaluated at notification time
// by Datadog and are NOT errors — only our own placeholders are.
func findUnresolved(req datadog.CreateMonitorRequest) string {
	scan := func(s string) string {
		i := 0
		for {
			open := strings.Index(s[i:], "{{")
			if open < 0 {
				return ""
			}
			open += i
			close := strings.Index(s[open:], "}}")
			if close < 0 {
				return ""
			}
			token := s[open : open+close+2]
			inner := strings.TrimSpace(token[2 : len(token)-2])
			if !isDDTemplateVar(inner) {
				return token
			}
			i = open + close + 2
		}
	}
	if u := scan(req.Name); u != "" {
		return u
	}
	if u := scan(req.Query); u != "" {
		return u
	}
	if u := scan(req.Message); u != "" {
		return u
	}
	for _, t := range req.Tags {
		if u := scan(t); u != "" {
			return u
		}
	}
	return ""
}

// isDDTemplateVar returns true for placeholders that Datadog substitutes itself
// at notification time, so we should leave them in place.
func isDDTemplateVar(name string) bool {
	if strings.Contains(name, ".") {
		// e.g. event.id, host.name, event.org.id
		return true
	}
	if strings.HasPrefix(name, "is_") || strings.HasPrefix(name, "#is_") || strings.HasPrefix(name, "/is_") {
		return true
	}
	switch name {
	case "value", "threshold", "warn_threshold", "critical_threshold", "ok_threshold",
		"hostname", "host", "alert_status", "alert_type", "alert_priority",
		"last_triggered_at", "last_triggered_at_epoch":
		return true
	}
	return false
}

var monitorsEditCmd = &cobra.Command{
	Use:   "edit <monitor-id>",
	Short: "Edit monitor fields",
	Long: `Update fields on an existing monitor.

Only provided flags will be updated, other fields remain unchanged.

Examples:
  datadog monitors edit 12345 --name "New name"
  datadog monitors edit 12345 --query "avg(last_5m):avg:system.cpu.user{*} > 95"
  datadog monitors edit 12345 --message "@slack-alerts" --priority 1
  datadog monitors edit 12345 --tags "env:prod,team:platform"
  datadog monitors edit 12345 --option renotify_interval=60 --option evaluation_delay=300
  datadog monitors edit 12345 --threshold critical=0.05 --threshold critical_recovery=0.03

--option and --threshold change one option or threshold and keep the rest
(values are read as JSON when they can be: 60, true, null). 'datadog monitors
review' writes these commands for you.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid monitor ID: %s", args[0])
		}

		fields := map[string]interface{}{}

		if cmd.Flags().Changed("name") {
			fields["name"] = editMonName
		}
		if cmd.Flags().Changed("query") {
			fields["query"] = editMonQuery
		}
		if cmd.Flags().Changed("message") {
			fields["message"] = editMonMessage
		}
		if cmd.Flags().Changed("tags") {
			fields["tags"] = strings.Split(editMonTags, ",")
		}
		if cmd.Flags().Changed("priority") {
			fields["priority"] = editMonPriority
		}
		if len(editMonOptions) > 0 || len(editMonThresholds) > 0 {
			opts, err := client.MonitorOptionsRaw(id)
			if err != nil {
				return err
			}
			if err := applySettings(opts, editMonOptions); err != nil {
				return fmt.Errorf("--option: %w", err)
			}
			if len(editMonThresholds) > 0 {
				th, _ := opts["thresholds"].(map[string]interface{})
				if th == nil {
					th = map[string]interface{}{}
				}
				if err := applySettings(th, editMonThresholds); err != nil {
					return fmt.Errorf("--threshold: %w", err)
				}
				opts["thresholds"] = th
			}
			fields["options"] = opts
		}

		if len(fields) == 0 {
			return fmt.Errorf("no fields to update — use --name, --query, --message, --tags, --priority, --option or --threshold")
		}

		if err := client.UpdateMonitor(id, fields); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Updated monitor %d", id)))
		return nil
	},
}

var monitorsDeleteCmd = &cobra.Command{
	Use:   "delete <monitor-id>",
	Short: "Delete a monitor",
	Long: `Delete a Datadog monitor permanently.

Requires confirmation unless --force is provided.

Examples:
  datadog monitors delete 12345
  datadog monitors delete 12345 --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid monitor ID: %s", args[0])
		}

		if !deleteMonForce {
			// Get monitor name first
			monitor, err := client.GetMonitor(id)
			if err != nil {
				return err
			}

			fmt.Println(ui.ErrorStyle.Render(fmt.Sprintf("  Delete monitor %d?", id)))
			fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", monitor.Name)))
			fmt.Println()
			fmt.Print("  Type the monitor ID to confirm: ")

			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != args[0] {
				fmt.Println(ui.Dimmed.Render("  Cancelled."))
				return nil
			}
		}

		if err := client.DeleteMonitor(id); err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Deleted monitor %d", id)))
		return nil
	},
}

var monitorsImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a monitor from JSON file or stdin",
	Long: `Create a monitor from a JSON definition file.

The JSON should match the Datadog monitor API format.
Useful for restoring exported monitors or creating from templates.

Examples:
  datadog monitors import -f monitor.json
  cat monitor.json | datadog monitors import
  datadog monitors show 12345 --json | jq 'del(.id)' | datadog monitors import`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var data []byte
		var err error

		if importMonFile != "" {
			data, err = os.ReadFile(importMonFile)
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}
		} else {
			data, err = readStdin()
			if err != nil {
				return fmt.Errorf("failed to read stdin: %w", err)
			}
		}

		var req datadog.CreateMonitorRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		if req.Name == "" {
			return fmt.Errorf("monitor name is required in JSON")
		}
		if req.Type == "" {
			return fmt.Errorf("monitor type is required in JSON")
		}
		if req.Query == "" {
			return fmt.Errorf("monitor query is required in JSON")
		}

		monitor, err := client.CreateMonitor(req)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Imported monitor %d", monitor.ID)))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", monitor.Name)))
		return nil
	},
}

var monitorsExportCmd = &cobra.Command{
	Use:   "export <monitor-id>",
	Short: "Export monitor definition as JSON",
	Long: `Export a monitor's definition as JSON for backup or import.

The output strips runtime fields (id, created, modified, etc.)
so it can be directly used with 'datadog monitors import'.

Examples:
  datadog monitors export 12345 > monitor.json
  datadog monitors export 12345 | datadog monitors import     # clone a monitor`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid monitor ID: %s", args[0])
		}

		monitor, err := client.GetMonitor(id)
		if err != nil {
			return err
		}

		// Build exportable definition
		export := datadog.CreateMonitorRequest{
			Name:     monitor.Name,
			Type:     monitor.Type,
			Query:    monitor.Query,
			Message:  monitor.Message,
			Tags:     monitor.Tags,
			Priority: monitor.Priority,
			Options: map[string]interface{}{
				"thresholds":     monitor.Options.Thresholds,
				"notify_no_data": monitor.Options.NotifyNoData,
				"notify_audit":   monitor.Options.NotifyAudit,
			},
		}
		if monitor.Options.TimeoutH != nil {
			export.Options["timeout_h"] = *monitor.Options.TimeoutH
		}

		return printJSON(export)
	},
}

func readStdin() ([]byte, error) {
	var buf []byte
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		buf = append(buf, scanner.Bytes()...)
		buf = append(buf, '\n')
	}
	return buf, scanner.Err()
}

func init() {
	monitorsCreateCmd.Flags().StringVar(&createMonType, "type", "", "Monitor type (e.g. 'metric alert', 'query alert', 'log alert')")
	monitorsCreateCmd.Flags().StringVar(&createMonQuery, "query", "", "Monitor query")
	monitorsCreateCmd.Flags().StringVar(&createMonMessage, "message", "", "Notification message (supports @mentions)")
	monitorsCreateCmd.Flags().StringVar(&createMonTags, "tags", "", "Comma-separated tags (e.g. env:prod,team:platform)")
	monitorsCreateCmd.Flags().IntVar(&createMonPriority, "priority", 0, "Priority (1-5, P1=highest)")
	monitorsCreateCmd.Flags().BoolVar(&createMonJSON, "json", false, "Output created monitor as JSON")
	monitorsCreateCmd.Flags().StringVar(&createMonTemplate, "template", "", "Template name (looks in ~/.config/datadog-cli/templates/monitors/<name>.yaml)")
	monitorsCreateCmd.Flags().StringSliceVar(&createMonSet, "set", nil, "Template variable (repeatable: --set service=api --set env=prod)")
	monitorsCreateCmd.Flags().BoolVar(&createMonDryRun, "dry-run", false, "Print the payload that would be POSTed and exit")

	monitorsEditCmd.Flags().StringVar(&editMonName, "name", "", "New monitor name")
	monitorsEditCmd.Flags().StringVar(&editMonQuery, "query", "", "New monitor query")
	monitorsEditCmd.Flags().StringVar(&editMonMessage, "message", "", "New notification message")
	monitorsEditCmd.Flags().StringVar(&editMonTags, "tags", "", "New tags (comma-separated)")
	monitorsEditCmd.Flags().IntVar(&editMonPriority, "priority", 0, "New priority (1-5)")
	monitorsEditCmd.Flags().StringArrayVar(&editMonOptions, "option", nil, "Set one option, keeping the rest: key=value (repeatable; renotify_interval=60)")
	monitorsEditCmd.Flags().StringArrayVar(&editMonThresholds, "threshold", nil, "Set one threshold, keeping the rest: name=value (repeatable; critical=0.05)")

	monitorsDeleteCmd.Flags().BoolVar(&deleteMonForce, "force", false, "Skip confirmation")

	monitorsImportCmd.Flags().StringVarP(&importMonFile, "file", "f", "", "JSON file to import")

	monitorsCmd.AddCommand(monitorsCreateCmd)
	monitorsCmd.AddCommand(monitorsEditCmd)
	monitorsCmd.AddCommand(monitorsDeleteCmd)
	monitorsCmd.AddCommand(monitorsImportCmd)
	monitorsCmd.AddCommand(monitorsExportCmd)
}

var editMonOptions, editMonThresholds []string

// applySettings sets key=value pairs on a JSON object, reading each value
// as JSON when it is (60, true, null, {"a":1}) and as text otherwise; null
// removes the key.
func applySettings(obj map[string]interface{}, pairs []string) error {
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return fmt.Errorf("%q isn't key=value", kv)
		}
		var val interface{}
		if err := json.Unmarshal([]byte(v), &val); err != nil {
			val = v
		}
		if val == nil {
			delete(obj, k)
			continue
		}
		obj[k] = val
	}
	return nil
}
