package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func color(code, s string) string { return "\033[" + code + "m" + s + "\033[0m" }
func red(s string) string         { return color("31", s) }
func green(s string) string       { return color("32", s) }
func dim(s string) string         { return color("2", s) }
func bold(s string) string        { return color("1", s) }

func info(format string, a ...any) { fmt.Println(dim(fmt.Sprintf(format, a...))) }
func ok(format string, a ...any)   { fmt.Println(green(fmt.Sprintf(format, a...))) }

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// interactive reports whether a person is at the terminal.
func interactive() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }

// fzf runs fzf over items and returns the selection (nil if cancelled).
func fzf(items []string, args ...string) ([]string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return nil, errors.New("fzf not installed")
	}
	cmd := exec.Command("fzf", append([]string{"--height=40%", "--reverse"}, args...)...)
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
	var picked []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" {
			picked = append(picked, l)
		}
	}
	return picked, nil
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
	return fzf(items, "--multi", "--prompt=repos> ", "--header=TAB select · ENTER confirm · ESC cancel")
}
