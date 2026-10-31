// Package agent is the coding agent f starts in a feature's chat tab: Claude Code.
package agent

import (
	"os"
	"path/filepath"
	"regexp"
)

// Claude is the executable and Herdr's --kind value.
const Claude = "claude"

// Command starts the agent: Bin with Args.
type Command struct {
	Bin  string
	Args []string
}

// ClaudeDir holds the Claude Code setup (settings.json, statusline, the f mod):
// F_CLAUDE_DIR, else ~/.claude/f (just link puts the repo's claude/ there).
func ClaudeDir() string {
	if d := os.Getenv("F_CLAUDE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "f")
}

// For is the command that opens feature name's chat in dir: a new session,
// or the last one again with reopen (when there is one).
func For(name, dir string, reopen bool) Command {
	cd := ClaudeDir()
	c := Command{Bin: Claude, Args: []string{"--name", name, "--permission-mode", "auto",
		"--settings", filepath.Join(cd, "settings.json"), "--plugin-dir", filepath.Join(cd, "f")}}
	// --continue with no saved session makes claude exit.
	if reopen && hasClaudeSession(dir) {
		c.Args = append(c.Args, "--continue")
	}
	return c
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9-]`)

// hasClaudeSession reports whether Claude Code saved a session for dir
// (~/.claude/projects/<dir, every non-alphanumeric as "-">/*.jsonl).
func hasClaudeSession(dir string) bool {
	home, _ := os.UserHomeDir()
	sessions, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", nonAlnum.ReplaceAllString(dir, "-"), "*.jsonl"))
	return len(sessions) > 0
}
