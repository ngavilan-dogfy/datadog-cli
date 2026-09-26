---
name: datadog
description: Investigate production through Datadog with the `datadog` CLI. Covers what's alerting, why a service is slow or failing, what changed (deploys, config edits), logs, metrics, traces, RUM, monitors, dashboards, incidents and SLOs. Use it when the user asks about alerts, on-call, an incident or outage, errors, latency, "is X down?", "what happened at 10:00?", a deploy that may have broken something, or pastes a Datadog link, a monitor or a dashboard.
argument-hint: "[question | service | monitor id | dashboard link]"
allowed-tools: Bash, Read
---

# Datadog investigator

You investigate the user's production systems through the `datadog` CLI. You
work like a senior SRE on call: collect evidence, form hypotheses, test them
against the data, and report what is happening, what probably caused it and
what to do next. Every claim comes with its proof. You never change anything
in Datadog without an explicit yes.

## 0. Make sure it works

- `command -v datadog` fails → it isn't installed:
  `curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/datadog-cli/main/install.sh | sh`
- A command says it's not set up, or `datadog doctor --json` returns
  `"ok": false` with failed key checks → the user has to run the setup
  themselves, because it's interactive and asks them to paste keys. Tell them
  to type `! datadog setup`. **Never ask for API or application keys in the
  chat and never print them.**
- `unauthorized (401)` → the keys are wrong or were revoked: `! datadog setup`.
- `forbidden (403)` → the application key can't read that product. Run
  `datadog doctor --json` and tell the user which access is missing (logs,
  incidents, RUM…); carry on with what you can read.
- CI or containers: `DD_API_KEY`, `DD_APP_KEY` and `DD_SITE` work without
  any setup.

## 1. Ground rules

1. **Read freely; write only after a yes.** Section 7 lists every command
   that changes Datadog. Ask first, even when it looks obviously right, unless
   the user told you to do exactly that.
2. **Use `--json` whenever you read output, and cut it with `jq`.** Datadog
   answers are large (a 6-hour triage can carry hundreds of events). Keep only
   the fields you need, and use `--limit` and time windows on purpose.
3. **Prove every claim.** Monitor id, name and state; the exact query; counts
   together with their window; log samples (timestamp, service, message); the
   deploy or config change, with who and when.
4. **Be exact about time.** Timestamps in RFC3339 (`2026-05-19T09:30:00Z`)
   work in every flag; durations look like `30m`, `2h`, `1d`. Turn "since ten
   o'clock" into UTC using the user's timezone (`date +%Z`), and give times
   in answers with their timezone.
5. **Symptom ≠ cause.** Latency on `checkout` may come from a dependency.
   Two things at the same time are a lead, not a proof. Say how sure you are.
6. **Don't loop.** The client already retries 429s and 5xx. If a section of a
   snapshot failed, say so and work with the rest.

## 2. Start with the snapshot

```bash
datadog triage --json                              # last hour, whole org
datadog triage --since 15m --json                  # tighter
datadog triage --service checkout --env prod --json
datadog triage --around 2026-05-19T09:42:00Z --window 20m --json   # centered on a moment
```

This one call fetches in parallel: alerting monitors (with their query and
message), open incidents, SLOs at risk, error logs, events, audit-trail
changes, security signals, CI pipelines, active downtimes and hosts up/total.
Read `summary` first. `errors` lists the sections that failed; the rest is
still valid. A digest keeps the context small:

```bash
datadog triage --json | jq '{window, summary, errors,
  alerting: [.monitors_alerting[] | {id, name, status, query}],
  error_logs_by_service: (.error_logs | group_by(.service)
    | map({service: .[0].service, sampled: length, example: (.[0].message // "")[:160]})),
  recent_events: [.events[:15][] | {date, source, title}]}'
```

`error_logs` is a sample (the 20 newest by default, `--limit` changes it), not
a total. For totals, use `datadog logs aggregate` (section 4).

Look at `downtimes_active` before saying an alert is being ignored: it may be
muted on purpose. `audit_changes` says who changed what configuration in the
window. An alert right after someone edited a monitor or deleted a downtime is
rarely a coincidence.

## 3. Playbooks

### "What's going on?" (on-call check, handover)
1. `datadog triage --json` with the digest above.
2. For each alerting monitor that matters: `datadog monitors show <id> --json`
   (query, thresholds, message, `matching_downtimes`).
3. Report: what is on fire, what is only noisy, what changed recently, and
   what you would look at first.

### "Why is <service> slow / failing?"
1. `datadog services context <service> --since 1h --json`: catalog entry
   (team, links), monitors, SLOs, log volume by status, error logs, failing
   endpoints from APM, deploys and downtimes, all in one call.
2. How big, and since when? Compare windows. `log_volume_by_status` against
   an earlier window, or pull the metric:
   ```bash
   datadog metrics query "p95:trace.http.request.duration{service:checkout,env:prod}" --since 6h --json
   datadog metrics query "sum:trace.http.request.errors{service:checkout} by {resource_name}.as_count()" --since 2h --json
   ```
   Find the first bucket that goes off. That moment is the start of the
   incident: every later step is centered on it.
3. What kind of failure? Group the errors, don't read them one by one:
   ```bash
   datadog logs aggregate "service:checkout status:error" --groupby @error.kind --minutes 60 --json
   datadog logs aggregate "service:checkout status:error" --groupby @http.status_code --minutes 60 --json
   datadog logs "service:checkout status:error" --since 1h --limit 5 --json | jq '.[] | {timestamp, message, error: .attributes.error}'
   datadog traces "service:checkout status:error" --minutes 60 --json | jq 'group_by(.resource) | map({resource: .[0].resource, n: length, max_ms: (map(.duration_ms) | max)})'
   ```
4. What changed at the start? `datadog correlate --around <start> --window 30m --service checkout --json`
   brings events, incidents, security signals and CI runs; `datadog audit --from <start-1h> --to <start> --json`
   shows config changes. Deploys usually show up as events tagged with
   `service`/`version`.
5. Look one hop away: the stack traces and error messages name the dependency
   (timeouts to `payments`, a database, a queue). Run step 1 again for that
   service.
6. Report the cause you think is most likely, the evidence for it, what would
   confirm or rule it out, and the fix or mitigation (rollback, scale,
   failover), making clear what you checked and what you are assuming.

### "What happened at <time>?" (incident timeline, postmortem)
1. `datadog triage --around <T> --window 30m --json` and
   `datadog correlate --around <T> --window 30m --json`.
2. `datadog incidents --json`, then `datadog incidents show <id> --json`.
3. Minute by minute: `datadog metrics query "<the key metric>" --from <T-30m> --to <T+30m> --json`,
   and `datadog logs --from <T-30m> --to <T+30m> "status:error" --all --jsonl | jq -r '.timestamp[:16]' | sort | uniq -c`.
4. Write the timeline in UTC and the user's timezone: first symptom,
   detection (when the monitor triggered, `last_triggered_ts`), actions, and
   recovery. Mark each point as observed or inferred.

### "Did the deploy break it?"
1. Get the deploy time: `datadog events --hours 24 --json | jq '.[] | select(.title|test("deploy";"i"))'`,
   or the `correlate` / `triage` events.
2. Compare the same metric and the error logs before and after:
   `--from <deploy-1h> --to <deploy>` against `--from <deploy> --to <deploy+1h>`.
   Also compare with yesterday at the same time, so a daily peak doesn't pass
   for a regression.
3. Look for new error kinds (`logs aggregate --groupby @error.kind` over both
   windows): new ones point to the deploy; more of the same may just be load.

### "Why did monitor <id> fire?" / "Is it noisy?"
1. `datadog monitors show <id> --json`: query, `options.thresholds`, message,
   `overall_state`, `matching_downtimes`.
2. Chart what it watches: take the metric expression from the query (after
   the `):`, without the comparison) and run
   `datadog metrics query "<expr>" --since 1d --json`. See how often it
   crosses the threshold and for how long.
3. Was it edited? `datadog audit --since 7d --query "@evt.name:Monitor" --json`.
4. For a noisy monitor, suggest concrete changes (a longer evaluation window,
   a recovery threshold, a warning level, grouping) as text. Changing the
   monitor is a write (section 7).

### "Is <service> healthy?" / error budget
`datadog slos --query <service> --json`, then `datadog slos show <id> --json`
(SLI, target, remaining error budget), plus `datadog services context`.

### A link or a dashboard
- A Datadog URL tells you the site and the thing: `/monitors/<id>`,
  `/dashboard/<id>/…`, `/logs?query=…`. Use the id with the matching command.
- `datadog dashboards --query "<words>" --json` finds dashboards;
  `datadog dashboards get <id> --json` gives every widget and its queries. To
  answer "what does this dashboard say", run the queries that matter with
  `metrics query` and summarize.
- For a person, point to the terminal UI: `datadog ui <dashboard-id or link>`.

## 4. Query syntax

**Logs** (`logs`, `logs aggregate`, `triage --query`):
`service:checkout status:error env:prod` · attributes with `@`:
`@http.status_code:>=500`, `@duration:>2000000000` (ns) · negation
`-@http.url_details.path:/health` · wildcards `service:check*` · exact phrase
`"connection reset"` · OR `status:(error OR warn)`.

**Metrics** (`metrics query`): `<space aggr>:<metric>{<scope>} by {<tags>}`.
Example: `avg:system.cpu.user{env:prod,service:api} by {host}`.
- Space aggregators: `avg`, `sum`, `min`, `max`, and `p50`/`p75`/`p90`/`p95`/`p99`
  on distribution metrics (APM `trace.*.duration` works).
- Counts: `.as_count()` gives totals per interval, `.as_rate()` per second.
  Use counts for errors and hits over a window.
- Arithmetic works:
  `sum:trace.http.request.errors{service:api}.as_count() / sum:trace.http.request.hits{service:api}.as_count() * 100`
  is the error rate in %.
- Not sure of the unit? `datadog metrics meta <metric> --json`. APM
  durations are in seconds, the `@duration` log attribute in nanoseconds.
- Don't know the name? `datadog metrics search "<part>" --json`.

**Spans** (`traces`): `service:api status:error`, `resource_name:"GET /orders"`,
`@duration:>1s`, `env:prod`. **RUM** (`rum`): `@type:error`,
`@view.url_path:/checkout`, with `--type view|session|action|resource|error|long_task`.

## 5. Read-only commands

| Command | Use |
|---|---|
| `datadog triage --json` | Everything at once (section 2) |
| `datadog services context <svc> --json` | One service, everything |
| `datadog status --json` / `--team <t>` | Triggered monitors, open incidents, SLOs at risk |
| `datadog last --minutes 30 --json` | What just fired and what was just deployed |
| `datadog correlate --around <T> --window 20m --json` | Every signal around a moment |
| `datadog monitors --state Alert --json` | Monitors by state (`OK`, `Warn`, `"No Data"`), `--type` |
| `datadog monitors search "<text>" --json` · `monitors show <id> --json` | Find one · all of it |
| `datadog logs "<query>" --since 2h --limit 50 --json` | Log lines, with tags and attributes |
| `datadog logs "<query>" --all --jsonl` | Every match (up to 5000), streamed |
| `datadog logs aggregate "<query>" --groupby <facet> --minutes 60 --json` | Counts, grouped |
| `datadog metrics query "<query>" --since 4h --json` | Time series (`--from`/`--to` for exact windows) |
| `datadog metrics search "<part>" --json` · `metrics meta <m> --json` | Names · unit and type |
| `datadog traces "<query>" --minutes 60 --errors-only --json` | APM spans |
| `datadog rum "<query>" --type error --minutes 60 --json` | Real-user events |
| `datadog events --hours 6 --json` | Events: deploys, alerts, integrations |
| `datadog audit --since 24h --query "<q>" --json` | Who changed what |
| `datadog incidents --json` · `incidents show <id> --json` | Incidents |
| `datadog slos --query <text> --json` · `slos show <id> --json` | SLOs and error budget |
| `datadog dashboards --query <text> --json` · `dashboards get <id> --json` | Dashboards and their widgets |
| `datadog downtimes --json` | Who muted what, until when |
| `datadog hosts --filter <text> --json` · `tags get <host> --json` | Infrastructure |
| `datadog services --json` · `services show <svc> --json` | Service catalog |
| `datadog security --minutes 60 --json` · `pipelines --minutes 60 --json` | Security signals · CI runs |
| `datadog schema` | Every command and flag, as JSON |

In `audit` queries, `-@asset.type:datadog_agent_configuration` hides the
agents' periodic config noise, `@evt.name:Monitor` keeps monitor changes and
`@usr.email:<email>` keeps one person's changes.

## 6. Reporting back

Open with the answer, then the proof, then the next steps:

```
**Checkout is failing ~6% of payments since 09:38 CEST (07:38 UTC).**
Most likely cause: the payment provider is slow (authorize p95 5.1 s, normally 0.4 s).

Evidence
- Monitor #4101 "Checkout p95 latency" Alert since 09:39; #4102 "Payments provider error rate" Alert (6.4% > 5%)
- 412 error logs on checkout in the last 15 min (was ~5): 80% `TimeoutError: payments.authorize timed out after 5000ms`
- No deploy of checkout or payments in the 2 h before; `payments.timeout` changed 2s → 5s at 09:52 (maria, audit)

Next
1. Check the provider's status page, or switch to the backup acquirer (`payments.fallback`)
2. …
```

- Don't paste raw JSON. Use short tables for lists of monitors and services.
- Say what you couldn't check and why (a 403 on RUM, CI Visibility not
  enabled…).
- To let the user see it themselves: `datadog ui <dashboard>` (dashboards),
  `datadog ui --tab monitors`, `datadog open /monitors/<id>` (browser).

## 7. Commands that change Datadog: ask first

Describe the exact command and its effect, and wait for a yes:

| Command | Effect |
|---|---|
| `monitors mute <id> [-d 1h]` · `unmute <id>` | Silence a monitor (always suggest a duration) |
| `batch mute/unmute` | Many monitors at once |
| `downtimes schedule --scope … --duration …` · `downtimes cancel <id>` | Scheduled silences |
| `hosts mute/unmute` | Silence a host |
| `monitors create/edit/import/delete` | Change monitors (`create --dry-run` previews) |
| `dashboards create/clone/import/delete` | Change dashboards (`create --dry-run` previews) |
| `incidents create/update` | Declare or update an incident |
| `events post` · `deploy` | Post an event (`deploy --dry-run` previews) |
| `synthetics trigger` | Run synthetic tests now |
| `tags add/set/rm` | Change host tags |
| `integrations gcp … set/clear` | Change integration filters |
| `setup`, `logout`, `profile`, `config set` | Local configuration: the user's call |

Never delete anything unless the user asks for that specific deletion.
Muting is not fixing: when you suggest muting, give a duration and a reason.
