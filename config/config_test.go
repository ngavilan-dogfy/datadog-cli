package config

import (
	"strings"
	"testing"
)

func TestKeyMasking(t *testing.T) {
	p := &Profile{APIKey: "abcdefgh12345678", AppKey: "zyxwvuts87654321"}
	got, err := p.Get("api_key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "abcdefgh") || got != "****5678" {
		t.Errorf("api_key not masked like Datadog shows keys (last 4): %q", got)
	}
}

func TestSetGetRoundtrip(t *testing.T) {
	p := &Profile{}
	if err := p.Set("site", "datadoghq.eu"); err != nil {
		t.Fatal(err)
	}
	got, _ := p.Get("site")
	if got != "datadoghq.eu" {
		t.Errorf("got %q", got)
	}
	if err := p.Set("bogus", "x"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestURLsForSites(t *testing.T) {
	cases := []struct{ site, api, app string }{
		{"", "https://api.datadoghq.com", "https://app.datadoghq.com"},
		{"datadoghq.eu", "https://api.datadoghq.eu", "https://app.datadoghq.eu"},
		{"us3.datadoghq.com", "https://api.us3.datadoghq.com", "https://us3.datadoghq.com"},
	}
	for _, c := range cases {
		p := &Profile{Site: c.site}
		if p.APIURL() != c.api {
			t.Errorf("site %q: APIURL %q, want %q", c.site, p.APIURL(), c.api)
		}
		if p.AppURL() != c.app {
			t.Errorf("site %q: AppURL %q, want %q", c.site, p.AppURL(), c.app)
		}
	}
}

func TestParseSite(t *testing.T) {
	cases := map[string]string{
		"datadoghq.eu": "datadoghq.eu", "EU": "datadoghq.eu", "us5": "us5.datadoghq.com",
		"https://app.datadoghq.eu/dashboard/abc-123/x?from_ts=1": "datadoghq.eu",
		"https://us5.datadoghq.com/monitors/manage":              "us5.datadoghq.com",
		"acme.datadoghq.eu":                       "datadoghq.eu",
		"https://acme.us3.datadoghq.com/apm/home": "us3.datadoghq.com",
		"app.datadoghq.com":                       "datadoghq.com",
		"api.ap1.datadoghq.com":                   "ap1.datadoghq.com",
		"https://app.ddog-gov.com/logs":           "ddog-gov.com",
	}
	for in, want := range cases {
		if got, ok := ParseSite(in); !ok || got != want {
			t.Errorf("ParseSite(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "example.com", "https://grafana.acme.io"} {
		if got, ok := ParseSite(bad); ok {
			t.Errorf("ParseSite(%q) = %q, want no match", bad, got)
		}
	}
	if Mask("abcd") != "****" || Mask("0123456789abcdef") != "****cdef" {
		t.Error("Mask")
	}
}
