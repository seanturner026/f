// Command f creates and manages multi-repo feature workspaces:
// git worktrees + a generated AGENTS.md + pi (in a Herdr workspace when inside Herdr).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/seanturner026/pi/internal/feature"
)

const help = `f: one pi session across several repos, each on its own worktree.

  f <name>         start a feature (pick repos), or reopen it
  f                picker: what needs you → jump there (ctrl-x remove, alt-x clean merged)
  f add [repo...]  add repos to this feature
  f prs [-r] [-c]  this feature's PRs (-r refresh, -c copy)

f <cmd> -h for flags. Plumbing used by pi/nvim: f goal, f comment.
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
				// Called by agent/extensions/guard.ts when pi starts.
				Name:   "creds",
				Hidden: true,
				Action: func(ctx context.Context, c *cli.Command) error { return creds(ctx) },
			},
			{
				// Called by agent/extensions/guard.ts before every bash command.
				Name:   "guard",
				Hidden: true,
				Commands: []*cli.Command{{
					Name: "check",
					Action: func(ctx context.Context, c *cli.Command) error {
						return guardCheck(strings.Join(c.Args().Slice(), " "))
					},
				}},
			},
			{
				// Called by agent/extensions/f.ts around each pi run.
				Name:   "checkpoint",
				Hidden: true,
				Commands: []*cli.Command{
					{Name: "begin", Action: func(ctx context.Context, c *cli.Command) error {
						name, err := resolve(d, "")
						if err != nil {
							return err
						}
						return feature.CheckpointBegin(ctx, d.run, d.layout, name)
					}},
					{Name: "end", Action: func(ctx context.Context, c *cli.Command) error {
						name, err := resolve(d, "")
						if err != nil {
							return err
						}
						_, err = feature.CheckpointEnd(ctx, d.run, d.layout, name)
						return err
					}},
				},
			},
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
				Name:   "comment",
				Hidden: true,
				Usage:  "review comments for pi (nvim: <leader>pc / <leader>ps)",
				Flags:  []cli.Flag{featureFlag},
				Commands: []*cli.Command{
					{Name: "add", Usage: "add a comment (JSON on stdin)", Flags: []cli.Flag{featureFlag},
						Action: func(ctx context.Context, c *cli.Command) error {
							return commentAdd(d, c.String("feature"), os.Stdin)
						}},
					{Name: "list", Usage: "pending comments", Flags: []cli.Flag{featureFlag, &cli.BoolFlag{Name: "json"}},
						Action: func(ctx context.Context, c *cli.Command) error {
							return commentList(d, c.String("feature"), c.Bool("json"))
						}},
					{Name: "send", Usage: "send them to pi and clear", Flags: []cli.Flag{featureFlag},
						Action: func(ctx context.Context, c *cli.Command) error {
							return commentSend(ctx, d, c.String("feature"))
						}},
					{Name: "clear", Usage: "discard them", Flags: []cli.Flag{featureFlag},
						Action: func(ctx context.Context, c *cli.Command) error {
							return commentClear(d, c.String("feature"))
						}},
				},
			},
			{
				Name:      "goal",
				Hidden:    true,
				Usage:     "show or set this feature's goal",
				ArgsUsage: "[text]",
				Flags:     []cli.Flag{featureFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return goal(d, c.String("feature"), strings.Join(c.Args().Slice(), " "))
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
					&cli.BoolFlag{Name: "copy", Aliases: []string{"c"}, Usage: "copy url: title lines"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return prs(ctx, d, c.String("feature"), prsOptions{
						refresh: c.Bool("refresh"),
						noOpen:  c.Bool("no-open") || c.Bool("json"),
						asJSON:  c.Bool("json"),
						addURL:  c.String("add"),
						copy:    c.Bool("copy"),
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
