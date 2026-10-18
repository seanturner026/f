// Package shell runs external commands. Exec satisfies the Runner interfaces
// declared by the packages that shell out (git, github, herdr, beads, feature).
package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Exec runs commands for real.
type Exec struct{}

// Run executes name with args in dir and returns trimmed stdout. On failure the
// error includes stderr and wraps the underlying *exec.ExitError.
func (Exec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), msg, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// Quote quotes s for a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// QuoteAll quotes each arg for a POSIX shell.
func QuoteAll(args []string) []string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return q
}
