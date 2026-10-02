// Command f creates and manages multi-repo feature workspaces:
// git worktrees + a generated AGENTS.md + a Herdr workspace running pi.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `f: multi-repo feature workspaces (git worktrees + Herdr + pi)

  f new  [feature] [-r repo]... [-p "prompt"]
         prompt ($EDITOR) → pick repos (fzf) → pi suggests a name →
         worktrees, AGENTS.md, Herdr workspace, pi starts with your prompt
  f add  <feature> [repo...]      add repos (fzf if none given)
  f open <feature>                focus (or recreate) the Herdr workspace
  f ls                            list features
  f prs  [feature]                PRs for the feature branch in each repo
  f done <feature> [--force]      remove worktrees + close the Herdr workspace

Layout:  $F_BASE/.features/<feature>/<repo>   (branch = <feature>)
         F_BASE defaults to ~/code/features
Herdr:   tabs 1 chat (pi) · 2 nvim · 3 shell   (F_NO_HERDR=1 to skip)
`

// errUsage signals that the usage text should be printed.
var errUsage = errors.New("usage")

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "%s %v\n", red("error:"), err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "new":
		return cmdNew(cfg, rest)
	case "add":
		return cmdAdd(cfg, rest)
	case "open":
		return cmdOpen(cfg, rest)
	case "ls":
		return cmdLs(cfg)
	case "prs":
		return cmdPrs(cfg, rest)
	case "done":
		return cmdDone(cfg, rest)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see f --help)", cmd)
	}
}

type config struct {
	Base     string // directory of base clones
	Features string // directory of feature workspaces
	NoHerdr  bool
}

func loadConfig() (config, error) {
	base := os.Getenv("F_BASE")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return config{}, err
		}
		base = filepath.Join(home, "code", "features")
	}
	return config{
		Base:     base,
		Features: filepath.Join(base, ".features"),
		NoHerdr:  os.Getenv("F_NO_HERDR") != "",
	}, nil
}

func (c config) featureDir(name string) string { return filepath.Join(c.Features, name) }

// featureFromCwd returns the feature containing the working directory, if any.
func (c config) featureFromCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(c.Features, wd)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.Split(rel, string(filepath.Separator))[0]
}

// ---- output helpers ----

func color(code, s string) string { return "\033[" + code + "m" + s + "\033[0m" }
func red(s string) string         { return color("31", s) }
func green(s string) string       { return color("32", s) }
func dim(s string) string         { return color("2", s) }
func bold(s string) string        { return color("1", s) }

func info(format string, a ...any) { fmt.Println(dim(fmt.Sprintf(format, a...))) }
func ok(format string, a ...any)   { fmt.Println(green(fmt.Sprintf(format, a...))) }
