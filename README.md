# pi

My config for [pi](https://pi.dev) and the feature-workspace setup around it
(Herdr + nvim + beads). See [PLAN.md](PLAN.md).

## Install

```bash
./install.sh                    # link pi config + nvim module, go install ./cmd/f
herdr integration install pi    # herdr agent-state extension (not tracked)
```

## Layout

- `agent/`: pi config (settings, AGENTS.md, prompts, extensions, skills, themes)
- `cmd/f/`: `f`, the multi-repo feature workspace CLI (`f --help`)
- `mockups/`: HTML layout mockups
