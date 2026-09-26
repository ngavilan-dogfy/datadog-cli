#!/bin/sh
# Install or update datadog, the Datadog CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/datadog-cli/main/install.sh | sh
#
# Settings (environment variables):
#   DATADOG_VERSION=v1.2.0             that release instead of the latest
#   DATADOG_INSTALL_DIR=/usr/local/bin where to put it (default: ~/.local/bin)
#
# It downloads the binary for this machine from the GitHub release, checks
# it against the release's checksums.txt and that it runs, then moves it into
# place. No sudo unless you pick a folder that needs it.
set -eu

REPO="ngavilan-dogfy/datadog-cli"
BIN="datadog"
DIR="${DATADOG_INSTALL_DIR:-$HOME/.local/bin}"

if [ -t 1 ]; then
	B="$(printf '\033[1m')"; D="$(printf '\033[2m')"; G="$(printf '\033[32m')"
	R="$(printf '\033[31m')"; Y="$(printf '\033[33m')"; N="$(printf '\033[0m')"
else
	B=""; D=""; G=""; R=""; Y=""; N=""
fi
say() { printf '  %s\n' "$*"; }
ok() { printf '  %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '  %s!%s %s\n' "$Y" "$N" "$*"; }
die() {
	printf '  %s✗%s %s\n' "$R" "$N" "$1" >&2
	[ -n "${2:-}" ] && printf '    %s%s%s\n' "$D" "$2" "$N" >&2
	exit 1
}

need() { command -v "$1" >/dev/null 2>&1 || die "This installer needs '$1'." "Install it and run the installer again."; }
need curl
need uname

# ─── platform ─────────────────────────────────────────────────────
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
darwin | linux) ;;
mingw* | msys* | cygwin*) die "On Windows, download ${BIN}-windows-amd64.exe from the releases page." "https://github.com/${REPO}/releases/latest" ;;
*) die "Unsupported system: $os" "Build from source instead: go install github.com/${REPO}/cmd/${BIN}@latest" ;;
esac
case "$arch" in
x86_64 | amd64) arch="amd64" ;;
arm64 | aarch64) arch="arm64" ;;
*) die "Unsupported processor: $arch" "Build from source instead: go install github.com/${REPO}/cmd/${BIN}@latest" ;;
esac
# A shell running under Rosetta reports x86_64 on Apple silicon.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch="arm64"
fi
asset="${BIN}-${os}-${arch}"

# ─── which release ────────────────────────────────────────────────
tag="${DATADOG_VERSION:-}"
if [ -z "$tag" ]; then
	# The public redirect has no rate limit; the API is the fallback.
	tag="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" 2>/dev/null | sed -n 's#.*/releases/tag/##p')"
	if [ -z "$tag" ]; then
		tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
	fi
	[ -n "$tag" ] || die "Couldn't find the latest release." "Check your connection, or see https://github.com/${REPO}/releases"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac

current=""
if [ -x "$DIR/$BIN" ]; then
	current="$("$DIR/$BIN" version 2>/dev/null | head -n 1 || true)"
fi

printf '\n'
if [ "$current" = "$tag" ]; then
	ok "${B}${BIN} ${tag}${N} is already installed ${D}(${DIR}/${BIN})${N}"
	printf '\n'
	exit 0
fi
say "${B}${BIN} ${tag}${N} ${D}${os}/${arch}${N}"

# ─── download and verify ──────────────────────────────────────────
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t datadog-install)"
trap 'rm -rf "$tmp"' EXIT INT TERM

base="https://github.com/${REPO}/releases/download/${tag}"
say "${D}downloading ${asset}…${N}"
curl -fsSL --retry 2 -o "$tmp/$BIN" "$base/$asset" ||
	die "Download failed: $base/$asset" "The release may still be building; try again in a minute."

if curl -fsSL --retry 2 -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
	want="$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")"
	if command -v sha256sum >/dev/null 2>&1; then
		got="$(sha256sum "$tmp/$BIN" | awk '{ print $1 }')"
	else
		got="$(shasum -a 256 "$tmp/$BIN" | awk '{ print $1 }')"
	fi
	[ -n "$want" ] || die "checksums.txt doesn't list $asset." "Nothing was installed."
	[ "$want" = "$got" ] || die "Checksum mismatch: the download is corrupt or was tampered with." "Nothing was installed."
	ok "checksum verified"
else
	warn "this release has no checksums.txt; skipping the checksum check"
fi

chmod +x "$tmp/$BIN"
"$tmp/$BIN" version >/dev/null 2>&1 ||
	die "The downloaded binary doesn't run on this machine." "Download it by hand: https://github.com/${REPO}/releases/tag/${tag}"

# ─── install ──────────────────────────────────────────────────────
# Copy next to the target and rename over it: atomic, and a datadog that's
# running keeps working (macOS kills binaries rewritten in place).
sudo=""
if ! mkdir -p "$DIR" 2>/dev/null || [ ! -w "$DIR" ]; then
	need sudo
	sudo="sudo"
	say "${D}${DIR} needs admin rights; asking sudo…${N}"
	$sudo mkdir -p "$DIR"
fi
$sudo cp "$tmp/$BIN" "$DIR/.$BIN.new"
$sudo mv -f "$DIR/.$BIN.new" "$DIR/$BIN"

if [ -n "$current" ]; then
	ok "updated ${D}${current} →${N} ${B}${tag}${N} ${D}${DIR}/${BIN}${N}"
else
	ok "installed ${B}${BIN} ${tag}${N} ${D}${DIR}/${BIN}${N}"
fi

# The Claude Code skill ships inside the binary: refresh it if it's there.
if [ -f "$HOME/.claude/skills/datadog/SKILL.md" ]; then
	"$DIR/$BIN" skill install --quiet >/dev/null 2>&1 && ok "Claude Code skill refreshed"
fi

# ─── PATH ─────────────────────────────────────────────────────────
found="$(command -v "$BIN" 2>/dev/null || true)"
case ":$PATH:" in
*":$DIR:"*)
	if [ -n "$found" ] && [ "$found" != "$DIR/$BIN" ]; then
		printf '\n'
		warn "Another ${BIN} comes first in your PATH: ${B}${found}${N}"
		say "  Remove it, or put ${DIR} before it."
	fi
	;;
*)
	shell_name="$(basename "${SHELL:-sh}")"
	# $HOME/... keeps the line portable when the folder is under home.
	# shellcheck disable=SC2016
	case "$DIR" in "$HOME"/*) show_dir='$HOME'"${DIR#"$HOME"}" ;; *) show_dir="$DIR" ;; esac
	# shellcheck disable=SC2088 # shown to the user, not expanded
	case "$shell_name" in
	zsh) rc="~/.zshrc" line="export PATH=\"$show_dir:\$PATH\"" ;;
	bash) rc="~/.bashrc" line="export PATH=\"$show_dir:\$PATH\"" ;;
	fish) rc="~/.config/fish/config.fish" line="fish_add_path $show_dir" ;;
	*) rc="your shell's startup file" line="export PATH=\"$show_dir:\$PATH\"" ;;
	esac
	printf '\n'
	warn "${DIR} isn't in your PATH yet. Add this line to ${rc}:"
	printf '\n      %s%s%s\n\n' "$B" "$line" "$N"
	say "then open a new terminal."
	;;
esac

# ─── next ─────────────────────────────────────────────────────────
printf '\n'
if [ -n "$current" ]; then
	say "What's new: https://github.com/${REPO}/releases/tag/${tag}"
elif ls "$HOME"/.config/datadog-cli/profiles/*.yaml >/dev/null 2>&1 || [ -n "${DD_API_KEY:-}" ]; then
	say "Run ${B}${BIN} ui${N} for the dashboard, or ${B}${BIN} --help${N}."
else
	say "Next: run ${B}${BIN} setup${N} to connect it to your Datadog (two keys, a minute)."
fi
if [ -d "$HOME/.claude" ] && [ ! -f "$HOME/.claude/skills/datadog/SKILL.md" ]; then
	say "Using Claude Code? ${B}${BIN} skill install${N} teaches it this CLI."
fi
say "Later updates: ${B}${BIN} update${N}"
printf '\n'
