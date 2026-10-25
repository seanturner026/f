/**
 * f: Claude Code port of the pi extensions (agent/extensions/f.ts, guard.ts,
 * copy-code.ts). Everything goes through the `f` CLI; this mod only reads
 * `.context/feature.json` to know it's in a feature.
 *
 * - Guard: `f creds` env at start, `f guard check` before each Bash call.
 * - Goal + checkpoints around each turn; notes from f ride the prompt.
 * - Setup: a new feature opens before its worktrees exist; a turn waits for
 *   `f setup` (state.json "setup") via `f setup-wait`.
 * - PRs: refreshed after each turn, `gh pr create` output recorded (`f prs --add`);
 *   claude/statusline.sh draws them.
 * - The feature is the session's root: Bash `cd` moves Claude Code's cwd, unlike pi's.
 * - Commands that never reach the model: /prs /prc /sync /plan /f /code /slack.
 *   /ci sends failing checks to the model. /plan replaces Claude Code's (shift+tab still reaches plan mode).
 * - Quieter chrome: no "Worked for …" line, no hint/mode row under the prompt while idle.
 */
import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Pick, PickOption } from '../types'
import { codeBlocks, toSlack } from './markdown'

type $ = EngineInterface
type FeatureInfo = { name: string; root: string; repos: string[]; goal?: string; issue?: string }
type Creds = { env: Record<string, string> }
type Verdict = { block: boolean; reason?: string }

const PICK_PANE = 'f-pick'
const PLAN_PANE = 'f-plan'
// f plan show --tree's status marks: done, in progress, ready, blocked, deferred.
const MARK_COLORS: Record<string, string> = { '✓': 'success', '◐': 'suggestion', '○': 'text', '●': 'warning', '❄': 'subtle' }
const PR_URL = /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/pull\/\d+/g

const plan = atom({ plugin: 'f', key: 'plan' } as const, null, { shape: 'plan-1' })
const pick = atom({ plugin: 'f', key: 'pick' } as const, null, { shape: 'pick-1' })

async function f($: $, args: string[], timeoutMs = 30_000) {
  try {
    // F_CALLER: f's traces record who called it (f trace stats).
    const res = await $.process.run(['f', ...args], { cwd: await $.session.root(), env: { F_CALLER: 'claude' }, timeoutMs })
    return { code: res.exitCode, stdout: res.stdout, stderr: res.stderr }
  } catch (err) {
    return { code: 1, stdout: '', stderr: String(err) }
  }
}

async function featureInfo($: $): Promise<FeatureInfo | undefined> {
  try {
    return JSON.parse(await $.fs.read(`${await $.session.root()}/.context/feature.json`)) as FeatureInfo
  } catch {
    return undefined
  }
}

// refreshPRs updates the feature's PRs and checks from GitHub (state.json);
// claude/statusline.sh draws them on its next refresh.
async function refreshPRs($: $) {
  if (await featureInfo($)) await f($, ['prs', '--json', '-r'])
}

// refreshPlan redraws the plan pane, if it's open, from beads.
async function refreshPlan($: $) {
  if (!(await $.ui.panes()).some(p => p.id === PLAN_PANE)) return
  const res = await f($, ['plan', 'show', '--tree'])
  if (res.code === 0) await update($, plan, () => res.stdout.trimEnd())
}

async function waitForSetup($: $) {
  let setup: { repos: string[]; pending: string[] } | undefined
  try {
    setup = (JSON.parse(await $.fs.read(`${await $.session.root()}/.context/state.json`)) as { setup?: typeof setup }).setup
  } catch {
    return
  }
  if (!setup) return
  $.ui.toast(`setting up worktrees… (${setup.repos.length - setup.pending.length}/${setup.repos.length})`)
  // A process in flight doesn't count against the hook's budget; a sleep loop here would.
  const res = await f($, ['setup-wait'], 6 * 60_000)
  if (res.code !== 0) $.ui.toast(`f setup-wait: ${res.stderr.trim()}`)
}

// background runs work after the hook returns, so bookkeeping never holds up a turn.
function background($: $, work: () => Promise<unknown>) {
  void $.clock.after(0, () => work().catch(() => {}))
}

async function lastReply($: $): Promise<string | undefined> {
  const messages = await $.session.messages()
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m.role === 'assistant' && m.text.trim()) return m.text
  }
  return undefined
}

function summarize(out: string, fallback: string): string {
  return out.trim().split('\n').pop() || fallback
}

async function copy($: $, text: string, what: string): Promise<string> {
  const res = await $.ui.copy({ text })
  return res.isCopied ? what : `copy failed: ${res.reason}`
}

async function copyBlocks($: $, codes: string[]): Promise<string> {
  const out = codes.join('\n\n')
  const lines = out.split('\n').length
  return copy($, out, `copied ${codes.length > 1 ? `${codes.length} blocks, ` : ''}${lines} line${lines === 1 ? '' : 's'}`)
}

async function openPick($: $, value: Pick) {
  await update($, pick, () => value)
  const widest = Math.max(20, ...value.options.map(o => o.label.length + 8))
  await $.ui.open({ id: PICK_PANE, title: value.kind === 'code' ? 'Copy which code block?' : value.kind === 'feature' ? 'Open which feature?' : `${value.name}: repos`, focus: true, closeOnEscape: true, rows: value.options.length + 4, columns: Math.min(widest, 80) })
}

async function closePick($: $) {
  await $.ui.close({ id: PICK_PANE })
  await update($, pick, () => null)
}

async function openFeature($: $, name: string, repos: string[]): Promise<string> {
  $.ui.toast(`f ${name}…`)
  const res = await f($, [name, ...repos.flatMap(r => ['-r', r])], 180_000)
  const out = (res.stdout + res.stderr).trim()
  return res.code === 0 ? summarize(out, `opened ${name}`) : out || `f ${name} failed`
}

async function subdirs($: $, dir: string, keep: (name: string) => Promise<boolean>): Promise<string[]> {
  try {
    const entries = await $.fs.list(dir)
    const names: string[] = []
    for (const e of entries) if ((e.kind === 'dir' || e.isLink) && !e.name.startsWith('.') && (await keep(e.name))) names.push(e.name)
    return names.sort()
  } catch {
    return []
  }
}

async function base($: $): Promise<string> {
  return (await $.env.get('F_BASE')) || `${await $.env.get('HOME')}/code/features`
}

const featureNames = async ($: $) => subdirs($, `${await base($)}/.features`, async () => true)

async function baseRepos($: $): Promise<string[]> {
  const root = await base($)
  return subdirs($, root, async n => {
    try {
      await $.fs.stat(`${root}/${n}/.git`)
      return true
    } catch {
      return false
    }
  })
}

const asOptions = (values: string[]): PickOption[] => values.map(v => ({ value: v, label: v }))

export const register: Register = on => {
  let guardError = 'starting'
  let running = false

  on('session.start', async ($, e, next) => {
    const commands = [
      { name: 'prs', description: "Refresh this feature's PRs and open the open ones in the browser" },
      { name: 'prc', description: "Copy this feature's PRs as url: title lines (stacks numbered)" },
      { name: 'sync', description: 'After a PR merges: reset its repo to main (or rebase newer commits onto it)' },
      { name: 'ci', description: "Send this feature's failing CI checks (with logs): find the cause, propose a fix, don't push" },
      { name: 'f', description: 'Open a feature, or start one: /f [name] [repo...]', argumentHint: '[name] [repo...]' },
      { name: 'code', description: 'Copy code block(s) from the last reply, unwrapped: /code [n]', argumentHint: '[n]' },
      { name: 'slack', description: 'Copy the last reply formatted for Slack (*bold*, plain headings and links)' },
    ]
    for (const c of commands) await $.command.register(c)

    const creds = await f($, ['creds'])
    if (creds.code !== 0) {
      guardError = creds.stderr.trim() || `f creds exited ${creds.code}`
      $.ui.toast(`guard: ${guardError}; Bash is blocked`)
    } else {
      const env = (JSON.parse(creds.stdout) as Creds).env
      await $.env.set('AWS_CONFIG_FILE', env.AWS_CONFIG_FILE || undefined)
      await $.env.set('AWS_SHARED_CREDENTIALS_FILE', env.AWS_SHARED_CREDENTIALS_FILE || undefined)
      await $.env.set('KUBECONFIG', env.KUBECONFIG || undefined)
      await $.env.set('AWS_PROFILE', env.AWS_PROFILE || undefined)
      guardError = ''
    }

    const info = await featureInfo($)
    if (info) {
      background($, async () => {
        await refreshPRs($)
        if (info.issue) await f($, ['plan', 'sync'])
      })
    }
    return next(e)
  })

  // --- guard + PR recording ------------------------------------------------------

  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    if (guardError) return { deny: `guard not set up: ${guardError}` }
    const res = await f($, ['guard', 'check', '--', e.command], 10_000)
    if (res.code !== 0) return { deny: `guard check failed: ${res.stderr.trim()}` }
    const v = JSON.parse(res.stdout) as Verdict
    if (v.block) return { deny: `guard: ${v.reason}` }

    const ran = await next(e)
    if (ran.deny === undefined && !ran.isError && /\bgh\s+pr\s+create\b/.test(e.command)) {
      const urls = [...new Set(ran.text?.match(PR_URL) ?? [])]
      background($, async () => {
        for (const url of urls) await f($, ['prs', '--add', url])
      })
    }
    // Claude edits the plan with bd: keep an open plan pane live.
    if (/\bbd\s/.test(e.command)) background($, () => refreshPlan($))
    return ran
  }).catch(($, e, next) => (next.called ? next(e) : { deny: 'guard: the f mod failed' }))

  // --- goal + checkpoints around each turn ---------------------------------------

  on('prompt.submit', async ($, e, next) => {
    if (e.command) return next(e)
    const info = await featureInfo($)
    if (!info) return next(e)
    const context = [...(e.context ?? [])]
    if (!running) {
      running = true // follow-ups within one turn share its snapshot
      const prompt = e.text.trim()
      await waitForSetup($) // the snapshot below needs the worktrees
      if (!info.goal && prompt) background($, () => f($, ['goal', prompt]))
      // Awaited: the snapshot must exist before Claude starts editing.
      const res = await f($, ['checkpoint', 'begin'])
      if (res.code !== 0) $.ui.toast(`f checkpoint: ${res.stderr.trim()}`)
    }
    const notes = await f($, ['sync', 'notes', '--take'])
    if (notes.code === 0 && notes.stdout.trim()) {
      context.push(`Notes from f since your last turn (re-read files before editing them; read any instruction files named here in full before working in that repo):\n${notes.stdout.trim()}`)
    }
    return next({ ...e, context })
  })

  on('turn.complete', async ($, e, next) => {
    if (running) {
      running = false
      await f($, ['checkpoint', 'end'])
      const info = await featureInfo($)
      background($, async () => {
        await refreshPRs($)
        if (info?.issue) await f($, ['plan', 'sync'])
        await refreshPlan($)
      })
    }
    return next(e)
  })

  // --- commands ------------------------------------------------------------------

  on('command.run', { command: 'prs' }, async $ => {
    if (!(await featureInfo($))) return { text: 'not in an f feature' }
    const res = await f($, ['prs', '-r'])
    return { text: res.code === 0 ? 'opened PRs' : res.stderr.trim() || 'f prs failed' }
  })

  on('command.run', { command: 'prc' }, async $ => {
    if (!(await featureInfo($))) return { text: 'not in an f feature' }
    const res = await f($, ['prs', '-c'])
    const out = (res.stdout + res.stderr).trim()
    return { text: res.code === 0 ? summarize(out, 'copied') : out || 'f prs -c failed' }
  })

  on('command.run', { command: 'sync' }, async $ => {
    if (!(await featureInfo($))) return { text: 'not in an f feature' }
    const res = await f($, ['sync'], 120_000)
    return { text: (res.stdout + res.stderr).trim() || 'nothing to sync' }
  })

  // Answers without next: Claude Code's own /plan never runs.
  on('command.run', { command: 'plan' }, async $ => {
    if ((await $.ui.panes()).some(p => p.id === PLAN_PANE)) {
      await $.ui.close({ id: PLAN_PANE })
      return { text: 'plan hidden' }
    }
    if (!(await featureInfo($))) return { text: 'not in an f feature' }
    const res = await f($, ['plan', 'show', '--tree'])
    if (res.code !== 0) return { text: res.stderr.trim() || 'f plan show failed' }
    await update($, plan, () => res.stdout.trimEnd())
    await $.ui.open({ id: PLAN_PANE, title: 'Plan' })
    return { text: 'plan in the sidebar, updated as it changes (/plan again to hide)' }
  })

  on('command.run', { command: 'ci' }, async $ => {
    if (!(await featureInfo($))) return { text: 'not in an f feature' }
    $.ui.toast('checking CI…')
    const res = await f($, ['ci', 'fix', '--print'], 120_000)
    if (res.code !== 0) return { text: res.stderr.trim() || 'f ci fix failed' }
    const prompt = res.stdout.trim()
    if (!prompt) return { text: 'no failing checks' }
    // Queued after the command returns: submit resolves when its turn starts, which waits for this command.
    background($, () => $.prompt.submit({ text: prompt }))
    return { text: 'sending failing checks…' }
  })

  on('command.run', { command: 'f' }, async ($, e) => {
    const [name, ...repos] = e.args.trim().split(/\s+/).filter(Boolean)
    if (!name) {
      await openPick($, { kind: 'feature', options: asOptions(await featureNames($)) })
      return { text: 'pick a feature' }
    }
    if (!(await featureNames($)).includes(name) && repos.length === 0) {
      await openPick($, { kind: 'repos', name, options: asOptions(await baseRepos($)), chosen: [] })
      return { text: `pick repos for ${name}` }
    }
    return { text: await openFeature($, name, repos) }
  })

  on('command.run', { command: 'code' }, async ($, e) => {
    const text = await lastReply($)
    const blocks = text ? codeBlocks(text) : []
    if (blocks.length === 0) return { text: 'no code blocks in the last reply' }
    const n = Number.parseInt(e.args.trim(), 10)
    if (!Number.isNaN(n)) {
      const block = blocks[n - 1]
      if (!block) return { text: `there are ${blocks.length} code blocks` }
      return { text: await copyBlocks($, [block.code]) }
    }
    if (blocks.length === 1) return { text: await copyBlocks($, blocks.map(b => b.code)) }
    const options = blocks.map((b, i) => {
      const first = b.code.split('\n')[0].trim()
      return { value: String(i), label: `${b.lang ? `[${b.lang}] ` : ''}${first.length > 70 ? `${first.slice(0, 69)}…` : first}` }
    })
    await openPick($, { kind: 'code', options: [...options, { value: 'all', label: `all ${blocks.length}` }] })
    return { text: 'pick a code block' }
  })

  on('command.run', { command: 'slack' }, async $ => {
    const text = await lastReply($)
    if (!text) return { text: 'no reply to copy' }
    return { text: await copy($, toSlack(text), 'copied for Slack') }
  })

  // --- drawing -------------------------------------------------------------------

  on('ui.render', { component: 'Pane', requestId: PICK_PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const p = await read($, pick)
    if (!p) return <Text dimColor>nothing to pick</Text>

    const choose = async (value: string) => {
      if (p.kind === 'code') {
        await closePick($)
        const blocks = codeBlocks((await lastReply($)) ?? '').filter((_b, i) => value === 'all' || i === Number(value))
        $.ui.toast(blocks.length ? await copyBlocks($, blocks.map(b => b.code)) : 'that code block is gone')
      } else if (p.kind === 'feature') {
        await closePick($)
        $.ui.toast(await openFeature($, value, []))
      } else if (value === '\u0000done') {
        await closePick($)
        $.ui.toast(await openFeature($, p.name, p.chosen))
      } else {
        await update($, pick, cur => (cur?.kind === 'repos' ? { ...cur, chosen: cur.chosen.includes(value) ? cur.chosen.filter(r => r !== value) : [...cur.chosen, value] } : cur))
      }
    }

    const options = p.kind === 'repos' && p.chosen.length ? [{ value: '\u0000done', label: `✓ done (${p.chosen.join(', ')})` }, ...p.options] : p.options
    const hotkeys = '123456789abcdefghijklmnopqrstuvwxyz'
    return (
      <Box flexDirection="column">
        {options.map((o, i) => (
          <Button key={o.value} plain hotkey={hotkeys[i]} autoFocus={i === 0 ? true : undefined} onPress={() => choose(o.value)}>
            {p.kind === 'repos' && p.chosen.includes(o.value) ? `[x] ${o.label}` : p.kind === 'repos' && o.value !== '\u0000done' ? `[ ] ${o.label}` : o.label}
          </Button>
        ))}
        <Text dimColor>esc to cancel</Text>
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PLAN_PANE }, async ($, e) => {
    const { Box, Text } = $.ui.resolve(e)
    const text = await read($, plan)
    if (!text) return <Text dimColor>no plan</Text>
    // Tree lines dim, the mark colored by status, the title wrapping in its own column.
    // Fixed-width columns that never shrink: a Text's trailing spaces get
    // collapsed, and a squeezed column wraps (tree lines split across rows).
    const rows = text.split('\n').filter(l => l.trim())
    return (
      <Box flexDirection="column" width={e.props.bodyColumns}>
        {rows.map((l, i) => {
          if (i === 0) return <Text bold>{l}</Text>
          const [, tree = '', rest = l] = /^([ │├└─]*)(.*)$/.exec(l) ?? []
          const step = /^([○●◐❄✓]) (\S+)\s+(.*)$/.exec(rest)
          return (
            <Box flexDirection="row">
              {tree && (
                <Box width={tree.length} flexShrink={0}>
                  <Text dimColor>{tree.trimEnd()}</Text>
                </Box>
              )}
              {step ? (
                <Box flexDirection="row" flexShrink={1}>
                  <Box width={2} flexShrink={0}>
                    <Text color={MARK_COLORS[step[1] ?? '']}>{step[1]}</Text>
                  </Box>
                  <Box width={(step[2] ?? '').length + 2} flexShrink={0}>
                    <Text dimColor>{step[2]}</Text>
                  </Box>
                  <Box flexShrink={1}>
                    <Text wrap="wrap">{step[3]}</Text>
                  </Box>
                </Box>
              ) : (
                <Box flexShrink={1}>
                  <Text dimColor wrap="wrap">{rest}</Text>
                </Box>
              )}
            </Box>
          )
        })}
      </Box>
    )
  })

  // Quieter chrome, closer to pi: no "Worked for 47s" row (ctrl+o still shows it).
  on('ui.render', { component: 'TurnDuration' }, ($, e, next) => {
    if (e.props.isExpanded) return next(e)
    const { Box } = $.ui.resolve(e)
    return <Box />
  })

  // The row under the prompt (mode pill, hints) only while a turn runs; the statusline covers the rest.
  on('ui.render', { component: 'PromptHint' }, ($, e, next) => {
    if (e.props.isWorking) return next(e)
    const { Box } = $.ui.resolve(e)
    return <Box />
  })
}
