//go:build e2e

package cmd

import (
	"errors"
	"os"
	"time"

	"github.com/ngavilan-dogfy/datadog-cli/config"
	"github.com/ngavilan-dogfy/datadog-cli/datadog"
)

// Hooks for driving the real binary end to end (go build -tags e2e), used
// to record the setup for the README without a real Datadog: with
// DATADOG_E2E_API_KEY and DATADOG_E2E_APP_KEY set, setup accepts exactly
// those made-up keys on datadoghq.eu, signs in as a made-up user, and logs
// the pages it would open to DATADOG_E2E_LOG instead of opening a browser.
// Never in releases.
func init() {
	api, app := os.Getenv("DATADOG_E2E_API_KEY"), os.Getenv("DATADOG_E2E_APP_KEY")
	if api == "" || app == "" {
		return
	}
	hostname = func() (string, error) { return "ana-laptop", nil }
	siteProbe = func(string) error { time.Sleep(400 * time.Millisecond); return nil }
	validateAPIKey = func(site, key string) (bool, error) {
		time.Sleep(600 * time.Millisecond)
		return site == "datadoghq.eu" && key == api, nil
	}
	whoAmI = func(p *config.Profile) (identity, error) {
		time.Sleep(600 * time.Millisecond)
		if p.AppKey != app {
			return identity{}, errors.New("forbidden (403)")
		}
		return identity{name: "Ana García", handle: "ana@acme.example", org: "Acme Shop"}, nil
	}
	readAccess = func(*datadog.Client) []check {
		time.Sleep(500 * time.Millisecond)
		var out []check
		for _, n := range []string{"monitors", "dashboards", "metrics", "logs", "incidents", "SLOs"} {
			out = append(out, check{Section: "Datadog", Name: n, Status: "ok", Detail: "Can read " + n})
		}
		return out
	}
	openURL = func(u string) {
		if f, err := os.OpenFile(os.Getenv("DATADOG_E2E_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.WriteString("open " + u + "\n")
			f.Close()
		}
	}
}
