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
	{"ap1.datadoghq.com", "AP1 (Japan)"},
	{"ap2.datadoghq.com", "AP2 (Australia)"},
	{"ddog-gov.com", "US1-FED (GovCloud)"},
}

// ParseSite understands a site name ("datadoghq.eu", "eu", "us5"), an app
// host ("app.datadoghq.eu", "us5.datadoghq.com", "acme.datadoghq.eu") or any
// Datadog URL pasted from the browser, and returns the site it belongs to.
func ParseSite(input string) (string, bool) {
	in := strings.ToLower(strings.TrimSpace(input))
	if in == "" {
		return "", false
	}
	switch in { // short names
	case "us", "us1", "com":
		return "datadoghq.com", true
	case "eu", "eu1":
		return "datadoghq.eu", true
	case "us3", "us5", "ap1", "ap2":
		return in + ".datadoghq.com", true
	case "gov", "fed", "us1-fed":
		return "ddog-gov.com", true
	}
	host := in
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#:"); i >= 0 {
		host = host[:i]
	}
	for _, s := range KnownSites {
		if host == s.Site || strings.HasSuffix(host, "."+s.Site) {
			// app.datadoghq.eu, api.us5.datadoghq.com, acme.datadoghq.eu (custom
			// subdomain) — but us5.datadoghq.com must not become datadoghq.com.
			if s.Site == "datadoghq.com" {
				for _, r := range []string{"us3", "us5", "ap1", "ap2"} {
					if host == r+".datadoghq.com" || strings.HasSuffix(host, "."+r+".datadoghq.com") {
						return r + ".datadoghq.com", true
					}
				}
			}
			return s.Site, true
		}
	}
	return "", false
}

// SiteLabel is the region name of a site ("EU1").
func SiteLabel(site string) string {
	for _, s := range KnownSites {
		if s.Site == site {
			return strings.TrimSuffix(s.Label, " (default)")
		}
	}
	return site
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
	// Sites with subdomains of datadoghq.com (us3, us5, ap1, ap2) don't use "app." prefix
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
		// No profile on disk: environment variables alone are enough (CI,
		// containers, agents): DD_API_KEY + DD_APP_KEY [+ DD_SITE].
		if envP := envProfile(); envP != nil {
			return envP, nil
		}
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

// envProfile builds a profile purely from DD_API_KEY, DD_APP_KEY and
// DD_SITE, or returns nil when the keys are missing.
func envProfile() *Profile {
	api, app := os.Getenv("DD_API_KEY"), os.Getenv("DD_APP_KEY")
	if api == "" || app == "" {
		return nil
	}
	site := "datadoghq.com"
	if v := os.Getenv("DD_SITE"); v != "" {
		if s, ok := ParseSite(v); ok {
			site = s
		} else {
			site = v
		}
	}
	return &Profile{Name: "env", APIKey: api, AppKey: app, Site: site, MaxResults: 25}
}

// Save writes the profile atomically (temp file + rename), readable only by
// the user: a reader must never see a half-written file.
func Save(p *Profile) error {
	if err := os.MkdirAll(ProfileDir(), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(ProfileDir(), "."+p.Name+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), profilePath(p.Name))
}

// Mask hides a key but its last 4 characters — how Datadog lists keys in
// Organization Settings, so it can be recognised there.
func Mask(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return "****" + key[len(key)-4:]
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
		return Mask(p.APIKey), nil
	case "app_key":
		return Mask(p.AppKey), nil
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
