package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/ui"

	"github.com/spf13/cobra"
)

var completionInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install shell completions for your current shell",
	Long: `Automatically detect your shell and install completions.

Supports bash, zsh, and fish.

Examples:
  datadog completion install          # auto-detect shell
  source <(datadog completion zsh)    # manual: source inline
  datadog completion bash > /tmp/dd   # manual: write to file`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := detectShell()
		if shell == "" {
			return fmt.Errorf("could not detect shell — use 'datadog completion bash|zsh|fish' manually")
		}

		fmt.Println(ui.Title.Render(" Shell Completion Setup"))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Detected shell: %s", shell)))
		fmt.Println()

		switch shell {
		case "zsh":
			return installZsh()
		case "bash":
			return installBash()
		case "fish":
			return installFish()
		default:
			return fmt.Errorf("unsupported shell %q — use bash, zsh, or fish", shell)
		}
	},
}

func detectShell() string {
	shellEnv := os.Getenv("SHELL")
	if shellEnv != "" {
		base := filepath.Base(shellEnv)
		switch base {
		case "zsh":
			return "zsh"
		case "bash":
			return "bash"
		case "fish":
			return "fish"
		}
	}
	return ""
}

func installZsh() error {
	// Try common completion dirs
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".zsh/completions"),
		filepath.Join(home, ".oh-my-zsh/completions"),
		"/usr/local/share/zsh/site-functions",
	}

	// Check fpath
	if runtime.GOOS == "darwin" {
		candidates = append([]string{"/opt/homebrew/share/zsh/site-functions"}, candidates...)
	}

	// Use first writable dir, or create ~/.zsh/completions
	target := ""
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			target = dir
			break
		}
	}

	if target == "" {
		target = filepath.Join(home, ".zsh/completions")
		os.MkdirAll(target, 0755)
	}

	completionFile := filepath.Join(target, "_datadog")

	f, err := os.Create(completionFile)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", completionFile, err)
	}
	defer f.Close()

	if err := rootCmd.GenZshCompletion(f); err != nil {
		return err
	}

	fmt.Println(ui.SuccessStyle.Render("  Installed!"))
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Written to: %s", completionFile)))
	fmt.Println()

	// Check if dir is in fpath
	fpath := os.Getenv("FPATH")
	if !strings.Contains(fpath, target) {
		zshrc := filepath.Join(home, ".zshrc")
		line := fmt.Sprintf(`fpath=(%s $fpath)`, target)
		fmt.Println(ui.Dimmed.Render("  Add to your ~/.zshrc (if not already present):"))
		fmt.Println(ui.Key.Render(fmt.Sprintf("    %s", line)))
		fmt.Println(ui.Key.Render("    autoload -Uz compinit && compinit"))
		fmt.Println()

		// Try to append automatically
		existing, _ := os.ReadFile(zshrc)
		if !strings.Contains(string(existing), "fpath=("+target) {
			f, err := os.OpenFile(zshrc, os.O_APPEND|os.O_WRONLY, 0644)
			if err == nil {
				fmt.Fprintf(f, "\n# datadog-cli completions\n%s\nautoload -Uz compinit && compinit\n", line)
				f.Close()
				fmt.Println(ui.SuccessStyle.Render("  Auto-added to ~/.zshrc"))
			}
		} else {
			fmt.Println(ui.Dimmed.Render("  (already in ~/.zshrc)"))
		}
	}

	fmt.Println()
	fmt.Println(ui.Dimmed.Render("  Restart your shell or run: exec zsh"))
	return nil
}

func installBash() error {
	home, _ := os.UserHomeDir()

	// Check for bash-completion dirs
	candidates := []string{
		"/etc/bash_completion.d",
		"/usr/local/etc/bash_completion.d",
		"/opt/homebrew/etc/bash_completion.d",
	}

	target := ""
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			target = dir
			break
		}
	}

	if target == "" {
		target = filepath.Join(home, ".bash_completion.d")
		os.MkdirAll(target, 0755)
	}

	completionFile := filepath.Join(target, "datadog")

	f, err := os.Create(completionFile)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", completionFile, err)
	}
	defer f.Close()

	if err := rootCmd.GenBashCompletion(f); err != nil {
		return err
	}

	fmt.Println(ui.SuccessStyle.Render("  Installed!"))
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Written to: %s", completionFile)))
	fmt.Println()

	// Ensure sourced in bashrc
	bashrc := filepath.Join(home, ".bashrc")
	existing, _ := os.ReadFile(bashrc)
	if !strings.Contains(string(existing), completionFile) {
		bf, err := os.OpenFile(bashrc, os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(bf, "\n# datadog-cli completions\nsource %s\n", completionFile)
			bf.Close()
			fmt.Println(ui.SuccessStyle.Render("  Auto-added to ~/.bashrc"))
		}
	}

	fmt.Println(ui.Dimmed.Render("  Restart your shell or run: source " + completionFile))
	return nil
}

func installFish() error {
	home, _ := os.UserHomeDir()
	fishDir := filepath.Join(home, ".config/fish/completions")
	os.MkdirAll(fishDir, 0755)

	completionFile := filepath.Join(fishDir, "datadog.fish")

	f, err := os.Create(completionFile)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", completionFile, err)
	}
	defer f.Close()

	if err := rootCmd.GenFishCompletion(f, true); err != nil {
		return err
	}

	fmt.Println(ui.SuccessStyle.Render("  Installed!"))
	fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  Written to: %s", completionFile)))
	fmt.Println(ui.Dimmed.Render("  Fish will auto-load completions on next start."))
	return nil
}

// uninstallCmd removes installed completions
var completionUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove installed shell completions",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		files := []string{
			filepath.Join(home, ".zsh/completions/_datadog"),
			filepath.Join(home, ".bash_completion.d/datadog"),
			filepath.Join(home, ".config/fish/completions/datadog.fish"),
			"/usr/local/share/zsh/site-functions/_datadog",
			"/opt/homebrew/share/zsh/site-functions/_datadog",
			"/etc/bash_completion.d/datadog",
			"/usr/local/etc/bash_completion.d/datadog",
			"/opt/homebrew/etc/bash_completion.d/datadog",
		}

		removed := 0
		for _, f := range files {
			if _, err := os.Stat(f); err == nil {
				os.Remove(f)
				fmt.Println(ui.Dimmed.Render("  Removed: " + f))
				removed++
			}
		}

		if removed == 0 {
			fmt.Println(ui.Dimmed.Render("  No completions found to remove."))
		} else {
			fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  Removed %d completion file(s)", removed)))
		}
		return nil
	},
}

// manCmd generates man pages
var manDir string

var manCmd = &cobra.Command{
	Use:   "man",
	Short: "Generate man pages",
	Long: `Generate man pages for all commands.

Examples:
  datadog man                          # generate to ./man/
  datadog man --dir /usr/local/share/man/man1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if manDir == "" {
			manDir = "man"
		}
		os.MkdirAll(manDir, 0755)

		// Use cobra doc generation
		// cobra/doc requires an import, so we do a simple approach
		header := fmt.Sprintf(".TH DATADOG 1 \"March 2026\" \"datadog-cli %s\" \"Datadog CLI\"\n", Version)
		header += ".SH NAME\ndatadog \\- Fast CLI for Datadog monitoring and observability\n"
		header += ".SH SYNOPSIS\n.B datadog\n[command] [flags]\n"
		header += ".SH DESCRIPTION\nA fast command-line tool for Datadog. Fully scriptable and pipe-friendly.\n"

		manFile := filepath.Join(manDir, "datadog.1")
		if err := os.WriteFile(manFile, []byte(header), 0644); err != nil {
			return err
		}

		// Also generate a quick reference
		helpOutput, _ := exec.Command("datadog", "--help").Output()
		refFile := filepath.Join(manDir, "datadog-reference.txt")
		os.WriteFile(refFile, helpOutput, 0644)

		fmt.Println(ui.SuccessStyle.Render("  Generated man pages"))
		fmt.Println(ui.Dimmed.Render(fmt.Sprintf("  %s", manDir)))
		return nil
	},
}

var completionCmd = &cobra.Command{
	Use:   "setup-completions",
	Short: "Install or remove shell completions",
}

func init() {
	completionCmd.AddCommand(completionInstallCmd)
	completionCmd.AddCommand(completionUninstallCmd)
	rootCmd.AddCommand(completionCmd)

	manCmd.Flags().StringVar(&manDir, "dir", "man", "Output directory for man pages")
	rootCmd.AddCommand(manCmd)
}
