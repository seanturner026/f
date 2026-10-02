package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"add-ack-brewfile":        "add-ack-brewfile",
		"  Add Ack To Brewfile\n": "add-ack-to-brewfile",
		"`bump_tile/cache-ttl`":   "bump-tile-cache-ttl",
		"--weird--":               "weird",
		"a-very-long-name-that-goes-on-and-on-forever-and-ever": "a-very-long-name-that-goes-on-and-on-for",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExistingGoal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(path, []byte("# Feature: x\n\n## Goal\n\nline one\nline two\n\n## Repos\n\n- a\n"), 0o644)
	if got, want := existingGoal(path), "line one\nline two"; got != want {
		t.Errorf("existingGoal = %q, want %q", got, want)
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
