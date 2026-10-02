package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
	return strings.Fields(string(out)), nil
}

// pickRepos multi-selects base clones, excluding ones already in the feature.
func pickRepos(cfg config, exclude []string) ([]string, error) {
	all, err := baseRepos(cfg)
	if err != nil {
		return nil, err
	}
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

// pickFeature selects one existing feature ("" if cancelled).
func pickFeature(cfg config) (string, error) {
	picked, err := fzf(features(cfg), "--prompt=feature> ", "--header=ENTER open · ESC cancel")
	if err != nil || len(picked) == 0 {
		return "", err
	}
	return picked[0], nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func interactive() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }
