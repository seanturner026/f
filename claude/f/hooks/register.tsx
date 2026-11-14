/**
 * f: the Claude Code side of f features. Everything goes through the `f` CLI;
 * this mod only reads `.context/feature.json` to know it's in a feature.
 *
 * - Guard: `f creds` env at start, `f guard check` before each Bash call.
 * - Goal + checkpoints around each turn; notes from f ride the prompt.
 * - Setup: a new feature opens before its worktrees exist; a turn waits for
 *   `f setup` (state.json "setup") via `f setup-wait`.
 * - PRs: refreshed after each turn, `gh pr create` output recorded (`f prs --add`);
 *   claude/statusline.sh draws them.
 * - The feature is the session's root: Bash `cd` moves Claude Code's cwd.
 * - One command, /f <cmd> (SUBCOMMANDS; bare /f lists them). It runs mid-turn; /f sync
 *   waits for a running turn. ci, sr and prr send a prompt to the model; the rest don't.
 * - Quieter chrome: no "Worked for …" line, no hint/mode row under the prompt while idle.
 */
import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Pick, PickOption } from '../types'
import { codeBlocks, slackText } from './markdown'

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
const pick = atom({ plugin: 'f', key: 'pick' } as const, null, { shape: 'pick-2' })

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

// togglePlan opens the plan pane with the last plan drawn, then refreshes it
// from beads (bd is slow), or closes it.
async function togglePlan($: $): Promise<string> {
  if ((await $.ui.panes()).some(p => p.id === PLAN_PANE)) {
    await $.ui.close({ id: PLAN_PANE })
    return 'plan hidden'
  }
  await $.ui.open({ id: PLAN_PANE, title: 'Plan' })
  background($, () => refreshPlan($))
  return 'plan in the sidebar, updated as it changes (/f plan again to hide)'

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
  await $.ui.open({ id: PICK_PANE, title: value.kind === 'code' ? 'Copy which code block?' : 'f commands', focus: true, closeOnEscape: true, rows: value.options.length + 4, columns: Math.min(widest, 90) })
}

async function closePick($: $) {
  await $.ui.close({ id: PICK_PANE })
  await update($, pick, () => null)
}

// --- /f subcommands ----------------------------------------------------------------

// sendPrompt submits text as the user's next message: queued until the turn
// it starts, so it never interrupts one.
function sendPrompt($: $, text: string) {
  background($, () => $.prompt.submit({ text }))
}

async function openPRs($: $): Promise<string> {
  const res = await f($, ['prs']) // saved PRs at once: a live lookup is several slow gh calls
  background($, () => refreshPRs($))
  return res.code === 0 ? 'opened PRs' : res.stderr.trim() || 'f prs failed'
}

async function copyPRs($: $): Promise<string> {
  const res = await f($, ['prs', '-c'])
  const out = (res.stdout + res.stderr).trim()
  return res.code === 0 ? summarize(out, 'copied') : out || 'f prs -c failed'
}

async function sendReviewThreads($: $, text: string): Promise<string> {
  $.ui.toast('reading review threads…')
  const res = await f($, ['prs', 'review'], 120_000)
  if (res.code !== 0) return res.stderr.trim() || 'f prs review failed'
  const prompt = [text, res.stdout.trim()].filter(Boolean).join('\n\n')
  if (!prompt) return 'no unresolved review threads'
  sendPrompt($, prompt)
  return res.stdout.trim() ? 'sending review threads…' : 'no unresolved review threads; sending your text'
}

async function sendReviewComments($: $, text: string): Promise<string> {
  const res = await f($, ['comment', 'send', '--print'], 60_000)
  if (res.code !== 0) return res.stderr.trim() || 'f comment send failed'
  const prompt = [text, res.stdout.trim()].filter(Boolean).join('\n\n')
  if (!prompt) return 'no review comments'
  sendPrompt($, prompt)
  return res.stdout.trim() ? 'sending review comments…' : 'no review comments; sending your text'
}

async function sendFailingChecks($: $): Promise<string> {
  $.ui.toast('checking CI…')
  const res = await f($, ['ci', 'fix', '--print'], 120_000)
  if (res.code !== 0) return res.stderr.trim() || 'f ci fix failed'
  if (!res.stdout.trim()) return 'no failing checks'
  sendPrompt($, res.stdout.trim())
  return 'sending failing checks…'
}

async function syncFeature($: $): Promise<string> {
  const res = await f($, ['sync'], 120_000)
  return (res.stdout + res.stderr).trim() || 'nothing to sync'
}

async function copyCode($: $, args: string): Promise<string> {
  const text = await lastReply($)
  const blocks = text ? codeBlocks(text) : []
  if (blocks.length === 0) return 'no code blocks in the last reply'
  const n = Number.parseInt(args, 10)
  if (!Number.isNaN(n)) {
    const block = blocks[n - 1]
    return block ? copyBlocks($, [block.code]) : `there are ${blocks.length} code blocks`
  }
  if (blocks.length === 1) return copyBlocks($, blocks.map(b => b.code))
  const options = blocks.map((b, i) => {
    const first = b.code.split('\n')[0].trim()
    return { value: String(i), label: `${b.lang ? `[${b.lang}] ` : ''}${first.length > 70 ? `${first.slice(0, 69)}…` : first}` }
  })
  await openPick($, { kind: 'code', options: [...options, { value: 'all', label: `all ${blocks.length}` }] })
  return 'pick a code block'
}

async function copySlack($: $): Promise<string> {
  const text = await lastReply($)
  return text ? copy($, slackText(text), 'copied for Slack') : 'no reply to copy'
}

type Subcommand = {
  name: string
  usage: string
  description: string
  feature: boolean // needs an f feature
}

// The /f subcommands, in the order bare /f lists them.
const SUBCOMMANDS: Subcommand[] = [
  { name: 'prs', usage: 'prs', description: "open the feature's open PRs", feature: true },
  { name: 'prc', usage: 'prc', description: 'copy them as url: title lines', feature: true },
  { name: 'prr', usage: 'prr [text]', description: '+ unresolved GitHub review threads → the agent', feature: true },
  { name: 'sr', usage: 'sr [text]', description: '+ tuicr/nvim review comments → the agent', feature: true },
  { name: 'ci', usage: 'ci', description: 'failing checks → the agent: fix and push', feature: true },
  { name: 'sync', usage: 'sync', description: 'after a merge: reset/rebase branches (after a running turn)', feature: true },
  { name: 'plan', usage: 'plan', description: 'show/hide the plan sidebar', feature: true },
  { name: 'code', usage: 'code [n]', description: 'copy code block(s) from the last reply', feature: false },
  { name: 'slack', usage: 'slack', description: "copy the last reply's quoted draft for Slack", feature: false },
]

// Turn is the session's turn state: whether a run is going, and a /f sync
// held back until it ends (sync moves branches under the agent).
type Turn = { running: boolean; syncAfter: boolean }

// dispatch runs /f <cmd>.
async function dispatch($: $, turn: Turn, sub: Subcommand, args: string): Promise<string> {
  if (sub.feature && !(await featureInfo($))) return 'not in an f feature'
  if (sub.name === 'sync' && turn.running) {
    turn.syncAfter = true
    return 'syncing when this turn ends'
  }
  switch (sub.name) {
    case 'prs':
      return openPRs($)
    case 'prc':
      return copyPRs($)
    case 'prr':
      return sendReviewThreads($, args)
    case 'sr':
      return sendReviewComments($, args)
    case 'ci':
      return sendFailingChecks($)
    case 'sync':
      return syncFeature($)
    case 'plan':
      return togglePlan($)
    case 'code':
      return copyCode($, args)
    case 'slack':
      return copySlack($)
  }
  return `no /f ${sub.name}`
}

export const register: Register = on => {
  let guardError = 'starting'
  const turn: Turn = { running: false, syncAfter: false }
  let turnPrompt = '' // names the run's turn in the review (f checkpoint end --prompt)


  on('session.start', async ($, e, next) => {
    await $.command.register({ name: 'f', description: `f: ${SUBCOMMANDS.map(c => c.name).join(' · ')} (bare /f lists them)`, argumentHint: '<cmd> [args]', immediate: true })

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
    if (!turn.running) {
      turn.running = true // follow-ups within one turn share its snapshot
      const prompt = e.text.trim()
      turnPrompt = prompt
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
    if (turn.running) {
      turn.running = false
      await f($, ['checkpoint', 'end', '--prompt', turnPrompt])
      if (turn.syncAfter) {
        turn.syncAfter = false
        background($, async () => $.ui.toast(await syncFeature($)))
      }
      const info = await featureInfo($)
      background($, async () => {
        await refreshPRs($)
        if (info?.issue) await f($, ['plan', 'sync'])
        await refreshPlan($)
      })
    }
    return next(e)
  })

  // --- /f ----------------------------------------------------------------------

  on('command.run', { command: 'f' }, async ($, e) => {
    const [name = '', ...rest] = e.args.trim().split(/\s+/)
    if (!name) {
      const width = Math.max(...SUBCOMMANDS.map(c => c.usage.length)) + 2
      await openPick($, { kind: 'command', options: SUBCOMMANDS.map(c => ({ value: c.name, label: `${c.usage.padEnd(width)}${c.description}` })) })
      return { text: 'pick an f command' }
    }
    const sub = SUBCOMMANDS.find(c => c.name === name)
    if (!sub) return { text: `no /f ${name}; there's ${SUBCOMMANDS.map(c => c.name).join(' · ')}` }
    return { text: await dispatch($, turn, sub, rest.join(' ')) }
  })

  // --- drawing -------------------------------------------------------------------

  on('ui.render', { component: 'Pane', requestId: PICK_PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const p = await read($, pick)
    if (!p) return <Text dimColor>nothing to pick</Text>

    const choose = async (value: string) => {
      await closePick($)
      if (p.kind === 'command') {
        const sub = SUBCOMMANDS.find(c => c.name === value)
        if (sub) $.ui.toast(await dispatch($, turn, sub, ''))
        return
      }
      const blocks = codeBlocks((await lastReply($)) ?? '').filter((_b, i) => value === 'all' || i === Number(value))
      $.ui.toast(blocks.length ? await copyBlocks($, blocks.map(b => b.code)) : 'that code block is gone')
    }

    const hotkeys = '123456789abcdefghijklmnopqrstuvwxyz'
    return (
      <Box flexDirection="column">
        {p.options.map((o, i) => (
          <Button key={o.value} plain hotkey={hotkeys[i]} autoFocus={i === 0 ? true : undefined} onPress={() => choose(o.value)}>
            {o.label}
          </Button>
        ))}
        <Text dimColor>esc to cancel</Text>
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PLAN_PANE }, async ($, e) => {
    const { Box, Text } = $.ui.resolve(e)
    const text = await read($, plan)
    if (!text) return <Text dimColor>loading…</Text>
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

  // Quieter chrome: no "Worked for 47s" row (ctrl+o still shows it).
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
