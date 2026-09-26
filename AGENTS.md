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
return structured data, and `mutates: true` on the ones that change Datadog.

### Read descriptions, not raw data

Raw series and log dumps are big and hard to reason about. These commands do
the reading and hand you facts to quote (all take `--md` and `--json`):

```sh
datadog read "<any Datadog link>"                       # dashboard, monitor, trace, logs, APM service, metric…
datadog metrics describe "p95:trace.http.request{service:api}" --since 6h --compare 1d
#   service:api: ~120ms; rose to ~480ms at 09:38 (×4); peak 1.2s at 09:52; last 450ms · vs 1d before: ×3.1
datadog logs patterns "service:api status:error" --since 1h --compare 1d
#   ≈4,321  35%  api  error  new  Payment declined for customer <*>: <*>
datadog trace <trace_id>                                # span tree, critical path, own time, N+1, errors, logs
datadog dashboards read <id | link | --file x.json>     # every widget described + its problems (JSON paths)
datadog monitors explain <id>                           # thresholds, who it wakes, what its data did
datadog coverage                                        # per service: monitors, SLOs, owner, gaps + fixes
datadog metrics tags <metric>                           # real tag keys/values and series count
```

Empty metric queries explain themselves when they can ("env:prod isn't a
value of env for trace.x (it has: production)").

### Designing dashboards and monitors

1. Learn the data: `metrics search`, `metrics tags`, `metrics describe --since 7d`.
2. Write the dashboard JSON (or `dashboards export <id> -o draft.json`).
3. Validate against real data: `datadog dashboards read --file draft.json --problems`.
4. The user previews it: `datadog ui --file draft.json` reloads on every save.
5. After a yes: `datadog dashboards create --file draft.json`.

### Anything else: the raw API

`datadog api <path>` calls any endpoint with the profile's keys (like
`gh api`): `-q k=v` query params, `-F k=v` typed JSON fields (`a.b=1` nests),
`--input file`. GETs and searches run straight away; other methods ask to
confirm (or `--yes`), and read-only profiles refuse them.

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
datadog logs aggregate "service:api" --groupby status --json
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
- The client retries 429 (waiting for `X-RateLimit-Reset`) and 5xx
  automatically — do not implement retries on top. Log search is limited to
  ~3 calls per 10 s: prefer `logs patterns` / `logs aggregate` to paging.
- Read-only profiles (`read_only: true`, or `DATADOG_READ_ONLY=1`) refuse
  every mutating command.

### Mutating commands — ask before running

`monitors mute/unmute/create/edit/import/delete`, `hosts mute/unmute`,
`downtimes schedule/cancel`, `incidents create/update`, `events post`,
`deploy`, `dashboards create/clone/import/delete`, `batch mute/unmute`,
`synthetics trigger`, `tags add/set/rm`, `integrations … set/clear`, and
`api` with any method but GET. `datadog schema` marks them `mutates: true`.
Everything else is read-only.

## Developing this repo

- Go 1.25. Commands in `cmd/` (Cobra, one file per area, each registers
  itself in `init()`; flags are package-level vars prefixed with the command
  name). `cmd/datadog/main.go` is the entry point.
- `datadog/` is a hand-rolled HTTP client (no official SDK). Types in
  `datadog/types*.go` model the *real* responses: check shapes against the
  live API, the docs have drifted before.
- `tui/` is `datadog ui` (Bubble Tea); `tui/report.go` reads a dashboard
  into text for `dashboards read`; `viz/` draws charts, sparklines and
  big numbers; `internal/series` describes a time series in words;
  `internal/logpattern` folds log messages into patterns; `internal/demo/` is the made-up org behind `ui --demo`;
  `internal/selfupdate/` is `update` and the update notice (kept identical
  across the ngavilan-dogfy CLIs); `internal/uiprefs/` remembers the chart
  style.
- Every list/show command implements the trio: TTY table, TSV (piped or
  `--plain`), `--json`. `cmd/logs.go` is the reference.
- Commands that work without credentials go in the list in `needsAuth`
  (`cmd/root.go`).
- The UI is tested against a fake Datadog (`tui/fakedd_test.go`): the
  harness types keys and checks that every frame is exactly the terminal's
  size. `internal/demo` has its own test that every monitor's state matches
  its data. Look at the real thing with `datadog ui --demo`.
- `make check` (vet + test + build) before committing. Smoke-test read-only
  commands against the real API when you have credentials; never run
  mutating commands against a real org to test.
- Commits follow Conventional Commits: they decide the next version and
  become the release notes (see CONTRIBUTING.md).
