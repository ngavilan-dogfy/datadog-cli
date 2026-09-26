#!/bin/sh
# Re-records assets/setup.gif: the e2e build of datadog accepts two made-up
# keys without calling Datadog, runs under a throwaway HOME, and logs the pages
# it would open; each key is copied to the clipboard 2.5s after its page
# "opens", as if clicked in the browser. macOS only (pbcopy); needs vhs.
#
#   make e2e && scripts/record-setup.sh
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
[ -x "$root/bin/datadog-e2e" ] || { echo "build it first: make e2e" >&2; exit 1; }
command -v vhs >/dev/null 2>&1 || { echo "needs vhs: brew install vhs" >&2; exit 1; }

api=4b7f0c2a1e4d3b2c1a09f8e7d6c5b4a3
app=9c1e5a7b3d2f4e6a8b0c1d2e3f4a5b6c7d8e9f01
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/home" "$work/bin"
ln -s "$root/bin/datadog-e2e" "$work/bin/datadog"
: > "$work/open.log"
sed -e "s|@R@|$work|g" -e "s|@API@|$api|" -e "s|@APP@|$app|" \
	"$root/scripts/record/setup.tape.in" > "$work/setup.tape"

clip="$(pbpaste 2>/dev/null || true)"
printf 'nothing here' | pbcopy
(
	copied=""
	i=0
	while [ "$i" -lt 900 ]; do
		if [ -z "$copied" ] && grep -q "api-keys" "$work/open.log"; then
			sleep 2.5
			printf '%s' "$api" | pbcopy
			copied=1
		fi
		if grep -q "application-keys" "$work/open.log"; then
			sleep 2.5
			printf '%s' "$app" | pbcopy
			exit 0
		fi
		sleep 0.1
		i=$((i + 1))
	done
) &
(cd "$work" && vhs setup.tape)
printf '%s' "$clip" | pbcopy
cp "$work/setup.gif" "$root/assets/setup.gif"
echo "assets/setup.gif updated: look at it before committing"
