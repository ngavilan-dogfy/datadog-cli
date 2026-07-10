package cmd

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDateEpochSeconds(t *testing.T) {
	got, err := parseDate("1747632000")
	if err != nil {
		t.Fatalf("epoch seconds: %v", err)
	}
	want := time.Unix(1747632000, 0)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateEpochMillis(t *testing.T) {
	got, err := parseDate("1747632000000")
	if err != nil {
		t.Fatalf("epoch millis: %v", err)
	}
	want := time.UnixMilli(1747632000000)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateRFC3339(t *testing.T) {
	got, err := parseDate("2026-05-19T04:54:00Z")
	if err != nil {
		t.Fatalf("rfc3339: %v", err)
	}
	if got.UTC().Hour() != 4 || got.UTC().Minute() != 54 {
		t.Errorf("wrong time: %v", got)
	}
}

func TestParseDateDateOnly(t *testing.T) {
	got, err := parseDate("2026-05-19")
	if err != nil {
		t.Fatalf("date only: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 5 || got.Day() != 19 {
		t.Errorf("wrong date: %v", got)
	}
}

func TestParseDateInvalid(t *testing.T) {
	if _, err := parseDate("not-a-date"); err == nil {
		t.Error("expected error for invalid input")
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"30m": 30 * time.Minute,
		"2h":  2 * time.Hour,
		"1d":  24 * time.Hour,
		"3d":  72 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseDuration(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
	if _, err := parseDuration("nope"); err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestHasTag(t *testing.T) {
	tags := []string{"service:api", "env:prod"}
	if !hasTag(tags, "service:api") {
		t.Error("expected match")
	}
	if hasTag(tags, "service:web") {
		t.Error("unexpected match")
	}
}

func TestBuildSchema(t *testing.T) {
	s := buildSchema(rootCmd)
	if len(s.Subcommands) < 20 {
		t.Errorf("expected 20+ top-level commands, got %d", len(s.Subcommands))
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("schema not marshalable: %v", err)
	}
	var logs *cmdSchema
	for i := range s.Subcommands {
		if s.Subcommands[i].Name == "datadog logs" {
			logs = &s.Subcommands[i]
		}
	}
	if logs == nil {
		t.Fatal("logs command missing from schema")
	}
	if !logs.JSONOutput {
		t.Error("logs should report json_output=true")
	}
	if len(data) == 0 {
		t.Error("empty schema")
	}
}

func TestWantsJSON(t *testing.T) {
	// wantsJSON reads os.Args; just ensure it doesn't panic and returns a bool.
	_ = wantsJSON()
}
