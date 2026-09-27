# AGENTS.md

Guidance for AI agents: first for using the `datadog` CLI to investigate
production, then for working on this repository. Claude Code users get the
same playbooks as a skill with `datadog skill install`.

## Using the CLI

### Ground rules

1. **Read freely; change nothing without an explicit yes.** Every command
   that writes to Datadog is listed at the end of this section, and
   `datadog schema` marks it `mutates: true`.
2. **Prefer the commands that describe** (below) to raw series and log dumps.
   They return facts you can quote, at a fraction of the size.
3. **Use `--json` when you parse and `--md` when you reason**, and cut large
   JSON with `jq`. Keep windows and limits deliberate.
4. **Prove every claim** with the monitor id and state, the exact query, counts
   with their window, and timestamps with their timezone.

### Where to start

| You have | Run |
|---|---|
| A question ("why is checkout slow since 9?") | `datadog investigate <service> --since "today 09:00" --question "<their words>" --json` — see below |
| Words, not names ("payments", "the order webhook") | `datadog find <words> --json` — what they are in Datadog, best first |
| "Are our monitors any good?" | `datadog monitors review [--service <s>] --md` |
| A Datadog link | `datadog read "<link>" --md` — keeps the link's window, variables and query |
| A trace id | `datadog trace <id> --md` |
| A service name | `datadog services context <service> --since 1h --json` |
| A monitor id | `datadog monitors explain <id> --md` |
| Nothing yet | `datadog triage --json` — the whole picture in one call |

`triage` fetches alerting monitors (with query and message), open incidents,
SLOs at risk, error logs, events, audit-trail changes, security signals, CI
runs, active downtimes and host counts concurrently:

```json
{
  "window": {"from": "…", "to": "…"},
  "summary": {"monitors_alerting": 2, "incidents_open": 0, "error_logs": 5, "audit_changes": 1, "hosts_up": 11, "hosts_total": 12},
  "errors": {"pipelines": "CI Visibility not enabled …"},
  "monitors_alerting": [], "incidents_open": [], "slos_at_risk": [], "events": [],
  "error_logs": [], "audit_changes": [], "security_signals": {}, "pipelines": [], "downtimes_active": []
}
```

Read `summary` first. `errors` lists the sections that failed (a missing
permission, a product not enabled); the rest is still valid. Check
`downtimes_active` before concluding that an alert is being ignored, and
`audit_changes` for who changed what: an alert right after a monitor edit is
rarely a coincidence.

### From a question to a report

People describe problems in their own words and times: *checkout has been
failing since this morning*, *the order webhook was slow yesterday at six*.

1. **Map the words.** When the service isn't named exactly, `datadog find
   <words> --json` returns the services, endpoints, monitors, dashboards and
   metrics they match, each with the command that reads it. Take the best
   match; say which one you took.
2. **Investigate.** `datadog investigate <service> [--resource "<endpoint>"]
   --since "<their time>" --question "<their words>" --json`. Times can be
   spoken (`"today 09:00"`, `yesterday`, `"ayer a las 18:00"`, `"hace 2h"`) or
   centred: `--around "yesterday 18:00" --window 1h`. It compares with the day
   before (`--compare 7d` for weekly patterns) and returns `summary`,
   `status`, `onset`, `timeline`, `leads` (with `confidence`,
   `evidence_for`, `evidence_against` and a `verify` command), `findings`,
   `checked_normal`, `could_not_check`, `suggestions`, `next_steps` and
   numbered `references` — links to the exact Datadog view and its query.
3. **Verify before you repeat.** Run the top lead's `verify` command; read
   the references behind the claims you'll make (`datadog read <url>`);
   follow `next_steps` (a slow trace, the new log pattern) when the answer
   isn't settled. A lead is a hypothesis until its evidence holds.
4. **Report.** Answer the question first, in one or two sentences, then the
   timeline, the leads with their confidence and evidence, and what was ruled
   out. Cite references (`[3]`) for every number and keep their links. Give
   times with their timezone and counts with their window. Say what couldn't
   be checked instead of guessing. `datadog investigate … --md` is a ready
   report to start from.
5. **Propose improvements; apply none without a yes.** The investigation's
   `suggestions` (the monitor that was missing, the one that alerted late),
   `datadog monitors review --service <s>` (noisy, silent, stuck or loose
   monitors, each with the `monitors edit … --option/--threshold` command
   that fixes it), `datadog coverage --service <s>` (what nothing watches) and
   `datadog dashboards read <id> --problems` (broken or empty widgets). Show
   the commands; run them only when the user says so.

### Read descriptions, not raw data

```sh
datadog read "<any Datadog link>"
datadog metrics describe "p95:trace.http.request{service:api}" --compare 1d
#   service:api: ~120ms; rose to ~480ms at 09:38 (×4); last 450ms · vs 1d before: ×3.1
datadog logs patterns "service:api status:error" --compare 1d
#   4,321  35.0%  new  api  error  09:58  authorize timed out after <num>ms
datadog trace <trace_id>                                # span tree, critical path, own time, N+1, errors, logs
datadog dashboards read <id | link | --file x.json>     # every widget described, and its problems
datadog monitors explain <id>                           # thresholds, who it wakes, what its data did
datadog coverage                                        # per service: monitors, SLOs, owner, gaps and fixes
datadog metrics tags <metric>                           # the tag keys and values that exist
```

An empty metric query says why when it can (*env:prod isn't a value of env
for trace.x (it has: production)*); `metrics tags` shows the real values.
Traces can be partial when sampling dropped a parent span — the report says
so. `logs patterns` estimates counts from a sample when a window holds more
than a thousand logs.

### Drill down

```sh
datadog logs "service:api status:error" --since 2h --limit 50 --json
datadog logs aggregate "service:api status:error" --groupby @http.status_code --minutes 60 --json
datadog metrics query "avg:system.cpu.user{service:api} by {host}" --since 4h --json
datadog metrics meta <metric> --json          # unit and type before interpreting numbers
datadog correlate --around <time> --window 20m --json
datadog audit --since 24h --query "@evt.name:Monitor" --json
datadog incidents show <id> --json · datadog slos show <id> --json
```

In `audit` queries, `-@asset.type:datadog_agent_configuration` hides the
agents' periodic configuration noise and `@usr.email:<email>` narrows to one
person's changes.

### Designing dashboards and monitors

1. Learn the data: `metrics search`, `metrics tags`, `metrics describe --since 7d`.
   Pick thresholds from what the data does, not from a guess.
2. Write the dashboard JSON, or start from `dashboards export <id> -o draft.json`.
3. Validate it against real data: `datadog dashboards read --file draft.json --problems`.
4. Let the user watch it: `datadog ui --file draft.json` reloads on every save.
5. Only after a yes: `datadog dashboards create --file draft.json`.

### The raw API

`datadog api <path>` calls any endpoint with the profile's keys, like
`gh api`: `-q k=v` for query parameters, `-F k=v` for typed JSON fields
(`a.b=1` nests), `--input file` for a body. GETs, searches and queries run
immediately; any other request asks for confirmation (`--yes` skips it) and
is refused by read-only profiles.

### Conventions you can rely on

- stdout carries data only: JSON with `--json`, TSV with a header row when
  piped, tables in a terminal.
- Failures exit 1 and go to stderr; with `--json`, stderr also gets one
  parseable line, `{"error":"..."}`.
- `--from`, `--to` and `--around` take RFC3339, epoch seconds/milliseconds,
  or times as people say them, in local time (`yesterday 18:00`,
  `monday 9am`, `2h ago`, `ayer a las 18:00`, `hace 2h`); `--since` takes a
  duration (`30m`, `2h`, `1d`) or such a moment, `--window` a duration.
- The client retries 429 and 5xx, waiting for Datadog's rate-limit window.
  Don't add retries. Log search allows about three calls per ten seconds, and
  span searches five a minute: prefer `investigate`, which reads APM from
  metrics, to many `traces` calls.
- JSON field names are stable: renaming one is a breaking change.

### Commands that change Datadog — ask first

`monitors mute/unmute/create/edit/import/delete`, `batch mute/unmute`,
`downtimes schedule/cancel`, `hosts mute/unmute`,
`dashboards create/clone/import/delete`, `incidents create/update`,
`events post`, `deploy`, `synthetics trigger`, `tags add/set/rm`,
`integrations … set/clear`, and `api` requests that write. Profiles with
`read_only: true`, and sessions with `DATADOG_READ_ONLY=1`, refuse them all.

## Working on this repository

Read [ARCHITECTURE.md](ARCHITECTURE.md) first: it maps the code and explains
the decisions behind it. [CONTRIBUTING.md](CONTRIBUTING.md) covers the
workflow and releases. The rules that matter most:

- **`make check`** (vet, tests, build) before every commit.
- **Every command speaks the output contract**: styled output in a terminal,
  TSV when piped (or with `--plain`), JSON with `--json`. `cmd/logs.go` is
  the reference. JSON field names are public; don't rename them.
- **A new command that writes** goes in `mutatingCommands` (`cmd/readonly.go`)
  so read-only profiles refuse it and `schema` marks it. A command that works
  without credentials goes in `needsAuth` (`cmd/root.go`).
- **Types model the real API.** Check response shapes against a live
  organization; the public docs have drifted before.
- **Never touch a real organization's data in tests**, and never run a
  mutating command against one to try something. Screenshots come from
  `datadog ui --demo`; examples use made-up services and ids.
- **Keep the skill current.** When commands or flags change, update
  `cmd/skill_data/SKILL.md` and its copy in `.claude/skills/datadog/` (a test
  checks they match).
- **Shared files** — `internal/selfupdate/` and `cmd/keyinput.go` also live in
  the other ngavilan-dogfy CLIs; port changes to them.
- **Conventional commits**: the subject becomes a line in the release notes.
