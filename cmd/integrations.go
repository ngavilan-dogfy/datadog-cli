package cmd

import (
	"fmt"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"
	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var integrationsJSON bool

var integrationsCmd = &cobra.Command{
	Use:   "integrations",
	Short: "Manage cloud integrations",
}

// ==================== GCP ====================

var gcpCmd = &cobra.Command{
	Use:     "gcp",
	Aliases: []string{"google"},
	Short:   "Manage GCP integration accounts",
	Long: `List and manage Datadog GCP integration accounts.

Shows service accounts, host filters, monitored resources, and accessible projects.

Examples:
  datadog integrations gcp                 # list accounts
  datadog integrations gcp show <id>       # full details
  datadog integrations gcp projects <id>   # accessible GCP projects
  datadog integrations gcp host-filters set <id> "tag:value" "tag2:value2"
  datadog integrations gcp host-filters clear <id>
  datadog integrations gcp resource-filter set <id> gce_instance "tag:value"
  datadog integrations gcp resource-filter clear <id> gce_instance`,
	RunE: func(cmd *cobra.Command, args []string) error {
		accounts, err := client.ListGCPAccounts()
		if err != nil {
			return err
		}

		if integrationsJSON {
			return printJSON(accounts)
		}

		if len(accounts) == 0 {
			fmt.Println(ui.Dimmed.Render("  No GCP integrations configured."))
			return nil
		}

		if !isTTY() {
			for _, a := range accounts {
				fmt.Printf("%s\t%s\t%s\n", a.ID, a.Attributes.ClientEmail, strings.Join(a.Attributes.HostFilters, ","))
			}
			return nil
		}

		fmt.Println(ui.Title.Render(" GCP Integrations"))

		for _, a := range accounts {
			fmt.Println()
			renderGCPAccountSummary(&a)
		}
		return nil
	},
}

var gcpShowCmd = &cobra.Command{
	Use:   "show [account-id]",
	Short: "Show full GCP account details",
	Long: `Show complete configuration for a GCP integration account.

If no account ID is given and only one account exists, shows that one.

Examples:
  datadog integrations gcp show
  datadog integrations gcp show 5421965d-eece-40c3-... --json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, err := resolveGCPAccount(args)
		if err != nil {
			return err
		}

		if integrationsJSON {
			return printJSON(account)
		}

		renderGCPAccountFull(account)
		return nil
	},
}

var gcpProjectsCmd = &cobra.Command{
	Use:   "projects [account-id]",
	Short: "List accessible GCP projects",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, err := resolveGCPAccount(args)
		if err != nil {
			return err
		}

		if account.Meta == nil || len(account.Meta.AccessibleProjects) == 0 {
			fmt.Println(ui.Dimmed.Render("  No accessible projects found."))
			return nil
		}

		if integrationsJSON {
			return printJSON(account.Meta.AccessibleProjects)
		}

		fmt.Println(ui.Title.Render(" Accessible GCP Projects"))
		for _, p := range account.Meta.AccessibleProjects {
			fmt.Printf("  %s\n", ui.Key.Render(p))
		}
		fmt.Println()
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d projects", len(account.Meta.AccessibleProjects))))
		return nil
	},
}

// --- Host Filters ---

var hostFiltersCmd = &cobra.Command{
	Use:   "host-filters",
	Short: "Manage GCP host filters",
}

var hostFiltersSetCmd = &cobra.Command{
	Use:   "set [account-id] <filter> [filter...]",
	Short: "Set host filters (replaces existing)",
	Long: `Set host filters for a GCP integration account.

Only GCP instances matching these filters will be imported as Datadog hosts.
Use GCP label format: key:value

To exclude all VMs, set a filter that matches nothing:
  datadog integrations gcp host-filters set datadog_monitor:true

Examples:
  datadog integrations gcp host-filters set "project_id:my-project"
  datadog integrations gcp host-filters set "env:prod" "team:platform"
  datadog integrations gcp host-filters set "datadog_monitor:true"   # exclude all unlabeled VMs`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, filters, err := splitAccountAndArgs(args)
		if err != nil {
			return err
		}
		if len(filters) == 0 {
			return fmt.Errorf("at least one filter is required")
		}

		attrs := datadog.GCPAccountPatchAttributes{
			HostFilters: &filters,
		}

		updated, err := client.UpdateGCPAccount(account.ID, attrs)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render("  Updated host filters"))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Filters: %s", strings.Join(updated.Attributes.HostFilters, ", "))))
		return nil
	},
}

var hostFiltersClearCmd = &cobra.Command{
	Use:   "clear [account-id]",
	Short: "Clear host filters (import all hosts)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, err := resolveGCPAccount(args)
		if err != nil {
			return err
		}

		empty := []string{}
		attrs := datadog.GCPAccountPatchAttributes{
			HostFilters: &empty,
		}

		_, err = client.UpdateGCPAccount(account.ID, attrs)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render("  Cleared host filters"))
		fmt.Println(ui.Dimmed.Render("  All GCP hosts will be imported"))
		return nil
	},
}

var hostFiltersGetCmd = &cobra.Command{
	Use:   "get [account-id]",
	Short: "Show current host filters",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, err := resolveGCPAccount(args)
		if err != nil {
			return err
		}

		if integrationsJSON {
			return printJSON(account.Attributes.HostFilters)
		}

		if len(account.Attributes.HostFilters) == 0 {
			fmt.Println(ui.Dimmed.Render("  No host filters (all hosts imported)"))
			return nil
		}

		fmt.Println(ui.Title.Render(" Host Filters"))
		for _, f := range account.Attributes.HostFilters {
			fmt.Printf("  %s\n", ui.Key.Render(f))
		}
		return nil
	},
}

// --- Resource Filters ---

var resourceFilterCmd = &cobra.Command{
	Use:   "resource-filter",
	Short: "Manage GCP monitored resource filters",
}

var resourceFilterSetCmd = &cobra.Command{
	Use:   "set [account-id] <resource-type> <filter> [filter...]",
	Short: "Set filters for a GCP resource type",
	Long: `Set monitored resource filters for a specific GCP resource type.

Common resource types:
  gce_instance       Compute Engine VMs
  gae_app            App Engine
  cloud_run_revision Cloud Run
  cloudsql_database  Cloud SQL
  k8s_container      GKE containers

Examples:
  datadog integrations gcp resource-filter set gce_instance "datadog_monitor:true"
  datadog integrations gcp resource-filter set gce_instance "project_id:prod"
  datadog integrations gcp resource-filter set cloud_run_revision "project_id:prod"`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, remaining, err := splitAccountAndArgs(args)
		if err != nil {
			return err
		}
		if len(remaining) < 2 {
			return fmt.Errorf("usage: resource-filter set [account-id] <resource-type> <filter> [filter...]")
		}

		resourceType := remaining[0]
		filters := remaining[1:]

		// Get current configs and update the specific type
		configs := account.Attributes.MonitoredResourceConfigs
		found := false
		for i, c := range configs {
			if c.Type == resourceType {
				configs[i].Filters = filters
				found = true
				break
			}
		}
		if !found {
			configs = append(configs, datadog.MonitoredResourceConfig{
				Type:    resourceType,
				Filters: filters,
			})
		}

		attrs := datadog.GCPAccountPatchAttributes{
			MonitoredResourceConfigs: &configs,
		}

		updated, err := client.UpdateGCPAccount(account.ID, attrs)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Updated %s filters", resourceType)))
		for _, c := range updated.Attributes.MonitoredResourceConfigs {
			if c.Type == resourceType {
				fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Filters: %s", strings.Join(c.Filters, ", "))))
			}
		}
		return nil
	},
}

var resourceFilterClearCmd = &cobra.Command{
	Use:   "clear [account-id] <resource-type>",
	Short: "Clear filters for a GCP resource type",
	Long: `Remove filter restrictions for a GCP resource type.

Examples:
  datadog integrations gcp resource-filter clear gce_instance`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, remaining, err := splitAccountAndArgs(args)
		if err != nil {
			return err
		}
		if len(remaining) < 1 {
			return fmt.Errorf("resource type is required")
		}

		resourceType := remaining[0]

		// Remove the resource type from configs
		var configs []datadog.MonitoredResourceConfig
		for _, c := range account.Attributes.MonitoredResourceConfigs {
			if c.Type != resourceType {
				configs = append(configs, c)
			}
		}

		attrs := datadog.GCPAccountPatchAttributes{
			MonitoredResourceConfigs: &configs,
		}

		_, err = client.UpdateGCPAccount(account.ID, attrs)
		if err != nil {
			return err
		}

		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Cleared %s filters", resourceType)))
		return nil
	},
}

var resourceFilterGetCmd = &cobra.Command{
	Use:   "get [account-id]",
	Short: "Show current resource filters",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		account, err := resolveGCPAccount(args)
		if err != nil {
			return err
		}

		if integrationsJSON {
			return printJSON(account.Attributes.MonitoredResourceConfigs)
		}

		if len(account.Attributes.MonitoredResourceConfigs) == 0 {
			fmt.Println(ui.Dimmed.Render("  No resource filters configured"))
			return nil
		}

		fmt.Println(ui.Title.Render(" Monitored Resource Configs"))
		for _, c := range account.Attributes.MonitoredResourceConfigs {
			fmt.Printf("  %s\n", ui.Key.Render(c.Type))
			for _, f := range c.Filters {
				fmt.Printf("    %s\n", ui.Dimmed.Render(f))
			}
		}
		return nil
	},
}

// --- Rendering ---

func renderGCPAccountSummary(a *datadog.GCPAccount) {
	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	idShort := a.ID
	if len(idShort) > 12 {
		idShort = idShort[:12] + "..."
	}
	fmt.Println(metaStyle.Render(
		ui.Label.Render("Account:") + " " + ui.Key.Render(idShort) + "  " +
			ui.Dimmed.Render(a.Attributes.ClientEmail)))

	if len(a.Attributes.HostFilters) > 0 {
		fmt.Println(metaStyle.Render(
			ui.Label.Render("Host filters:") + " " + strings.Join(a.Attributes.HostFilters, ", ")))
	} else {
		fmt.Println(metaStyle.Render(
			ui.Label.Render("Host filters:") + " " + ui.Dimmed.Render("(none — all hosts)")))
	}

	if len(a.Attributes.MonitoredResourceConfigs) > 0 {
		for _, c := range a.Attributes.MonitoredResourceConfigs {
			fmt.Println(metaStyle.Render(
				ui.Label.Render("  "+c.Type+":") + " " + strings.Join(c.Filters, ", ")))
		}
	}

	if a.Meta != nil {
		fmt.Println(metaStyle.Render(
			ui.Label.Render("Projects:") + " " +
				ui.Dimmed.Render(fmt.Sprintf("%d accessible", len(a.Meta.AccessibleProjects)))))
	}
}

func renderGCPAccountFull(a *datadog.GCPAccount) {
	fmt.Println(ui.Title.Render(" GCP Integration"))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	type row struct{ label, value string }
	rows := []row{
		{"ID", a.ID},
		{"Email", a.Attributes.ClientEmail},
		{"Automute", fmt.Sprintf("%v", a.Attributes.Automute)},
		{"Resource Col.", fmt.Sprintf("%v", a.Attributes.ResourceCollectionEnabled)},
		{"CSPM", fmt.Sprintf("%v", a.Attributes.IsCspmEnabled)},
	}

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	// Host filters
	fmt.Println()
	fmt.Println(ui.SectionHeader.Render("  Host Filters"))
	if len(a.Attributes.HostFilters) == 0 {
		fmt.Println(metaStyle.Render("    " + ui.Dimmed.Render("(none — all hosts imported)")))
	} else {
		for _, f := range a.Attributes.HostFilters {
			fmt.Println(metaStyle.Render("    " + ui.Key.Render(f)))
		}
	}

	// Resource configs
	if len(a.Attributes.MonitoredResourceConfigs) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Monitored Resources"))
		var tblRows [][]string
		for _, c := range a.Attributes.MonitoredResourceConfigs {
			filters := strings.Join(c.Filters, ", ")
			if filters == "" {
				filters = "(all)"
			}
			tblRows = append(tblRows, []string{c.Type, filters})
		}

		t := table.New().
			Headers("TYPE", "FILTERS").
			Rows(tblRows...).
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
				}
				s := lipgloss.NewStyle().Padding(0, 1)
				if col == 0 {
					s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(25)
				} else {
					s = s.Foreground(ui.Text)
				}
				return s
			})
		fmt.Println(t)
	}

	// Cloud Run filters
	if len(a.Attributes.CloudRunRevisionFilters) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Cloud Run Revision Filters"))
		for _, f := range a.Attributes.CloudRunRevisionFilters {
			fmt.Println(metaStyle.Render("    " + f))
		}
	}

	// Projects
	if a.Meta != nil && len(a.Meta.AccessibleProjects) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render(fmt.Sprintf("  Accessible Projects (%d)", len(a.Meta.AccessibleProjects))))
		for _, p := range a.Meta.AccessibleProjects {
			fmt.Println(metaStyle.Render("    " + p))
		}
	}

	fmt.Println()
}

// --- Helpers ---

func resolveGCPAccount(args []string) (*datadog.GCPAccount, error) {
	accounts, err := client.ListGCPAccounts()
	if err != nil {
		return nil, err
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no GCP integrations configured")
	}

	if len(args) == 0 || args[0] == "" {
		if len(accounts) == 1 {
			return &accounts[0], nil
		}
		return nil, fmt.Errorf("multiple GCP accounts found — specify an account ID")
	}

	id := args[0]
	for _, a := range accounts {
		if a.ID == id || strings.HasPrefix(a.ID, id) {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("GCP account %q not found", id)
}

// splitAccountAndArgs tries to resolve the first arg as an account ID.
// If it fails (not a UUID prefix), assumes single-account mode and treats all args as filters.
func splitAccountAndArgs(args []string) (*datadog.GCPAccount, []string, error) {
	accounts, err := client.ListGCPAccounts()
	if err != nil {
		return nil, nil, err
	}

	if len(accounts) == 0 {
		return nil, nil, fmt.Errorf("no GCP integrations configured")
	}

	// Single account: all args are filters
	if len(accounts) == 1 {
		return &accounts[0], args, nil
	}

	// Multiple accounts: first arg must be account ID
	if len(args) == 0 {
		return nil, nil, fmt.Errorf("multiple GCP accounts — specify account ID as first argument")
	}

	for _, a := range accounts {
		if a.ID == args[0] || strings.HasPrefix(a.ID, args[0]) {
			return &a, args[1:], nil
		}
	}
	return nil, nil, fmt.Errorf("GCP account %q not found", args[0])
}

func init() {
	gcpCmd.Flags().BoolVar(&integrationsJSON, "json", false, "Output as JSON")

	gcpShowCmd.Flags().BoolVar(&integrationsJSON, "json", false, "Output as JSON")
	gcpProjectsCmd.Flags().BoolVar(&integrationsJSON, "json", false, "Output as JSON")
	hostFiltersGetCmd.Flags().BoolVar(&integrationsJSON, "json", false, "Output as JSON")
	resourceFilterGetCmd.Flags().BoolVar(&integrationsJSON, "json", false, "Output as JSON")

	hostFiltersCmd.AddCommand(hostFiltersSetCmd)
	hostFiltersCmd.AddCommand(hostFiltersClearCmd)
	hostFiltersCmd.AddCommand(hostFiltersGetCmd)

	resourceFilterCmd.AddCommand(resourceFilterSetCmd)
	resourceFilterCmd.AddCommand(resourceFilterClearCmd)
	resourceFilterCmd.AddCommand(resourceFilterGetCmd)

	gcpCmd.AddCommand(gcpShowCmd)
	gcpCmd.AddCommand(gcpProjectsCmd)
	gcpCmd.AddCommand(hostFiltersCmd)
	gcpCmd.AddCommand(resourceFilterCmd)

	integrationsCmd.AddCommand(gcpCmd)
	rootCmd.AddCommand(integrationsCmd)
}
