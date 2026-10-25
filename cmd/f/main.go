// Command f creates and manages multi-repo feature workspaces:
// git worktrees + a generated AGENTS.md + the agent (in a Herdr workspace when inside Herdr).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/herdr"
	"github.com/seanturner026/pi/internal/tracing"
)

const help = `f: one agent session (Claude Code, or pi with F_AGENT=pi) across several repos, each on its own worktree.

  f <name>         start a feature (pick repos), or reopen it
  f                picker: what needs you → jump there (ctrl-x remove)
  f add [repo...]  add repos to this feature
  f resume         after a Herdr restart: restart the agent, nvim, prs in every feature workspace
  f prs [-r] [-c]  this feature's PRs (-r refresh, -c copy, -w live view)
  f ci fix         send failing checks to the agent (it proposes a fix, doesn't push)
  f sync           after a PR merges: reset/drop merged branches, rebase the rest onto main
  f restore [name] bring back a removed feature (14 days); no name lists the trash
  f trace [stats]  where f spends time: Jaeger (Docker), or stats as text

f <cmd> -h for flags. Plumbing used by the agent/nvim: f goal, f comment.
`

// sinceFlag bounds f trace to recent spans.
var sinceFlag = &cli.DurationFlag{Name: "since", Value: 24 * time.Hour, Usage: "spans that ended this recently"}

// featureFlag picks a feature explicitly; otherwise it's inferred from the cwd.
var featureFlag = &cli.StringFlag{Name: "feature", Aliases: []string{"f"}, Usage: "feature (default: from cwd)"}

func main() {
	tracing.Init()
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
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.NArg() > 1 {
				return fmt.Errorf("one feature name at a time")
			}
			return start(ctx, d, c.Args().First(), c.StringSlice("repo"))
		},
		ShellComplete: func(ctx context.Context, c *cli.Command) { printLines(feature.List(d.layout)) },
		Commands: []*cli.Command{
			{
				// Called by agent/extensions/f.ts after each pi run: beads progress for the picker.
				Name:   "plan",
				Hidden: true,
				Commands: []*cli.Command{
					{
						Name:   "sync",
						Action: func(ctx context.Context, c *cli.Command) error { return planSync(ctx, d) },
					},
					{
						// /plan in pi: steps grouped by in progress / ready / blocked / done.
						Name:   "show",
						Flags:  []cli.Flag{&cli.BoolFlag{Name: "tree", Usage: "dependency tree (/plan in Claude Code)"}},
						Action: func(ctx context.Context, c *cli.Command) error { return planShow(ctx, d, c.Bool("tree")) },
					},
				},
			},
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
				Name:   "resume",
				Usage:  "after a Herdr restart: restart the agent, nvim, review, prs in every feature workspace",
				Action: func(ctx context.Context, c *cli.Command) error { return resume(ctx, d) },
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
				Usage:  "review comments for the agent (nvim: <leader>pc / <leader>ps)",
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
					{Name: "send", Usage: "send them to the agent and clear", Flags: []cli.Flag{featureFlag},
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
					return goal(ctx, d, c.String("feature"), strings.Join(c.Args().Slice(), " "))
				},
			},
			{
				// Deletes removed features older than feature.TrashRetention (f sync --all runs it too).
				Name:   "empty-trash",
				Hidden: true,
				Action: func(ctx context.Context, c *cli.Command) error {
					return feature.EmptyTrash(ctx, d.run, d.layout, feature.TrashRetention)
				},
			},
			{
				Name:  "trace",
				Usage: "where f spends time: open its traces in Jaeger (Docker)",
				Flags: []cli.Flag{sinceFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return traceOpen(ctx, d, c.Duration("since"))
				},
				Commands: []*cli.Command{
					{
						Name:   "stop",
						Usage:  "stop the Jaeger container",
						Action: func(ctx context.Context, c *cli.Command) error { return traceStop(ctx, d) },
					},
					{
						Name:   "stats",
						Usage:  "per span: count, p50, p95, max; then the slowest runs (no Docker)",
						Flags:  []cli.Flag{sinceFlag},
						Action: func(ctx context.Context, c *cli.Command) error { return traceStats(c.Duration("since")) },
					},
				},
			},
			{
				Name:      "restore",
				Usage:     "bring back a removed feature (kept for 14 days); no name lists the trash",
				ArgsUsage: "[feature]",
				Action: func(ctx context.Context, c *cli.Command) error {
					return restore(ctx, d, c.Args().First())
				},
				ShellComplete: func(ctx context.Context, c *cli.Command) {
					var names []string
					for _, t := range feature.Trash(d.layout) {
						names = append(names, t.Name)
					}
					printLines(names)
				},
			},
			{
				// Started by f <new feature> and f add: fetch and add worktrees, then
				// the beads issue, while the workspace is already open.
				Name:      "setup",
				Hidden:    true,
				ArgsUsage: "repo...",
				Flags:     []cli.Flag{featureFlag},
				Action: func(ctx context.Context, c *cli.Command) error {
					return setup(ctx, d, c.String("feature"), c.Args().Slice())
				},
			},
			{
				// Called by the Claude Code mod (claude/f) before a turn: wait for f setup.
				Name:   "setup-wait",
				Hidden: true,
				Action: func(ctx context.Context, c *cli.Command) error { return setupWait(ctx, d) },
			},
			{
				// Started by the picker after removing a feature (bd is slow).
				Name:   "close-issue",
				Hidden: true,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "issue"}, &cli.StringFlag{Name: "name"},
					&cli.StringFlag{Name: "reason"}, &cli.StringFlag{Name: "note"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return closeIssueCmd(ctx, d, c.String("issue"), c.String("name"), c.String("reason"), c.String("note"))
				},
			},
			{
				// The f Herdr plugin's open-picker action (ctrl+space).
				Name:   "open-picker",
				Hidden: true,
				Action: func(ctx context.Context, c *cli.Command) error { return herdr.OpenPicker(ctx, d.run, d.socket) },
			},
			{
				Name:  "sync",
				Usage: "after a PR merges: reset its repo to main (or rebase newer commits onto it)",
				Flags: []cli.Flag{
					featureFlag,
					&cli.BoolFlag{Name: "background", Hidden: true},
					&cli.BoolFlag{Name: "all", Usage: "every feature (launchd, every 30 min): refreshes all; resets wait for the one you're in and busy pis; never pushes"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Bool("all") {
						return syncAll(ctx, d)
					}
					return syncFeature(ctx, d, c.String("feature"), c.Bool("background"))
				},
				Commands: []*cli.Command{{
					// Called by agent/extensions/f.ts before each prompt.
					Name:   "notes",
					Hidden: true,
					Flags:  []cli.Flag{featureFlag, &cli.BoolFlag{Name: "take"}},
					Action: func(ctx context.Context, c *cli.Command) error { return takeSyncNotes(d, c.String("feature")) },
				}},
			},
			{
				// Bound to cmd+ctrl+<key> (WezTerm → ctrl+alt → Herdr shell command).
				Name:      "tab",
				Hidden:    true,
				ArgsUsage: "<key> (c chat · v nvim · a s e r g repos · p prs · t shell)",
				Action: func(ctx context.Context, c *cli.Command) error {
					return herdr.FocusTab(ctx, d.run, c.Args().First())
				},
			},
			{
				Name:  "ci",
				Usage: "CI on this feature's PRs",
				Commands: []*cli.Command{{
					Name:  "fix",
					Usage: "send failing checks (with failed job logs) to the agent: find the cause, propose a fix, don't push",
					Flags: []cli.Flag{featureFlag, &cli.BoolFlag{Name: "print", Usage: "print the prompt instead (for /ci)"}},
					Action: func(ctx context.Context, c *cli.Command) error {
						return ciFix(ctx, d, c.String("feature"), c.Bool("print"))
					},
				}},
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
					&cli.BoolFlag{Name: "watch", Aliases: []string{"w"}, Usage: "live view with checks (the prs tab)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Bool("watch") {
						name, err := resolve(d, c.String("feature"))
						if err != nil {
							return err
						}
						return watchPRs(ctx, d, name)
					}
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
	traceActions(app)
	if err := app.Run(context.Background(), os.Args); err != nil {
		fail(err)
	}
	tracing.Shutdown()
}

// traceActions gives every command's run a root span named after it ("f sync",
// "f checkpoint begin"); f sync --all keeps its flag, a different job.
func traceActions(cmd *cli.Command) {
	if cmd.Name == "trace" {
		return // f trace itself would only add noise to what it shows
	}
	if action := cmd.Action; action != nil {
		cmd.Action = func(ctx context.Context, c *cli.Command) error {
			name := c.FullName()
			if c.Name == "sync" && c.Bool("all") {
				name += " --all"
			}
			ctx, span := tracing.Root(ctx, name)
			span.SetAttributes(attribute.StringSlice("f.flags", c.LocalFlagNames()))
			err := action(ctx, c)
			tracing.End(span, err)
			return err
		}
	}
	for _, sub := range cmd.Commands {
		traceActions(sub)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%s %v\n", red("error:"), err)
	tracing.Shutdown()
	os.Exit(1)
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}
