package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// iTerm2 is driven through AppleScript. Arguments go through argv, never
// spliced into the script, so paths and prompts need no escaping there.

// openTabScript opens a tab, runs a command in it and — when given the
// TUI's own tty — selects the TUI's tab again, so agents start in the
// background and you can keep dispatching. Prints "<session id> <tty>".
const openTabScript = `on run argv
	set theCmd to item 1 of argv
	set theName to item 2 of argv
	set backTTY to item 3 of argv
	tell application "iTerm2"
		if (count of windows) is 0 then
			create window with default profile
		else
			tell current window to create tab with default profile
		end if
		set newSession to current session of current window
		tell newSession
			set name to theName
			write text theCmd
		end tell
		set theResult to (unique id of newSession) & " " & (tty of newSession)
		if backTTY is not "" then
			repeat with w in windows
				repeat with t in tabs of w
					repeat with s in sessions of t
						if (tty of s) is backTTY then
							tell t to select
							tell s to select
						end if
					end repeat
				end repeat
			end repeat
		end if
		return theResult
	end tell
end run`

// focusTTYScript brings forward the iTerm2 tab whose session owns a tty.
const focusTTYScript = `on run argv
	set theTTY to item 1 of argv
	tell application "iTerm2"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with s in sessions of t
					if (tty of s) is theTTY then
						tell t to select
						tell s to select
						set index of w to 1
						activate
						return "ok"
					end if
				end repeat
			end repeat
		end repeat
	end tell
	return "missing"
end run`

// inITerm reports whether the TUI runs inside iTerm2.
func inITerm() bool { return os.Getenv("TERM_PROGRAM") == "iTerm.app" }

// ownTTY is the TUI's terminal device (e.g. /dev/ttys004), or "".
func ownTTY() string {
	out, err := sys.run(3*time.Second, "", "ps", "-o", "tty=", "-p", strconv.Itoa(os.Getpid()))
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(string(out))
	if t == "" || t == "??" {
		return ""
	}
	return "/dev/" + t
}

// openInITerm runs command in a new background tab titled name.
func openInITerm(command, name string) (string, error) {
	out, err := sys.run(15*time.Second, "", "osascript", "-e", openTabScript, command, name, ownTTY())
	if err != nil {
		return "", fmt.Errorf("iTerm2: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func focusITermTTY(tty string) error {
	out, err := sys.run(10*time.Second, "", "osascript", "-e", focusTTYScript, tty)
	if err != nil {
		return fmt.Errorf("iTerm2: %w", err)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("no iTerm2 tab owns %s anymore", tty)
	}
	return nil
}
