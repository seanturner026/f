// Command f creates and manages multi-repo feature workspaces:
// git worktrees + a generated AGENTS.md + a Herdr workspace running pi.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

const help = `f: start a pi session that works across several repos, each on its own worktree.

Usage:
  f <name>         start a feature: pick repos (fzf) → pi opens, ready for your prompt
                   reopens the feature (continuing its pi session) if it exists
  f                pick an existing feature to reopen
  f add [repo...]  add repos to the current feature (fzf if none)
  f prs            PRs for the current feature; opens open ones in the browser

Flags:
  -r <repo>        repo, repeatable (skips fzf)
  -p <prompt>      first prompt for pi
  -f <feature>     feature for add/prs (default: from cwd)
  -n, --json       prs: don't open the browser / print JSON

Inside Herdr each feature gets its own workspace (chat · nvim · shell); outside,
pi starts in this terminal. Starting a feature offers to clean up merged ones.
Features live in ~/code/features/.features/<name>/<repo> (override with F_BASE).
`

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fail(err)
	}
	app := &cli.Command{
		Name:                          "f",
		CustomRootCommandHelpTemplate: help,
		HideHelpCommand:               true,
		EnableShellCompletion:         true,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "repo", Aliases: []string{"r"}, Usage: "repo (skips fzf)", Local: true},
			&cli.StringFlag{Name: "prompt", Aliases: []string{"p"}, Usage: "first prompt for pi", Local: true},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.NArg() > 1 {
				return fmt.Errorf("one feature name at a time")
			}
			return start(cfg, c.Args().First(), c.StringSlice("repo"), c.String("prompt"))
		},
		ShellComplete: func(ctx context.Context, c *cli.Command) { printLines(features(cfg)) },
		Commands: []*cli.Command{
			{
				Name:  "add",
				Usage: "add repos to this feature",
				Flags: []cli.Flag{featureFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return add(cfg, c.String("feature"), c.Args().Slice())
				},
				ShellComplete: func(ctx context.Context, c *cli.Command) {
					all, _ := baseRepos(cfg)
					printLines(all)
				},
			},
			{
				Name:  "prs",
				Usage: "PRs for this feature",
				Flags: []cli.Flag{
					featureFlag,
					&cli.BoolFlag{Name: "no-open", Aliases: []string{"n"}, Usage: "don't open in browser"},
					&cli.BoolFlag{Name: "json", Usage: "machine-readable output"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return prs(cfg, c.String("feature"), c.Bool("no-open") || c.Bool("json"), c.Bool("json"))
				},
			},
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		fail(err)
	}
}

// featureFlag picks a feature explicitly; otherwise it's inferred from the cwd.
var featureFlag = &cli.StringFlag{Name: "feature", Aliases: []string{"f"}, Usage: "feature (default: from cwd)"}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%s %v\n", red("error:"), err)
	os.Exit(1)
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
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

// resolveFeature returns the explicit feature, or the one containing the cwd.
func (c config) resolveFeature(explicit string) (string, error) {
	name := explicit
	if name == "" {
		if wd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(c.Features, wd); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				name = strings.Split(rel, string(filepath.Separator))[0]
			}
		}
	}
	if name == "" {
		return "", fmt.Errorf("not inside a feature (cd into one or pass -f <feature>)")
	}
	if !exists(c.featureDir(name)) {
		return "", fmt.Errorf("no feature %q", name)
	}
	return name, nil
}

// features lists existing feature names.
func features(cfg config) []string {
	entries, _ := os.ReadDir(cfg.Features)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// ---- output helpers ----

func color(code, s string) string { return "\033[" + code + "m" + s + "\033[0m" }
func red(s string) string         { return color("31", s) }
func green(s string) string       { return color("32", s) }
func dim(s string) string         { return color("2", s) }
func bold(s string) string        { return color("1", s) }

func info(format string, a ...any) { fmt.Println(dim(fmt.Sprintf(format, a...))) }
func ok(format string, a ...any)   { fmt.Println(green(fmt.Sprintf(format, a...))) }
