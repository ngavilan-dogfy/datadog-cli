<h1 align="center">datadog</h1>

<p align="center"><strong>Datadog in your terminal, for you and for your agents.</strong><br>
Dashboards drawn the way Datadog draws them, monitors, logs and metrics in a fast terminal UI, and a CLI whose commands all speak JSON, so Claude Code (or any agent) can help you work through an incident.</p>

<p align="center">
  <a href="https://github.com/ngavilan-dogfy/datadog-cli/releases/latest"><img src="https://img.shields.io/github/v/release/ngavilan-dogfy/datadog-cli?style=flat-square&color=632ca6&label=release" alt="Release"></a>
  <a href="https://github.com/ngavilan-dogfy/datadog-cli/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/ngavilan-dogfy/datadog-cli/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=flat-square" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green?style=flat-square" alt="License"></a>
</p>

<p align="center">
  <img src="assets/dashboard.png" alt="A Datadog dashboard drawn in the terminal: query values, time series, stacked error bars, a top list and a change widget" width="860">
</p>

## Get started in two minutes

```bash
curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/datadog-cli/main/install.sh | sh
datadog setup
datadog ui
```

No keys at hand? `datadog ui --demo` opens a made-up web store whose checkout is having a bad afternoon, so you can look around first. Nothing leaves your machine.

The installer picks the build for your computer, checks it against the release's checksums, puts it in `~/.local/bin` (no sudo), offers to add that folder to your `PATH`, to teach Claude Code this CLI if you use it, and to run `datadog setup`. Run it again any time: it updates in place.

`datadog setup` walks you through three things and checks each one against Datadog before going on:

1. **Site**: pick yours from the list, or paste any link from your Datadog (a dashboard, a monitor) and the site is taken from it.
2. **API key**: setup opens the right page of Datadog and says what to click. Click **Copy** on a key and setup takes it from your clipboard: no pasting. A key from another site is caught, and setup tells you which site it is. Can't create keys? It tells you what to ask an admin for.
3. **Application key**: the same, on your personal settings, where anyone can create one. Setup says whose key it is and what it can read: monitors, dashboards, metrics, logs, incidents, SLOs.

Then two choices: whether this CLI may change things in Datadog (mute monitors, create dashboards) or only read, which is the safest for AI agents; and, if you use Claude Code, the `/datadog` skill.

Keys stay on your machine, in `~/.config/datadog-cli/profiles/` with owner-only permissions, and setup clears them from the clipboard once they're saved. `datadog doctor` checks everything again whenever something looks off, with a fix for each problem it finds.

In CI or containers there's nothing to set up: `DD_API_KEY`, `DD_APP_KEY` and `DD_SITE` are enough.

## The terminal UI

`datadog ui` has five tabs. Everything loads in parallel; Now, dashboards and monitors refresh every minute, and logs tail live with `L`.

| | |
|---|---|
| **Now**: *is anything wrong right now?* What's alerting, incidents, SLOs at risk, which services are logging errors and what changed recently, with repeated events folded into one line. | <img src="assets/now.png" width="420"> |
| **Dashboards**: your dashboards, laid out on Datadog's grid: time series, query values, top lists, change widgets, notes, groups, log streams and monitor summaries, with template variables (`v`) and one time range for everything (`t`). | <img src="assets/dashboard.png" width="420"> |
| **Zoom**: `⏎` on a widget fills the screen. Move the cursor with `←` `→` to read exact values at any moment; the queries are listed below. | <img src="assets/zoom.png" width="420"> |
| **Monitors**: grouped by state, P1 first. A monitor's detail charts what it watches against its thresholds, lists its groups and shows the message as it reads in the current state. `m` mutes for a while, `u` unmutes. | <img src="assets/monitor.png" width="420"> |
| **Logs**: volume by status over time, the matching lines, and a detail view with every attribute and the stack trace. `L` tails live, `s` narrows to the log's service. | <img src="assets/logs.png" width="420"> |
| **Metrics**: type a metric (`tab` completes), then change the space aggregation with `a` and the grouping with `b`. | <img src="assets/metrics.png" width="420"> |

`:` or `Ctrl+K` jumps anywhere: a dashboard, a monitor, a tab. `o` opens what you're looking at in Datadog. On a monitor or a log, `C` hands it to Claude Code with the commands to dig further.

**Charts** come in two styles: braille dots, which are finer, and blocks, which work with any font. `B` switches between them and the choice is remembered; `setup` shows you both so you can pick. If braille charts look faint or show boxes, your font has no braille: use blocks, or set `DATADOG_CHARTS=blocks`.

<details>
<summary><strong>Keyboard shortcuts</strong></summary>

| Key | Where | Action |
|---|---|---|
| `1`–`5`, `Tab` | everywhere | Now · Dashboards · Monitors · Logs · Metrics |
| `:` / `Ctrl+K` | everywhere | Jump to a dashboard, monitor or tab |
| `t` | everywhere | Time range |
| `B` | everywhere | Braille or block charts |
| `?` | everywhere | Every key for the current screen |
| `q` / `Esc` | everywhere | Back, or quit |
| `h` `j` `k` `l` / arrows | dashboard | Move between widgets |
| `⏎` | dashboard | Zoom a widget, fold a group |
| `v` | dashboard | Template variables |
| `c` / `+` `-` | dashboard | Fold every group / denser or roomier layout |
| `←` `→`, `H` `L` | zoomed chart | Move the cursor, jump further |
| `m` / `u` | monitors | Mute for a while / unmute |
| `/` | lists, logs, metrics | Filter, or edit the query |
| `L` / `s` / `n` | logs | Live tail / only this service / load more |
| `a` / `b` | metrics | Space aggregation / group by |
| `C` | monitor, log | Investigate with Claude Code |
| `o` | anywhere | Open in Datadog |

</details>

## For scripts and AI agents

Every command adapts to where its output goes:

| Output goes to | Format |
|---|---|
| a terminal | colors, tables and charts |
| a pipe | tab-separated values, header row first |
| `--json` (most commands) | structured JSON on stdout, nothing else |

With `--json`, a failure also writes one parseable line to stderr, `{"error":"..."}`, and exits with 1. `NO_COLOR` is honored.

Agents can't look at charts, so a set of commands does the reading for them and returns facts, at a fraction of the size of the raw data (each takes `--md` and `--json`):

```console
$ datadog metrics describe "p95:trace.http.request{service:checkout}" --since 6h --compare 1d
p95:trace.http.request{service:checkout} · May 19 04:00 → 10:00 CEST
  service:checkout: ~120ms; rose to ~480ms at 09:38 (×4); peak 1.2s at 09:52; last 450ms · vs 1d before: ×3.1

$ datadog logs patterns "service:checkout status:error" --since 1h --compare 1d
    ≈COUNT   SHARE  VS 1D   SERVICE   LEVEL  LAST    PATTERN
     4,321   35.0%  new     checkout  error  09:58   TimeoutError: payments.authorize timed out after <num>ms
       912    7.4%  ×1.1    checkout  error  09:57   Payment declined for customer <*>: card expired
```

- **`datadog read <link>`**: paste any Datadog link (dashboard, monitor, trace, logs search, APM service, metric, incident, SLO) and get what it shows, with the link's time window and template variables.
- **`datadog trace <id>`**: one request across services: the span tree as a waterfall, the critical path, each span's own time, repeated calls (N+1), the error that started it all, and the logs written during the request.
- **`datadog dashboards read <id>`**: every widget described (each line of a chart, the numbers, monitors in alert, log patterns) and what's wrong with it: failing queries, no data and why, always zero, unreadable charts, duplicates. `--file draft.json` checks a dashboard you're writing against real data, and `datadog ui --file draft.json` previews it, reloading every time the file changes.
- **`datadog monitors explain <id>`**: what a monitor evaluates, who it wakes, which groups aren't OK, and what its data did against the threshold, rolled up the way the monitor does it.
- **`datadog coverage`**: every service with traffic or error logs against the monitors, SLOs and owners that watch it, and the gaps, each with the monitor query that would close it.
- **`datadog metrics tags <metric>`**: the tag values that exist, so queries don't come back empty (`env:production`, not `env:prod`). When one does, the CLI says why.
- **`datadog api <path>`**: any endpoint of the API with your profile's keys, like `gh api`. Changes ask for confirmation.

Three more exist mostly for automation:

- **`datadog triage --json`**: one call, the whole picture. Alerting monitors (with their query and message), open incidents, SLOs at risk, error logs, events, audit-trail changes, security signals, CI pipelines, active downtimes and hosts up, fetched concurrently. Narrow it with `--since 15m`, `--service api --env prod` or `--around <time>`. A `summary` block comes first, and sections that failed are listed under `errors` without spoiling the rest.
- **`datadog services context <service> --json`**: everything about one service: catalog entry, monitors, SLOs, log volume by status, error logs, failing endpoints from APM, deploys and downtimes.
- **`datadog schema`**: every command and flag as JSON, and which ones support `--json`.

### Claude Code

```bash
datadog skill install
```

This installs the `/datadog` skill, so Claude Code knows how to investigate with this CLI: start from `triage`, go down to a service, then to its logs and metrics, and ask before changing anything. The skill ships inside the binary and is refreshed when you update. [AGENTS.md](AGENTS.md) has the full guide for agents.

## Commands

| Area | Commands |
|---|---|
| What's going on | `triage`, `status`, `last`, `correlate`, `read`, `coverage` |
| Monitors | `monitors` (`show`, `explain`, `search`, `mute`, `unmute`, `create`, `edit`, `delete`, `export`, `import`), `batch mute/unmute` |
| Logs | `logs` (`--all`, `--jsonl`), `logs tail`, `logs aggregate`, `logs patterns` |
| Metrics | `metrics search/query/describe/tags/meta`, `tags` |
| Dashboards | `dashboards` (`get`, `read`, `open`, `create`, `clone`, `export`, `import`, `delete`, `lint`) |
| Incidents and SLOs | `incidents` (`show`, `create`, `update`), `slos` |
| Traces and RUM | `trace`, `traces`, `rum` |
| Infrastructure | `hosts` (`mute`, `unmute`), `services` (`show`, `context`), `integrations` |
| CI/CD | `pipelines`, `deploy`, `synthetics` (`show`, `trigger`) |
| Security | `security`, `audit` |
| Other | `events` (`post`), `downtimes` (`schedule`, `cancel`), `notebooks`, `usage`, `open`, `ui`, `api` |
| This CLI | `setup`, `doctor`, `update`, `version`, `skill`, `whoami`, `profile`, `config`, `logout`, `schema`, `completion` |

`datadog <command> --help` explains each one, with examples.

## Updating

```bash
datadog update
```

It shows what's new, downloads the release for your machine, checks its checksum and that the new binary runs, and only then replaces the old one. Your settings aren't touched. When a new version is out, commands you run in a terminal mention it after their output (the check runs in the background, at most once a day); `DATADOG_NO_UPDATE_NOTIFIER=1` turns that off.

<details>
<summary><strong>Other ways to install</strong></summary>

- **A specific version or folder**: `curl -fsSL …/install.sh | DATADOG_VERSION=v1.2.0 DATADOG_INSTALL_DIR=~/bin sh` (`DATADOG_NO_SETUP=1`, `DATADOG_NO_SKILL=1` and `DATADOG_NO_MODIFY_PATH=1` keep it from asking).
- **With Go**: `go install github.com/ngavilan-dogfy/datadog-cli/cmd/datadog@latest`
- **By hand**: download `datadog-<os>-<arch>` from the [latest release](https://github.com/ngavilan-dogfy/datadog-cli/releases/latest), check it against `checksums.txt`, make it executable and put it in your `PATH`. Windows: `datadog-windows-amd64.exe`.
- **From source**: `make install` builds and copies to `~/.local/bin`.

</details>

## Contributing

`make check` runs vet, the tests and a build. Releases are cut automatically from [conventional commits](https://www.conventionalcommits.org) on `main`; [CONTRIBUTING.md](CONTRIBUTING.md) explains how.

## License

[MIT](LICENSE)
