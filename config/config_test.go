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
	if strings.Contains(got, "12345678") || !strings.HasSuffix(got, "****") {
		t.Errorf("api_key not masked: %q", got)
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
