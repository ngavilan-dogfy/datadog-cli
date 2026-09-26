# datadog-cli

Go CLI for Datadog. Read AGENTS.md for the full guide (CLI usage conventions
for agents + development notes) and CONTRIBUTING.md for releases.

Essentials:
- `make check` = vet + test + build. Run it before committing.
- Command pattern: one file per area in `cmd/`, self-registers in `init()`,
  implements TTY table + piped TSV + `--json`. Reference: `cmd/logs.go`.
- `datadog/` is a hand-rolled HTTP client; types must match the *real* API
  responses (verify live, docs drift).
- Commands that work without credentials go in `needsAuth` in `cmd/root.go`.
- `datadog ui --demo` shows the UI with made-up data: use it to look at UI
  changes, and for screenshots (never screenshot a real org for the README).
- Conventional commits (`feat:`, `fix:`, `perf:`; `!` for breaking): they
  pick the next version and write the release notes.
- The Claude Code skill is `cmd/skill_data/SKILL.md` (embedded in the binary);
  `.claude/skills/datadog/SKILL.md` must stay identical (a test checks).
