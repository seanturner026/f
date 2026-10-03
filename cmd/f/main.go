// Command f creates and manages multi-repo feature workspaces:
// git worktrees + a generated AGENTS.md + pi (in a Herdr workspace when inside Herdr).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/seanturner026/pi/internal/feature"
)

const help = `f: start a pi session that works across several repos, each on its own worktree.

Usage:
  f <name>         start a feature: pick repos (fzf) → pi opens, ready for your prompt
                   reopens the feature (continuing its pi session) if it exists
  f                pick an existing feature to reopen
  f add [repo...]  add repos to the current feature (fzf if none), with Herdr tabs
  f prs            PRs for the current feature (saved state, instant); opens open ones

Flags:
  -r <repo>        repo, repeatable (skips fzf)
  -p <prompt>      first prompt for pi
  -f <feature>     feature for add/prs (default: from cwd)
  -r               prs: refresh live from GitHub (pi does this after every turn)
  -n, --json       prs: don't open the browser / print JSON

Inside Herdr each feature gets its own workspace (chat · nvim · <repo>… · shell, each
repo tab a live CodeDiff); outside,
pi starts in this terminal. Starting a feature offers to clean up merged ones.
Feature state (e.g. PRs) lives in <feature>/.context/, local-only.
Features live in ~/code/features/.features/<name>/<repo> (override with F_BASE).
`

// featureFlag picks a feature explicitly; otherwise it's inferred from the cwd.
var featureFlag = &cli.StringFlag{Name: "feature", Aliases: []string{"f"}, Usage: "feature (default: from cwd)"}

func main() {
	d, err := newDeps()
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
			return start(ctx, d, c.Args().First(), c.StringSlice("repo"), c.String("prompt"))
		},
		ShellComplete: func(ctx context.Context, c *cli.Command) { printLines(feature.List(d.layout)) },
		Commands: []*cli.Command{
			{
				Name:  "add",
				Usage: "add repos to this feature",
				Flags: []cli.Flag{featureFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return add(ctx, d, c.String("feature"), c.Args().Slice())
				},
				ShellComplete: func(ctx context.Context, c *cli.Command) {
					all, _ := feature.BaseRepos(d.layout)
					printLines(all)
				},
			},
			{
				Name:  "prs",
				Usage: "PRs for this feature",
				Flags: []cli.Flag{
					featureFlag,
					&cli.BoolFlag{Name: "refresh", Aliases: []string{"r"}, Usage: "look up live"},
					&cli.BoolFlag{Name: "no-open", Aliases: []string{"n"}, Usage: "don't open in browser"},
					&cli.BoolFlag{Name: "json", Usage: "machine-readable output"},
					&cli.StringFlag{Name: "add", Usage: "record a PR URL"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return prs(ctx, d, c.String("feature"), prsOptions{
						refresh: c.Bool("refresh"),
						noOpen:  c.Bool("no-open") || c.Bool("json"),
						asJSON:  c.Bool("json"),
						addURL:  c.String("add"),
					})
				},
			},
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%s %v\n", red("error:"), err)
	os.Exit(1)
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}
