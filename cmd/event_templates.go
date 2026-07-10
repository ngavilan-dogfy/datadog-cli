package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"datadog-cli/datadog"

	"gopkg.in/yaml.v3"
)

type eventTemplate struct {
	Title     string   `yaml:"title"`
	Text      string   `yaml:"text"`
	Priority  string   `yaml:"priority,omitempty"`
	AlertType string   `yaml:"alert_type,omitempty"`
	Tags      []string `yaml:"tags,omitempty"`
	Host      string   `yaml:"host,omitempty"`
}

func eventTemplatesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "datadog-cli", "templates", "events")
}

func loadEventTemplate(name string, vars map[string]string) (*datadog.PostEventRequest, error) {
	path := name
	if !filepath.IsAbs(path) && !strings.Contains(path, "/") {
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			path += ".yaml"
		}
		path = filepath.Join(eventTemplatesDir(), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("event template not found: %s (looked in %s): %w", name, path, err)
	}
	body := applyVars(string(raw), vars)

	var tpl eventTemplate
	if err := yaml.Unmarshal([]byte(body), &tpl); err != nil {
		return nil, fmt.Errorf("event template YAML invalid: %w", err)
	}
	return &datadog.PostEventRequest{
		Title:     tpl.Title,
		Text:      tpl.Text,
		Priority:  tpl.Priority,
		AlertType: tpl.AlertType,
		Tags:      tpl.Tags,
		Host:      tpl.Host,
	}, nil
}
