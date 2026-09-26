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

var (
	servicesJSON    bool
	servicesPlain   bool
	servicesQuery   string
	serviceShowJSON bool
)

var servicesCmd = &cobra.Command{
	Use:     "services",
	Aliases: []string{"svc"},
	Short:   "List and view services from the Service Catalog",
	Long: `List services registered in the Datadog Service Catalog.

Output adapts automatically:
  • Terminal  → colored table
  • Piped     → tab-separated plain text (TSV)
  • --json    → structured JSON array

Examples:
  datadog services                         # all services
  datadog svc                              # alias
  datadog svc -q "api"                     # filter by name
  datadog svc --json | jq '.[].name'       # extract names`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := client.ListServices()
		if err != nil {
			return err
		}

		if servicesQuery != "" {
			entries = filterServices(entries, servicesQuery)
		}

		if servicesJSON {
			return printJSON(servicesToJSON(entries))
		}

		if len(entries) == 0 {
			if isTTY() && !servicesPlain {
				fmt.Println(ui.Dimmed.Render("  No services found."))
			}
			return nil
		}

		if !isTTY() || servicesPlain {
			return printServicesTSV(entries)
		}

		return printServicesTable(entries)
	},
}

var servicesShowCmd = &cobra.Command{
	Use:   "show <service-name>",
	Short: "Show full service details",
	Long: `Display complete information about a service from the Service Catalog.

Examples:
  datadog services show api-gateway
  datadog svc show api-gateway --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		svc, err := client.GetService(name)
		if err != nil {
			return err
		}

		if serviceShowJSON {
			return printJSON(svc)
		}

		renderService(svc)
		return nil
	},
}

// --- helpers ---

func filterServices(entries []datadog.ServiceCatalogEntry, query string) []datadog.ServiceCatalogEntry {
	q := strings.ToLower(query)
	var filtered []datadog.ServiceCatalogEntry
	for _, e := range entries {
		name := strings.ToLower(e.Attributes.Schema.DDService)
		if strings.Contains(name, q) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

type serviceJSONOut struct {
	Name        string   `json:"name"`
	Team        string   `json:"team,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	Lifecycle   string   `json:"lifecycle,omitempty"`
	Application string   `json:"application,omitempty"`
	Description string   `json:"description,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	URL         string   `json:"url"`
}

func servicesToJSON(entries []datadog.ServiceCatalogEntry) []serviceJSONOut {
	out := make([]serviceJSONOut, len(entries))
	for i, e := range entries {
		s := e.Attributes.Schema
		out[i] = serviceJSONOut{
			Name:        s.DDService,
			Team:        s.Team,
			Tier:        s.Tier,
			Lifecycle:   s.Lifecycle,
			Application: s.Application,
			Description: s.Description,
			Languages:   s.Languages,
			URL:         client.BrowseURL(fmt.Sprintf("/services/%s", s.DDService)),
		}
	}
	return out
}

func printServicesTSV(entries []datadog.ServiceCatalogEntry) error {
	headers := []string{"NAME", "TEAM", "TIER", "LIFECYCLE", "APPLICATION"}
	var rows [][]string
	for _, e := range entries {
		s := e.Attributes.Schema
		rows = append(rows, []string{
			s.DDService,
			s.Team,
			s.Tier,
			s.Lifecycle,
			s.Application,
		})
	}
	printTSV(headers, rows)
	return nil
}

func printServicesTable(entries []datadog.ServiceCatalogEntry) error {
	header := " Service Catalog"
	if servicesQuery != "" {
		header += " · " + servicesQuery
	}
	fmt.Println(ui.Title.Render(header))

	var rows [][]string
	for _, e := range entries {
		s := e.Attributes.Schema
		name := s.DDService
		if len(name) > 30 {
			name = name[:27] + "..."
		}
		desc := s.Description
		if len(desc) > 35 {
			desc = desc[:32] + "..."
		}

		rows = append(rows, []string{
			name,
			s.Team,
			s.Tier,
			s.Lifecycle,
			s.Application,
			desc,
		})
	}

	t := table.New().
		Headers("NAME", "TEAM", "TIER", "LIFECYCLE", "APP", "DESCRIPTION").
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ui.Subtle)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(ui.Secondary).Padding(0, 1)
			}
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0: // NAME
				s = s.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Width(32)
			case 1: // TEAM
				s = s.Foreground(ui.Secondary).Width(15)
			case 2: // TIER
				s = s.Foreground(ui.Warning).Width(8)
			case 3: // LIFECYCLE
				s = s.Foreground(ui.Muted).Width(12)
			case 4: // APP
				s = s.Foreground(ui.Muted).Width(15)
			case 5: // DESCRIPTION
				s = s.Foreground(ui.Text).Width(37)
			}
			return s
		})

	fmt.Println(t)
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %d services", len(entries))))
	return nil
}

func renderService(e *datadog.ServiceCatalogEntry) {
	s := e.Attributes.Schema

	fmt.Println(ui.Title.Render(fmt.Sprintf(" %s", s.DDService)))
	fmt.Println()

	metaStyle := lipgloss.NewStyle().PaddingLeft(2)

	type row struct{ label, value string }
	var rows []row

	if s.Description != "" {
		rows = append(rows, row{"Description", s.Description})
	}
	if s.Team != "" {
		rows = append(rows, row{"Team", s.Team})
	}
	if s.Tier != "" {
		rows = append(rows, row{"Tier", s.Tier})
	}
	if s.Lifecycle != "" {
		rows = append(rows, row{"Lifecycle", s.Lifecycle})
	}
	if s.Application != "" {
		rows = append(rows, row{"Application", s.Application})
	}
	if s.Type != "" {
		rows = append(rows, row{"Type", s.Type})
	}
	if len(s.Languages) > 0 {
		rows = append(rows, row{"Languages", strings.Join(s.Languages, ", ")})
	}
	if len(s.Tags) > 0 {
		rows = append(rows, row{"Tags", strings.Join(s.Tags, ", ")})
	}

	for _, r := range rows {
		line := ui.Label.Render(r.label+":") + " " + r.value
		fmt.Println(metaStyle.Render(line))
	}

	if len(s.Links) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Links"))
		for _, l := range s.Links {
			linkType := l.Type
			if linkType == "" {
				linkType = "link"
			}
			name := l.Name
			if name == "" {
				name = linkType
			}
			fmt.Printf("    %s  %s\n",
				ui.Subtitle.Render(name),
				ui.Dimmed.Render(l.URL))
		}
	}

	if len(s.Contacts) > 0 {
		fmt.Println()
		fmt.Println(ui.SectionHeader.Render("  Contacts"))
		for _, c := range s.Contacts {
			name := c.Name
			if name == "" {
				name = c.Type
			}
			fmt.Printf("    %s  %s\n",
				ui.Subtitle.Render(name),
				ui.Dimmed.Render(c.Contact))
		}
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", client.BrowseURL(fmt.Sprintf("/services/%s", s.DDService)))))
}

func init() {
	servicesCmd.Flags().BoolVar(&servicesJSON, "json", false, "Output as JSON array")
	servicesCmd.Flags().BoolVar(&servicesPlain, "plain", false, "Force plain TSV output")
	servicesCmd.Flags().StringVarP(&servicesQuery, "query", "q", "", "Filter by name")

	servicesShowCmd.Flags().BoolVar(&serviceShowJSON, "json", false, "Output as JSON")

	servicesCmd.AddCommand(servicesShowCmd)
	rootCmd.AddCommand(servicesCmd)
}
