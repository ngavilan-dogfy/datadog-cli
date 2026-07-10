package cmd

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"datadog-cli/datadog"
	"datadog-cli/ui"

	"github.com/spf13/cobra"
)

var (
	deployService string
	deployEnv     string
	deployVersion string
	deployRepo    string
	deploySource  string
	deployText    string
	deployTags    []string
	deployStatus  string
	deployJSON    bool
	deployDryRun  bool
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Post a structured deployment event",
	Long: `Post a deployment event to Datadog with the standard conventions
(env, service, version, source, repository). Datadog's Deployment Tracking
and Service Catalog rely on these tags to wire deployments to services.

If --version is omitted and we're inside a git repo, the short SHA is used.

Examples:
  datadog deploy --service api --env prod --version v1.2.3
  datadog deploy --service api --env staging                 # version = git short SHA
  datadog deploy --service api --env prod --status error     # mark a failed deploy
  datadog deploy --service api --env prod --tag release_pipeline:gha
  datadog deploy --service api --env prod --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if deployService == "" {
			return fmt.Errorf("--service is required")
		}
		if deployEnv == "" {
			return fmt.Errorf("--env is required (e.g. prod, staging)")
		}
		if deployVersion == "" {
			if sha, ok := gitShortSHA(); ok {
				deployVersion = sha
			} else {
				return fmt.Errorf("--version is required (or run inside a git repo to autodetect SHA)")
			}
		}
		if deployRepo == "" {
			deployRepo, _ = gitRepoName()
		}

		tags := []string{
			"env:" + deployEnv,
			"service:" + deployService,
			"version:" + deployVersion,
			"source:cli",
		}
		if deployRepo != "" {
			tags = append(tags, "repository:"+deployRepo)
		}
		for _, t := range deployTags {
			for _, p := range strings.Split(t, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					tags = append(tags, p)
				}
			}
		}

		title := fmt.Sprintf("Deploy %s %s → %s", deployService, deployVersion, deployEnv)
		text := deployText
		if text == "" {
			text = fmt.Sprintf("%s deployed to %s at version %s.", deployService, deployEnv, deployVersion)
		}
		alertType := deployStatus
		switch alertType {
		case "":
			alertType = "success"
		case "ok":
			alertType = "success"
		case "fail":
			alertType = "error"
		}

		req := datadog.PostEventRequest{
			Title:     title,
			Text:      text,
			AlertType: alertType,
			Tags:      tags,
		}

		if deployDryRun {
			return printJSON(map[string]interface{}{
				"dry_run": true,
				"payload": req,
			})
		}

		event, err := client.PostEvent(req)
		if err != nil {
			return fmt.Errorf("failed to post deploy event: %w", err)
		}

		if deployJSON {
			return printJSON(map[string]interface{}{
				"id":    event.ID,
				"title": event.Title,
				"url":   event.URL,
				"tags":  event.Tags,
			})
		}
		if !isTTY() {
			fmt.Println(event.ID)
			return nil
		}
		fmt.Println(ui.SuccessStyle.Render(fmt.Sprintf("  OK  Posted deploy event %d", event.ID)))
		fmt.Println(ui.Dimmed.Render("  " + title))
		fmt.Println(ui.Dimmed.Render("  tags: " + strings.Join(tags, ", ")))
		if event.URL != "" {
			fmt.Println(ui.Dimmed.Render("  " + event.URL))
		}
		return nil
	},
}

func gitShortSHA() (string, bool) {
	out, err := runCapture("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(out)
	return sha, sha != ""
}

func gitRepoName() (string, error) {
	// Try "origin/owner/repo" from the remote URL
	out, err := runCapture("git", "config", "--get", "remote.origin.url")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	// Handle ssh: git@github.com:owner/repo.git
	if strings.Contains(url, ":") && strings.Contains(url, "@") {
		idx := strings.LastIndex(url, ":")
		url = url[idx+1:]
	} else if strings.HasPrefix(url, "https://") {
		// strip host
		parts := strings.SplitN(url, "/", 4)
		if len(parts) == 4 {
			url = parts[3]
		}
	}
	url = strings.TrimSuffix(url, ".git")
	return url, nil
}

func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func init() {
	deployCmd.Flags().StringVarP(&deployService, "service", "s", "", "Service name (required)")
	deployCmd.Flags().StringVarP(&deployEnv, "env", "e", "", "Environment (e.g. prod, staging)")
	deployCmd.Flags().StringVarP(&deployVersion, "version", "v", "", "Version/SHA (defaults to git short SHA)")
	deployCmd.Flags().StringVar(&deployRepo, "repo", "", "Repository (owner/repo) — autodetected from git if not set")
	deployCmd.Flags().StringVar(&deploySource, "source", "ci", "Source label (added as source:<value> tag)")
	deployCmd.Flags().StringVar(&deployText, "text", "", "Custom event body (defaults to a summary)")
	deployCmd.Flags().StringSliceVar(&deployTags, "tag", nil, "Extra tags (repeatable or comma-separated)")
	deployCmd.Flags().StringVar(&deployStatus, "status", "success", "Deploy status: success|error|warning (default success)")
	deployCmd.Flags().BoolVar(&deployJSON, "json", false, "Output as JSON")
	deployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Print the payload without posting")
	rootCmd.AddCommand(deployCmd)
}
