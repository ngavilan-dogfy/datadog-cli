package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	"gopkg.in/yaml.v3"
)

// monitorTemplate is the YAML schema for ~/.config/datadog-cli/templates/monitors/*.yaml.
//
// Any field can contain {{placeholders}} which are substituted from --set key=value.
type monitorTemplate struct {
	Name     string                 `yaml:"name"`
	Type     string                 `yaml:"type"`
	Query    string                 `yaml:"query"`
	Message  string                 `yaml:"message,omitempty"`
	Tags     []string               `yaml:"tags,omitempty"`
	Priority int                    `yaml:"priority,omitempty"`
	Options  map[string]interface{} `yaml:"options,omitempty"`
}

func monitorTemplatesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "datadog-cli", "templates", "monitors")
}

// loadMonitorTemplate reads <name>.yaml (or absolute path / containing /), then
// substitutes {{key}} occurrences using the provided vars map.
func loadMonitorTemplate(name string, vars map[string]string) (*datadog.CreateMonitorRequest, error) {
	path := name
	if !filepath.IsAbs(path) && !strings.Contains(path, "/") {
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			path += ".yaml"
		}
		path = filepath.Join(monitorTemplatesDir(), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("template not found: %s (looked in %s): %w", name, path, err)
	}
	body := applyVars(string(raw), vars)

	var tpl monitorTemplate
	if err := yaml.Unmarshal([]byte(body), &tpl); err != nil {
		return nil, fmt.Errorf("template YAML invalid: %w", err)
	}

	req := &datadog.CreateMonitorRequest{
		Name:    tpl.Name,
		Type:    tpl.Type,
		Query:   tpl.Query,
		Message: tpl.Message,
		Tags:    tpl.Tags,
		Options: tpl.Options,
	}
	if tpl.Priority > 0 && tpl.Priority <= 5 {
		p := tpl.Priority
		req.Priority = &p
	}
	return req, nil
}

func applyVars(s string, vars map[string]string) string {
	out := s
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "{{ "+k+" }}", v)
	}
	return out
}

// parseSetFlags converts --set key=value flags into a map.
func parseSetFlags(set []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range set {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --set %q (expected key=value)", s)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}
