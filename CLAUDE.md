# datadog-cli

Go CLI for Datadog. Read AGENTS.md for the full guide (CLI usage conventions
for agents + development notes).

Essentials:
- `make check` = vet + test + build. Run it before committing.
- Command pattern: one file per area in `cmd/`, self-registers in `init()`,
  implements TTY table + piped TSV + `--json`. Reference: `cmd/logs.go`.
- `datadog/` is a hand-rolled HTTP client; types must match the *real* API
  responses (verify live, docs drift).
- Commands that work without credentials go in the `noAuth` list in `cmd/root.go`.
