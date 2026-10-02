package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistingGoal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(path, []byte("# Feature: x\n\n## Goal\n\nline one\nline two\n\n## Repos\n\n- a\n"), 0o644)
	if got, want := existingGoal(path), "line one\nline two"; got != want {
		t.Errorf("existingGoal = %q, want %q", got, want)
	}
	os.WriteFile(path, []byte("## Goal\n\n"+goalPlaceholder+"\n\n## Repos\n"), 0o644)
	if got := existingGoal(path); got != "" {
		t.Errorf("placeholder should read as empty, got %q", got)
	}
	if got := existingGoal(filepath.Join(t.TempDir(), "missing.md")); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}

func TestAgentName(t *testing.T) {
	if got := agentName("v1.2-release"); got != "f-v1-2-release" {
		t.Errorf("got %q", got)
	}
	if got := agentName("this-is-a-really-long-feature-name-indeed"); len(got) > 32 {
		t.Errorf("too long: %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	if got, want := shellQuote("it's"), `'it'\''s'`; got != want {
		t.Errorf("got %s want %s", got, want)
	}
}
