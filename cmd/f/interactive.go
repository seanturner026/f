package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const promptTemplate = `

# What are we doing? This becomes pi's first prompt and the feature goal.
# Lines starting with '#' are ignored. Save empty to abort.
`

// editPrompt opens $EDITOR and returns the text written, minus comment lines.
func editPrompt() (string, error) {
	f, err := os.CreateTemp("", "f-prompt-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(promptTemplate); err != nil {
		return "", err
	}
	f.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	// Run through the shell so EDITOR values with flags ("code -w") work.
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", f.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor: %w", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// pickRepos runs fzf multi-select over base clones, excluding `exclude`.
func pickRepos(cfg config, exclude []string) ([]string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return nil, errors.New("fzf not installed; pass repos with -r")
	}
	all, err := baseRepos(cfg)
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, r := range exclude {
		skip[r] = true
	}
	var input bytes.Buffer
	for _, r := range all {
		if !skip[r] {
			fmt.Fprintln(&input, r)
		}
	}
	cmd := exec.Command("fzf", "--multi", "--height=40%", "--reverse",
		"--prompt=repos> ", "--header=TAB select · ENTER confirm · ESC cancel")
	cmd.Stdin, cmd.Stderr = &input, os.Stderr
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

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// suggestName asks a fast model for a branch-style name. Best effort: "" on failure.
func suggestName(prompt string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pi", "-p", "--no-session", "--offline",
		"-ne", "-ns", "-nc", "--no-tools", "--model", "claude-haiku-4-5",
		"Give a 2-4 word kebab-case git branch name for this task. Reply with only the name.\n\nTask: "+prompt)
	cmd.Dir = os.TempDir() // avoid project config/trust prompts
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return slugify(first)
}

// askName shows the suggestion and lets you accept (enter) or type another.
func askName(suggestion string) (string, error) {
	if suggestion != "" {
		fmt.Printf("name [%s]: ", bold(suggestion))
	} else {
		fmt.Print("name: ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no name; aborted")
	}
	if name := strings.TrimSpace(line); name != "" {
		return slugify(name), nil
	}
	if suggestion == "" {
		return "", errors.New("no name; aborted")
	}
	return suggestion, nil
}
