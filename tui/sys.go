package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runner executes external programs (git, gh, pgrep, lsof, osascript…).
// Everything that touches the machine goes through it so tests can fake
// what they can't safely run for real.
type runner interface {
	run(timeout time.Duration, dir, name string, args ...string) ([]byte, error)
}

var sys runner = execRunner{}

type execRunner struct{}

func (execRunner) run(timeout time.Duration, dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s: %s", name, firstLine(msg))
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// lookPath is exec.LookPath, swappable in tests.
var lookPath = exec.LookPath

// shellQuote quotes s for POSIX shells.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
