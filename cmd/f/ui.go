package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// useColor: only when a person is looking. nvim, pi and launchd read f's
// output as text, where escape codes show up as junk ("^[[31merror:").
var useColor = isTerminal(os.Stdout) && isTerminal(os.Stderr)

func color(code, s string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func red(s string) string   { return color("31", s) }
func green(s string) string { return color("32", s) }
func dim(s string) string   { return color("2", s) }
func bold(s string) string  { return color("1", s) }

// link makes text a terminal hyperlink (OSC 8): cmd+click opens url.
func link(url, text string) string {
	if !useColor || url == "" {
		return text
	}
	return "\033]8;;" + url + "\033\\" + text + "\033]8;;\033\\"
}

func info(format string, a ...any) { fmt.Println(dim(fmt.Sprintf(format, a...))) }
func ok(format string, a ...any)   { fmt.Println(green(fmt.Sprintf(format, a...))) }

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func copyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
	}
	return nil
}

// interactive reports whether a person is at the terminal.
func interactive() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }

// fzf runs fzf inline (40% height) over items and returns the selection (nil if cancelled).
func fzf(items []string, args ...string) ([]string, error) {
	return runFzf(items, append([]string{"--height=40%"}, args...)...)
}

// runFzf runs fzf with exactly args (plus --reverse). Without --height fzf is
// full-screen and skips the cursor-position query, which is faster in popups.
func runFzf(items []string, args ...string) ([]string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return nil, errors.New("fzf not installed")
	}
	cmd := exec.Command("fzf", append([]string{"--reverse"}, args...)...)
	cmd.Stdin = bytes.NewBufferString(strings.Join(items, "\n") + "\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
		return nil, nil // no match / cancelled
	}
	if err != nil {
		return nil, fmt.Errorf("fzf: %w", err)
	}
	expect := false
	for _, a := range args {
		expect = expect || strings.HasPrefix(a, "--expect")
	}
	return parseFzfOutput(string(out), expect), nil
}

// parseFzfOutput splits fzf's output into lines. With --expect, the first line
// is the key pressed and is empty for ENTER, so it's kept even when empty.
func parseFzfOutput(out string, expect bool) []string {
	var picked []string
	for i, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l != "" || (expect && i == 0) {
			picked = append(picked, l)
		}
	}
	return picked
}

// pickRepos multi-selects from all, excluding ones in exclude.
func pickRepos(all, exclude []string) ([]string, error) {
	skip := map[string]bool{}
	for _, r := range exclude {
		skip[r] = true
	}
	var items []string
	for _, r := range all {
		if !skip[r] {
			items = append(items, r)
		}
	}
	// TAB selects and clears the filter, ready to type the next repo.
	return fzf(items, "--multi", "--bind=tab:toggle+clear-query", "--prompt=repos> ",
		"--header=TAB select · ENTER confirm · ESC cancel")
}
