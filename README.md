# datadog-cli

Fast, scriptable command-line tool for Datadog. Built in Go with Cobra + Bubbletea.

```
datadog triage                       # everything going on right now, one call
datadog status                       # triggered monitors, open incidents, SLOs at risk
datadog logs "service:api status:error" --since 2h
datadog monitors --state Alert
datadog correlate --around 2026-05-19T04:54:00Z --window 10m
```

## Install

```sh
make install        # builds with version info and copies to /usr/local/bin
# or
go build -o datadog-cli .
```

## Authentication

```sh
datadog login       # interactive: API key + App key + site
datadog whoami      # validate credentials
```

Environment variables `DD_API_KEY`, `DD_APP_KEY` and `DD_SITE` override the
active profile. Profiles live in `~/.config/datadog-cli/profiles/` and are
managed with `datadog profile create/ls/use/delete/show`.

## Output conventions

Every read command adapts to its consumer:

| Consumer            | Format                                        |
|---------------------|-----------------------------------------------|
| Terminal (TTY)      | colored, human-friendly tables                |
| Piped               | tab-separated values (TSV), header row first  |
| `--json`            | structured JSON                               |
| `--plain`           | force TSV even on a TTY                       |

On failure with `--json`, a single parseable line `{"error":"..."}` is also
written to stderr. Exit code is 0 on success, 1 on any error.
`NO_COLOR` is honored for terminal output.

## For scripts and AI agents

Two commands are designed specifically for automation:

- **`datadog schema [command] [--full]`** — dumps the whole command tree
  (commands, flags, types, defaults, which support `--json`) as JSON.
  One call to discover everything the CLI can do.
- **`datadog triage [--since 1h] [--service X] [--env Y] --json`** — one-shot
  context snapshot: alerting monitors, open incidents, SLOs at risk, events,
  error logs, security signals, CI pipelines and active downtimes, fetched
  concurrently and returned as a single JSON document with a `summary` block
  and per-section `errors`.

See [AGENTS.md](AGENTS.md) for the full agent guide and recipes.

## Command overview

| Area        | Commands                                                        |
|-------------|-----------------------------------------------------------------|
| Snapshot    | `triage`, `status`, `last`, `correlate`, `watch`                |
| Monitors    | `monitors [show/mute/unmute/search/create/update/delete]`, `batch mute/unmute` |
| Logs        | `logs`, `logs tail`, `logs-aggregate`                           |
| Metrics     | `metrics search/query`, `tags`                                  |
| Dashboards  | `dashboards [show/open/create/update/delete/lint]`              |
| Incidents   | `incidents [show/create/update]`                                |
| SLOs        | `slos [show]`                                                   |
| Traces/RUM  | `traces`, `rum`, `profile`                                      |
| Infra       | `hosts [mute/unmute]`, `services`, `integrations`               |
| CI/CD       | `pipelines`, `deploy`, `synthetics`                             |
| Security    | `security`                                                      |
| Other       | `events`, `downtimes`, `notebooks`, `usage`, `open`, `ui` (TUI) |
| Meta        | `schema`, `whoami`, `login`, `logout`, `config`, `profile`, `completion` |

Run `datadog <command> --help` or `datadog schema <command> --full` for details.

## Development

```sh
make build     # build with version ldflags
make test      # go test ./...
make vet       # go vet ./...
make check     # vet + test + build
```
