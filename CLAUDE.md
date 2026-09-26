# datadog-cli

Read [AGENTS.md](AGENTS.md): how to use this CLI and how to work on it.
[ARCHITECTURE.md](ARCHITECTURE.md) maps the code; [CONTRIBUTING.md](CONTRIBUTING.md)
covers the workflow and releases.

Essentials:

- `make check` (vet, tests, build) before committing; `datadog ui --demo` to
  look at UI changes without a real organization.
- One file per command area in `cmd/`, registered in `init()`, with the
  three output modes (terminal, TSV, `--json`); `cmd/logs.go` is the reference.
- Mutating commands go in `cmd/readonly.go`; commands without credentials in
  `needsAuth` (`cmd/root.go`).
- `datadog/` types model the real API responses: verify them live.
- The skill is `cmd/skill_data/SKILL.md`, embedded in the binary;
  `.claude/skills/datadog/SKILL.md` must stay identical.
- Conventional commits pick the next version and write the release notes.
