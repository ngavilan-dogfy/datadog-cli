package cmd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// The Claude Code skill ships inside the binary, so it always matches the
// commands this version has. The copy in .claude/skills/ is the same file.
//
//go:embed skill_data/SKILL.md
var skillMD []byte

var skillQuiet bool

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage the Claude Code skill (/datadog)",
	Long: `Install, update or remove the datadog skill for Claude Code, so any
Claude session knows how to use this CLI: /datadog, or just ask about
alerts, dashboards, logs or a service.

Examples:
  datadog skill install    # also run after 'datadog update' to refresh it
  datadog skill status
  datadog skill remove`,
}

func skillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your home folder: %w", err)
	}
	return filepath.Join(home, ".claude", "skills", "datadog", "SKILL.md"), nil
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install or refresh the /datadog skill for Claude Code",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dest, err := skillPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create skill folder: %w", err)
		}
		if err := os.WriteFile(dest, skillMD, 0o644); err != nil {
			return fmt.Errorf("write skill: %w", err)
		}
		if skillQuiet {
			return nil
		}
		fmt.Println()
		sayOK("Claude Code skill installed " + wzMuted.Render(tildePath(dest)))
		fmt.Println("    Use " + wzBold.Render("/datadog") + " in any Claude Code session, or just ask:")
		fmt.Println(wzMuted.Render("    \"what's alerting?\", \"why is api slow since 10:00?\", \"show me the on-call dashboard\""))
		fmt.Println()
		return nil
	},
}

var skillRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the /datadog skill from Claude Code",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dest, err := skillPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			sayInfo("The skill isn't installed.")
			return nil
		}
		if err := os.RemoveAll(filepath.Dir(dest)); err != nil {
			return fmt.Errorf("remove skill: %w", err)
		}
		sayOK("Skill removed")
		return nil
	},
}

var skillStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether the Claude Code skill is installed and current",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dest, err := skillPath()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(dest)
		switch {
		case os.IsNotExist(err):
			sayInfo("Not installed. Run: " + cmdHint("datadog skill install"))
		case err != nil:
			return err
		case string(data) != string(skillMD):
			sayWarn("Installed but from another version "+wzMuted.Render(tildePath(dest)), "Refresh it: datadog skill install")
		default:
			sayOK("Installed and current " + wzMuted.Render(tildePath(dest)))
		}
		return nil
	},
}

// skillInstalled reports whether the user has the skill, so updates can
// refresh it.
func skillInstalled() bool {
	dest, err := skillPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(dest)
	return err == nil
}

func init() {
	skillInstallCmd.Flags().BoolVarP(&skillQuiet, "quiet", "q", false, "Print nothing on success (for scripts and installers)")
	skillCmd.AddCommand(skillInstallCmd, skillRemoveCmd, skillStatusCmd)
	rootCmd.AddCommand(skillCmd)
}
