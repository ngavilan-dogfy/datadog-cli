package cmd

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/config"

	"github.com/spf13/cobra"
)

// Commands that change Datadog. Read-only profiles (for AI agents) refuse
// them, 'datadog schema' marks them with "mutates": true, and the skill tells
// agents to ask before running them. Keep it complete when adding commands.
var mutatingCommands = []string{
	"datadog monitors mute", "datadog monitors unmute", "datadog monitors create",
	"datadog monitors edit", "datadog monitors import", "datadog monitors delete",
	"datadog batch mute", "datadog batch unmute",
	"datadog downtimes schedule", "datadog downtimes cancel",
	"datadog hosts mute", "datadog hosts unmute",
	"datadog dashboards create", "datadog dashboards clone", "datadog dashboards import",
	"datadog dashboards delete",
	"datadog incidents create", "datadog incidents update",
	"datadog events post", "datadog deploy",
	"datadog synthetics trigger",
	"datadog tags add", "datadog tags set", "datadog tags rm",
	"datadog integrations gcp host-filters set", "datadog integrations gcp host-filters clear",
	"datadog integrations gcp resource-filter set", "datadog integrations gcp resource-filter clear",
}

// mutates reports whether a command changes Datadog.
func mutates(c *cobra.Command) bool {
	path := c.CommandPath()
	for _, m := range mutatingCommands {
		if path == m {
			return true
		}
	}
	return false
}

// guardReadOnly stops a command that would change Datadog when the profile
// is read-only. A --dry-run only previews, so it's allowed.
func guardReadOnly(c *cobra.Command) error {
	if cfg == nil || !cfg.ReadOnly || !mutates(c) {
		return nil
	}
	if f := c.Flags().Lookup("dry-run"); f != nil && f.Value.String() == "true" {
		return nil
	}
	how := "set read_only: false in the profile"
	if config.ReadOnlyFromEnv() {
		how = "unset DATADOG_READ_ONLY"
	}
	return fmt.Errorf("profile %q is read-only, and '%s' would change your Datadog (to allow it: %s)", cfg.Name, strings.TrimPrefix(c.CommandPath(), "datadog "), how)
}
