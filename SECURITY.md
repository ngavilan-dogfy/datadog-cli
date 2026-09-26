# Security

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: **Security → Report a
vulnerability** on this repository. Don't open a public issue. You'll get an
acknowledgement within a few days and a fix, or an explanation, as soon as
it's understood.

Only the latest release receives fixes; `datadog update` gets you there.

## What the CLI handles, and how

**Credentials.** Your API and application keys are stored in plain text in
`~/.config/datadog-cli/profiles/<profile>.yaml`, written atomically with mode
`0600` in a directory with mode `0700`. They are sent only to your Datadog
site's API, over HTTPS. Anyone who can read your home directory can read the
keys; on shared machines prefer the `DD_API_KEY` / `DD_APP_KEY` environment
variables from your secret manager, or an application key with read-only
scopes.

**Clipboard.** During `datadog setup`, while a key prompt is on screen, the
clipboard is read a few times per second. Only a value shaped like the key
being asked for is taken; nothing else is kept or logged. Once the profile is
saved, the key is removed from the clipboard if it is still there.

**Read-only mode.** `read_only: true` in a profile, or `DATADOG_READ_ONLY=1`,
makes the CLI refuse every mutating command before any request is sent. It
is a safeguard enforced by this program, not by Datadog: for a guarantee,
create the application key with read-only scopes.

**Network.** The CLI talks to your Datadog site and, for updates, to GitHub.
There is no telemetry and no third-party service.

**Updates and the installer.** Releases are built by GitHub Actions from
this repository. `install.sh` and `datadog update` verify each download
against the SHA-256 in the release's `checksums.txt` and check that the new
binary runs before replacing the old one. Checksums protect against corrupted
or truncated downloads; they are published with the release, so they are not
a signature against a compromised release.

**Agents.** The Claude Code skill tells agents to read freely and to ask
before any command that changes Datadog. That is guidance, not enforcement:
combine it with a read-only profile when an agent works unattended.
