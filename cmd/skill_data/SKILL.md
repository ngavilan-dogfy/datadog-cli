---
name: datadog
description: Read Datadog from the terminal with the datadog CLI — what's alerting, triage, logs, metrics, monitors, dashboards, services and incidents. Use when the user asks about alerts, errors, latency, dashboards, logs or the health of a service.
allowed-tools: Bash, Read
---

Use the `datadog` CLI to answer questions about the user's Datadog org.

1. Start with `datadog triage --json` (add `--service X --env prod` or
   `--since 15m` to narrow it). Read `summary` first, then drill down.
2. For one service: `datadog services context <service> --json`.
3. Everything else: `datadog schema` lists every command and flag as JSON.
   Most commands accept `--json`.

Never create, edit, mute or delete anything without asking the user first.
If a command fails with 401/403, run `datadog doctor` and tell the user what
it says.
