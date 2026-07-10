package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Name string `yaml:"-"`

	// Auth
	APIKey string `yaml:"api_key,omitempty"`
	AppKey string `yaml:"app_key,omitempty"`

	// Site (e.g. datadoghq.eu, datadoghq.com, us3.datadoghq.com)
	Site string `yaml:"site,omitempty"`

	// Settings
	MaxResults int `yaml:"max_results,omitempty"`
}

// Known Datadog sites with labels.
var KnownSites = []struct {
	Site  string
	Label string
}{
	{"datadoghq.com", "US1 (default)"},
	{"datadoghq.eu", "EU1"},
	{"us3.datadoghq.com", "US3"},
	{"us5.datadoghq.com", "US5"},
	{"ap1.datadoghq.com", "AP1"},
	{"ddog-gov.com", "US1-FED / GovCloud"},
}

func (p *Profile) APIURL() string {
	site := p.Site
	if site == "" {
		site = "datadoghq.com"
	}
	return fmt.Sprintf("https://api.%s", site)
}

func (p *Profile) AppURL() string {
	site := p.Site
	if site == "" {
		site = "datadoghq.com"
	}
	// Sites with subdomains of datadoghq.com (us3, us5, ap1) don't use "app." prefix
	switch site {
	case "datadoghq.com", "datadoghq.eu", "ddog-gov.com":
		return fmt.Sprintf("https://app.%s", site)
	default:
		return fmt.Sprintf("https://%s", site)
	}
}

func (p *Profile) IsAuthenticated() bool {
	return p.APIKey != "" && p.AppKey != ""
}

// --- paths ---

func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "datadog-cli")
}

func ProfileDir() string {
	return filepath.Join(Dir(), "profiles")
}

func activePath() string {
	return filepath.Join(Dir(), "active")
}

func profilePath(name string) string {
	return filepath.Join(ProfileDir(), name+".yaml")
}

// --- active profile ---

func ActiveName() string {
	data, err := os.ReadFile(activePath())
	if err != nil {
		return "default"
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "default"
	}
	return name
}

func SetActive(name string) error {
	if !Exists(name) {
		return fmt.Errorf("profile %q does not exist", name)
	}
	os.MkdirAll(Dir(), 0700)
	return os.WriteFile(activePath(), []byte(name), 0600)
}

// --- CRUD ---

func Load(name string) (*Profile, error) {
	p := &Profile{Name: name}
	data, err := os.ReadFile(profilePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("profile %q not found", name)
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, err
	}
	return p, nil
}

func LoadActive() (*Profile, error) {
	name := ActiveName()
	p, err := Load(name)
	if err != nil {
		return nil, err
	}

	if v := os.Getenv("DD_API_KEY"); v != "" {
		p.APIKey = v
	}
	if v := os.Getenv("DD_APP_KEY"); v != "" {
		p.AppKey = v
	}
	if v := os.Getenv("DD_SITE"); v != "" {
		p.Site = v
	}

	return p, nil
}

func Save(p *Profile) error {
	os.MkdirAll(ProfileDir(), 0700)
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(profilePath(p.Name), data, 0600)
}

func Create(name string) (*Profile, error) {
	if Exists(name) {
		return nil, fmt.Errorf("profile %q already exists", name)
	}
	p := &Profile{Name: name, MaxResults: 25, Site: "datadoghq.eu"}
	if err := Save(p); err != nil {
		return nil, err
	}
	return p, nil
}

func Delete(name string) error {
	if !Exists(name) {
		return fmt.Errorf("profile %q not found", name)
	}
	if ActiveName() == name {
		return fmt.Errorf("cannot delete active profile %q — switch first", name)
	}
	return os.Remove(profilePath(name))
}

func Exists(name string) bool {
	_, err := os.Stat(profilePath(name))
	return err == nil
}

func List() ([]string, error) {
	entries, err := os.ReadDir(ProfileDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	return names, nil
}

// --- property access ---

var ValidKeys = []string{"site", "api_key", "app_key", "max_results"}

func (p *Profile) Get(key string) (string, error) {
	switch key {
	case "site":
		return p.Site, nil
	case "api_key":
		if p.APIKey != "" {
			return p.APIKey[:8] + "****", nil
		}
		return "", nil
	case "app_key":
		if p.AppKey != "" {
			return p.AppKey[:8] + "****", nil
		}
		return "", nil
	case "max_results":
		if p.MaxResults == 0 {
			return "25", nil
		}
		return fmt.Sprintf("%d", p.MaxResults), nil
	default:
		return "", fmt.Errorf("unknown key %q — valid keys: %s", key, strings.Join(ValidKeys, ", "))
	}
}

func (p *Profile) Set(key, value string) error {
	switch key {
	case "site":
		p.Site = value
	case "api_key":
		p.APIKey = value
	case "app_key":
		p.AppKey = value
	case "max_results":
		n := 25
		fmt.Sscanf(value, "%d", &n)
		p.MaxResults = n
	default:
		return fmt.Errorf("unknown key %q — valid keys: %s", key, strings.Join(ValidKeys, ", "))
	}
	return nil
}
