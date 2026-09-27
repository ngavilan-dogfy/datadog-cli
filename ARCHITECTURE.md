# Architecture

This document is a map: where things live, how a request flows through the
code, and the decisions that shaped it. It describes what is true of the code
today; if you change one of the invariants below, change this file in the
same commit.

## Bird's-eye view

`datadog` is one Go binary with two faces over one HTTP client:

```
 terminal, scripts, agents
              │
   ┌──────────▼───────────┐                 ┌──────────────────────┐
   │ cmd/                 │                 │ tui/                 │
   │ cobra commands       │── datadog ui ──▶│ Bubble Tea app       │
   │ table · TSV · --json │                 │ through tui.API      │
   └──────────────────────┘                 └──────────────────────┘
              │                                        │
   ┌──────────▼────────────────────────────────────────▼───────────┐
   │ datadog/   hand-rolled REST client                            │ ──▶ api.<site>
   └───────────────────────────────────────────────────────────────┘
   internal/demo implements tui.API with a made-up org
```

The commands and the UI share the client and the rendering primitives, but
not their control flow: commands are short-lived and print once; the UI is a
long-lived model that fetches concurrently and repaints.

## Code map

| Path | What lives there |
|---|---|
| `cmd/datadog/main.go` | Entry point: `cmd.Execute()`. |
| `cmd/` | One file per command area; every command registers itself in `init()`. `root.go` loads the profile, builds the client, enforces read-only mode and offers setup on first run. `output.go` has the TTY/TSV/JSON helpers. |
| `cmd/` — reading commands | `read.go` (link dispatch), `trace.go`, `metrics_describe.go`, `metrics_tags.go`, `logs_patterns.go`, `dashboards_read.go`, `monitors_explain.go`, `coverage.go`. Each fetches with the client and hands the data to a pure package for analysis. |
| `cmd/` — questions | `find.go` (words → what exists), `investigate.go` (gathering), `investigate_analysis.go` (facts → verdicts, onset, leads), `investigate_report.go` (text and markdown), `monitors_review.go` (monitors against what they did), `refs.go` (numbered references and the links behind them), `when.go` and `window.go` (times as people say them, shared window flags), `events_search.go`. |
| `cmd/` — onboarding | `setup.go` (the guided flow), `keyinput.go` (the secret prompt that watches the clipboard), `wizard_ui.go` (step headers, status lines, spinners), `doctor.go`, `update.go`, `skill.go` (the embedded Claude Code skill). |
| `datadog/` | The HTTP client: `client.go` (auth, retries, rate limits), one `client_*.go` per API area, `types*.go` modeled on real responses, `client_raw.go` for `datadog api`. |
| `config/` | Profiles on disk, environment overrides, read-only mode, site parsing. |
| `tui/` | `datadog ui`. `app.go` is the root model (tabs, a stack of screens per tab, modals, toasts). `dashmodel.go` parses dashboards and lays widgets on the grid; `widgetdata.go` fetches what each widget type needs; `widgetview.go` renders it; `report.go` turns a whole dashboard into text for `dashboards read`. |
| `viz/` | Charts (braille and block), sparklines, big numbers, unit-aware number formatting. No I/O. |
| `internal/series` | A time series → a summary: level, level shifts, spikes, gaps, trend, and one sentence describing them. |
| `internal/logpattern` | Log messages → templates: variable parts replaced by placeholders, near-identical templates folded together. |
| `internal/demo` | The made-up organization behind `datadog ui --demo`: deterministic signals with one incident, monitors whose state follows their data. |
| `internal/selfupdate` | `datadog update` and the daily update notice. Shared, file for file, with the other ngavilan-dogfy CLIs. |
| `internal/uiprefs` | Remembered UI preferences (chart style). |
| `ui/` | Lip Gloss styles for command output. |
| `scripts/`, `install.sh`, `.github/workflows/` | Release pipeline and installer (see [CONTRIBUTING.md](CONTRIBUTING.md)). |

## How a command runs

1. `cobra` parses flags. `PersistentPreRunE` in `root.go` resolves the
   profile — the file on disk, overridden by `DD_API_KEY`/`DD_APP_KEY`/
   `DD_SITE`, or those variables alone — and builds `client`.
2. If the command is listed in `readonly.go` as mutating and the profile is
   read-only, it fails here, before any request is made.
3. The command calls the client. `client.do()` signs the request, retries
   429s (waiting for `X-RateLimit-Reset`) and 5xx with backoff, and turns
   Datadog's error bodies into one readable line.
4. The command renders for its destination: a styled table in a terminal,
   TSV when piped, JSON with `--json`. Nothing but data goes to stdout.
5. `Execute()` prints a failure to stderr (plus `{"error": …}` under
   `--json`), shows the update notice if one is due, and exits 1 on error.

## The reading commands

These commands exist because an agent can't look at a chart. Each one is a
thin fetch in `cmd/` over a pure, tested analysis:

- **`internal/series`** finds level shifts by binary segmentation on prefix
  sums, keeping a split only when the difference stands well above the
  noise (estimated from neighbouring differences, which shifts barely move),
  lasts, and is a real part of the series' range. A steady ramp would split
  into "steps"; a correlation check turns those back into a trend. Spikes
  must be far from their neighbourhood and not sit next to a shift.
- **`internal/logpattern`** normalizes ids, numbers, emails, times, URLs and
  quoted values, then folds templates of the same length that differ in at
  most a quarter of their words, keeping the shared prefix and suffix of each
  differing word (`orderId=<*>,`).
- **`logs patterns`** can't read every log: Datadog allows about three log
  searches per ten seconds. It asks for the counts per interval (about sixty
  buckets) in one aggregate call, reads at most two pages split on a bucket
  boundary so each half's total is exact, and weighs each sampled log by how
  many it stands for.
- **`trace`** rebuilds the tree from indexed spans; with sampling, parents can
  be missing, so orphans become roots and the report says it's partial. The
  critical path walks back from the end of each span through the child that
  finished last.
- **`dashboards read`** reuses the UI's widget fetching (`tui/report.go`), so
  a dashboard reads exactly as it draws.
- **`monitors explain`** rolls every metric term up over the monitor's own
  window and aggregation (`sum(last_10m)` → `.rollup(sum, 600)`), so the
  points compared with the threshold are the values the monitor saw.

Text output and `--json` come from the same structs: the text is a view of
the numbers, never a separate computation.

## Questions: find, investigate, review

These commands take what a person says and do the correlating. Each is split
into gathering (reads, concurrent, tolerant of failures) and judging (pure
functions over a facts struct), so every rule is tested on synthetic facts.

- **`find`** fans out over what can hold a name — services and operations
  and endpoints (one spans aggregation each), log services, the catalog,
  monitors, dashboards, SLOs, incidents, metrics, hosts — and scores each
  candidate against the question's words: exact, stem, prefix, then edit
  distance, so plurals and typos match.
- **`investigate`** gathers facts (`invFacts`) and turns them into an
  `investigation`. APM comes from trace metrics, not span searches: spans
  allow five searches a minute, metrics six hundred per ten seconds, so
  every APM question is a comma-separated batch of metric queries (the window
  and the baseline, per endpoint, host, version and dependency), one call
  each, answered by `query_index`. It runs in waves: resolve the target,
  gather what doesn't depend on anything (series, logs, monitors, SLOs, the
  catalog, org events), then the follow-ups that need the first answers
  (the process's vitals on the busiest hosts, an example failing request, a
  slow one). `DATADOG_DEBUG_TIMING=1` prints each call's duration to stderr.
- **The analysis** judges each signal against the baseline with absolute
  floors (a quiet service's one slow request isn't a spike), finds the onset,
  and ranks leads by how close in time and how strong their evidence is: a
  change just before the onset; everything spiking at once (the process or
  its host, not a dependency); a heavy job at those moments; one dependency
  or endpoint; a new error pattern, or an old one that grew; load. Each lead
  carries evidence for and against and a command to verify it.
- **References** (`refs.go`): every claim cites numbered links to the exact
  view in Datadog with the same window, and the query behind it. A test
  checks that every kind of link round-trips through `datadog read`.
- **`monitors review`** reads every monitor with its transitions (the events
  API, grouped per monitor and per group, with the flips a monitor makes in
  one second when it's created collapsed into the change they add up to) and
  its data rolled up like the monitor evaluates it. The rules propose fixes
  as `monitors edit --option/--threshold` commands, which change one setting
  and keep the rest.

## Decisions

**A hand-rolled client instead of Datadog's generated SDK.** The official Go
client is large and generated for every endpoint; this CLI uses a few dozen.
Hand-written types model only the fields we read, keep the binary small and
the build fast, and are checked against live responses — the public docs have
drifted from the API more than once. The cost is tracking API changes
ourselves.

**The UI depends on an interface.** `tui.API` is implemented by
`*datadog.Client`, by `internal/demo`, and by an in-memory fake in the tests.
The demo exists so the UI can be developed, tested and screenshotted without
a real organization; its monitors derive their state from the same signals
their widgets draw, and a test checks they agree at every hour of the day.

**One output contract.** Every command renders through the same helpers,
so the rules in the README's *Scripting* section hold everywhere. JSON field
names are a public interface: changing one is a breaking change.

**Read-only is enforced once.** Mutating commands are listed in one place
(`readonly.go`), checked before any request, and exposed through
`datadog schema` as `mutates: true`. `datadog api` classifies requests by
method and path (searches and queries are reads).

**Setup validates before it saves.** Every step checks its answer against
Datadog; nothing is written until the end, and profiles are written
atomically (temp file, `chmod 0600`, rename). The network-facing steps are
function variables, so tests and the `e2e` build can replace them.

**Updates replace, they don't overwrite.** `internal/selfupdate` downloads to
a temp file, verifies the SHA-256 from `checksums.txt`, runs the new binary,
and renames it over the old one (copy + `mv` on macOS, where rewriting a
signed binary in place gets it killed).

**The terminal decides the colors.** The UI paints with the terminal's own
ANSI palette, so it follows the user's theme; only neutral surfaces are
blended from the real background and foreground, queried once at startup.

## Invariants

- Every frame the UI renders is exactly the terminal's width and height;
  widths are measured with `x/ansi`, never `len()`. The UI tests check it.
- `internal/series`, `internal/logpattern` and `viz` do no I/O.
- Nothing in the repository contains data from a real organization:
  screenshots come from `--demo`, examples use made-up services and ids.
- Tests never call a real Datadog, and never run a mutating command against
  one.

## Testing

| Layer | How |
|---|---|
| Analysis (`series`, `logpattern`, trace assembly, coverage gaps, link parsing) | Table and scenario tests on synthetic data. |
| Questions (`find`, `investigate`, `monitors review`, spoken times) | Scenario tests on synthetic facts: a deploy then errors, latency spikes, a traffic drop, a quiet day; flapping, stuck, silent and loose monitors. |
| UI | `tui/fakedd_test.go`: a harness types keys against a fake Datadog and asserts on each frame. |
| Demo | `internal/demo`: monitor states match their data across a day. |
| Setup | Key prompt model tests with a fake clipboard; the `e2e` build drives the real binary for recordings. |
| Release | CI builds and vets on Linux and macOS, shellchecks the installer and renders release notes. |
