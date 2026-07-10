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
  "summary": {"monitors_alerting": 2, "incidents_open": 0, "error_logs": 5,
              "audit_changes": 1, "hosts_up": 11, "hosts_total": 12, ...},
  "errors": {"pipelines": "CI Visibility not enabled ..."},
  "monitors_alerting": [...], "incidents_open": [...], "slos_at_risk": [...],
  "events": [...], "error_logs": [...], "audit_changes": [...],
  "security_signals": {...}, "pipelines": [...], "downtimes_active": [...],
  "host_totals": {"total_up": 11, "total_active": 12}
}
```

Read `summary` first to decide where to drill down. `errors` lists sections
that failed (missing permissions, product not enabled) — the rest of the
snapshot is still valid. Check `downtimes_active` before concluding an alert
is being ignored: it may be muted on purpose. `audit_changes` tells you who
changed configuration in the window — an alert right after a monitor edit or
a deleted downtime usually isn't a coincidence. Alerting monitors come
enriched with their `query` and `message` (top 10). Compare `hosts_up` vs
`hosts_total` to spot machines that dropped off.

### Investigate one service

When the question is about a specific service, `services context` returns
the full dossier in one call:

```sh
datadog services context api --since 2h --json
```

It includes: Service Catalog entry (team/tier/links), all monitors tagged
with the service, SLOs, **log volume by status** (`{"error": 664, "info":
12379, "warn": 833}` — is the error rate abnormal?), recent error logs,
error spans from APM (failing endpoints with durations and trace IDs),
events and active downtimes. Same `summary`/`errors` conventions as triage.

### Drill down

```sh
datadog logs "service:api status:error" --since 2h --json   # includes tags + attributes
datadog logs "service:api" --all --jsonl                    # paginate everything (cap 5000), NDJSON
datadog audit --since 24h --json                            # who changed what (config changes)
datadog monitors show <id> --json
datadog incidents show <id> --json
datadog correlate --around <spike-ts> --window 10m --json   # events/incidents/security near a timestamp
datadog metrics meta system.cpu.user --json                 # unit/type before interpreting numbers
datadog metrics query "avg:system.cpu.user{service:api}" --json
datadog logs-aggregate "service:api" --group-by status --json
```

Audit query tips: `-@asset.type:datadog_agent_configuration` drops periodic
agent-config noise; `@evt.name:Monitor` narrows to monitor changes;
`@usr.email:x@y.com` narrows to one author.

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
