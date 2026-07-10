# Agent guide — datadog-cli

Guidance for AI agents (and scripts) using this CLI to gather Datadog context,
plus notes for agents developing this repository.

## Using the CLI

### Discover capabilities

```sh
datadog schema                 # full command tree as JSON: commands, flags, defaults
datadog schema logs --full     # one subtree, with full help text and examples
```

Every entry reports `json_output: true/false` so you know which commands can
return structured data.

### Gather context (start here)

`datadog triage` is the one-call context snapshot. Prefer it over stitching
together multiple commands:

```sh
datadog triage --json                          # last hour, whole org
datadog triage --since 15m --json              # tighter window
datadog triage --service api --env prod --json # scoped to a service
datadog triage --around 1747632000 --window 20m --json  # centered on an alert timestamp
```

The JSON has this shape:

```json
{
  "generated_at": "...", "site": "...",
  "window": {"from": "...", "to": "..."},
  "summary": {"monitors_alerting": 2, "incidents_open": 0, "error_logs": 5, ...},
  "errors": {"pipelines": "CI Visibility not enabled ..."},
  "monitors_alerting": [...], "incidents_open": [...], "slos_at_risk": [...],
  "events": [...], "error_logs": [...], "security_signals": {...},
  "pipelines": [...], "downtimes_active": [...]
}
```

Read `summary` first to decide where to drill down. `errors` lists sections
that failed (missing permissions, product not enabled) — the rest of the
snapshot is still valid. Check `downtimes_active` before concluding an alert
is being ignored: it may be muted on purpose.

### Drill down

```sh
datadog logs "service:api status:error" --since 2h --json   # includes tags + attributes
datadog monitors show <id> --json
datadog incidents show <id> --json
datadog correlate --around <spike-ts> --window 10m --json   # events/incidents/security near a timestamp
datadog metrics query "avg:system.cpu.user{service:api}" --json
datadog logs-aggregate "service:api" --group-by status --json
```

### Conventions you can rely on

- `--json` → structured JSON on stdout, nothing else on stdout.
- On error with `--json`, stderr carries one parseable line: `{"error":"..."}`.
  Exit code 0 = success, 1 = any failure.
- Piped output without `--json` is TSV with a header row.
- Timestamps: RFC3339 (`2026-05-19T04:54:00Z`) or epoch seconds/millis both
  accepted by `--from/--to/--around`.
- Durations: `30m`, `2h`, `1d` accepted by `--since`, `--window`, `--duration`.
- The client retries 429 (honoring `Retry-After`) and 5xx automatically —
  do not implement retries on top.

### Mutating commands — ask before running

`monitors mute/unmute/create/update/delete`, `hosts mute/unmute`,
`downtimes schedule/cancel`, `incidents create/update`, `events post`,
`dashboards create/update/delete`, `batch mute/unmute`, `synthetics trigger`.
Everything else is read-only.

## Developing this repo

- Go 1.25, Cobra for commands (`cmd/`), raw HTTP client in `datadog/`
  (no official SDK), Bubbletea TUI in `tui/`, lipgloss styles in `ui/`.
- One file per command area in `cmd/`; each file registers itself in `init()`
  via `rootCmd.AddCommand`. Flags are package-level vars prefixed with the
  command name.
- Every list/show command implements the trio: TTY table, TSV (piped or
  `--plain`), `--json`. Follow `cmd/logs.go` as the reference pattern.
- API types live in `datadog/types*.go` and model real API responses —
  verify shapes against the live API, not just the docs (metadata shapes
  have diverged from docs before).
- New commands that don't need credentials must be added to the `noAuth`
  list in `cmd/root.go`.
- Check: `make check` (vet + test + build). Smoke-test read-only commands
  against the real API when credentials are configured.
