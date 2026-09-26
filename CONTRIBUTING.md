# Contributing

## Build and test

```bash
make check      # go vet, the tests and a build (bin/datadog)
make install    # build and copy to ~/.local/bin/datadog
datadog ui --demo
```

`datadog ui --demo` runs the UI against a made-up org (`internal/demo`), so
UI changes can be checked, and screenshotted, without touching a real
Datadog. The UI tests in `tui/` drive the app with keys against a fake
Datadog and fail if any frame isn't exactly the terminal's size.

AGENTS.md has the layout of the code and its conventions.

## Commit messages

Commits on `main` follow [Conventional Commits](https://www.conventionalcommits.org).
They decide the next version and become the release notes, so write the
subject for someone reading "what's new" before updating:

| Commit | Release |
|---|---|
| `feat: …`, `feat(tui): …` | minor: v1.**3**.0 |
| `fix: …`, `perf: …` | patch: v1.2.**1** |
| `feat!: …`, or `BREAKING CHANGE:` in the body | major: v**2**.0.0 |
| `docs:`, `refactor:`, `test:`, `build:`, `ci:`, `chore:` | none |

Add `[skip release]` to a commit message to keep it out of the next release.

## Releases

Nobody cuts releases by hand. On every push to `main`,
[Auto Version](.github/workflows/auto-version.yml):

1. works out the next version from the commits since the last tag
   (`scripts/next-version.sh`);
2. runs the tests;
3. builds `datadog-<os>-<arch>` for macOS, Linux and Windows, plus
   `checksums.txt` (`scripts/build-release.sh`);
4. writes the notes from the commits (`scripts/release-notes.sh`);
5. publishes the GitHub release, which creates the tag.

`datadog update`, `install.sh` and the update notice all read those
releases, so a release is live for everyone as soon as it's published.

To publish a version on purpose (a major bump, say), push a tag:
`git tag v2.0.0 && git push origin v2.0.0`. [Release](.github/workflows/release.yml)
builds and publishes it the same way.

`make release VERSION=v1.2.3` builds the release files into `dist/` locally,
for a look before pushing.
