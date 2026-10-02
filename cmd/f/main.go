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

const help = `f [name]         start a feature, or reopen it if it exists
                 (no name: pi suggests one after you write the prompt)
f add [repo...]  add repos to this feature (fzf if none)
f prs            PRs for this feature
f gc             clean up features whose PRs have all merged
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
			&cli.StringFlag{Name: "prompt", Aliases: []string{"p"}, Usage: "prompt (skips $EDITOR)", Local: true},
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
				Flags: []cli.Flag{featureFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return prs(cfg, c.String("feature"))
				},
			},
			{
				Name:  "gc",
				Usage: "clean up features whose PRs have all merged",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "don't ask"}},
				Action: func(ctx context.Context, c *cli.Command) error {
					return gc(cfg, c.Bool("yes"))
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
