//go:build !windows

package tui

import (
	"os"
	"os/exec"
)

func bestEffortResetTTY() {
	// If stdin isn't a TTY, nothing to fix.
	fi, err := os.Stdin.Stat()
	if err != nil {
		return
	}
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		return
	}

	// Reset the controlling TTY. This is intentionally best-effort.
	// Use /dev/tty so we don't depend on redirected stdin.
	_ = exec.Command("sh", "-lc", "stty sane < /dev/tty >/dev/null 2>&1 || true").Run()
}
