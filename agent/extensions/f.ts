/**
 * f: pi integration for `f` feature workspaces. Everything goes through the
 * `f` CLI, which owns the feature (worktrees, AGENTS.md, .context/); this
 * extension only reads `.context/feature.json` to know it's in a feature.
 *
 * - Goal: the first prompt becomes the feature's goal (`f goal`).
 * - Checkpoints: around each run, `f checkpoint begin/end` marks the code
 *   before pi's last code change (nvim: <leader>pr in a repo tab).
 * - PRs: footer from `f prs --json` (instant), refreshed after each run;
 *   `gh pr create` output is recorded at once (`f prs --add`); /prs opens them.
 * - CI: /ci sends failing checks with their failed-job logs (`f ci fix --print`)
 *   as a prompt: find the cause, propose a fix, don't push.
 * - Sync: /sync runs `f sync` (after a merge: reset that repo to main). Notes
 *   from the background sync (launchd: f sync --all) come with the next prompt.
 * - /f <name> [repo...]: open or start a feature without leaving chat.
 * - Plan: after each run, `f plan sync` saves the beads issue's progress for
 *   the picker (bd is too slow to query when the picker opens).
 */
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { isBashToolResult, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";

// .context/feature.json (feature.Info in internal/feature/info.go).
type FeatureInfo = { name: string; root: string; repos: string[]; goal?: string; issue?: string };
// `f prs --json` (model.PR in internal/model).
type PRStatus = "draft" | "open" | "merged" | "closed";
type CheckState = "pass" | "fail" | "pending" | "skipped";
type PR = { repo: string; number: number; url: string; status: PRStatus; checks?: { state: CheckState }[] };

// ciMark: ✗ if any check failed, ⋯ if any is running, ✓ if all passed; "" without checks.
function ciMark(p: PR): "" | "✗" | "⋯" | "✓" {
	const states = (p.checks ?? []).map((c) => c.state);
	if (states.includes("fail")) return "✗";
	if (states.includes("pending")) return "⋯";
	return states.length ? "✓" : "";
}

const PR_URL = /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/pull\/\d+/g;
const STATUS_KEY = "f-prs";

function featureInfo(cwd: string): FeatureInfo | undefined {
	const path = join(cwd, ".context", "feature.json");
	if (!existsSync(path)) return undefined;
	try {
		return JSON.parse(readFileSync(path, "utf-8")) as FeatureInfo;
	} catch {
		return undefined;
	}
}

// The f layout (internal/feature: DefaultLayout): base clones and features.
const BASE = process.env.F_BASE || join(homedir(), "code", "features");

function subdirs(dir: string, keep: (name: string) => boolean = () => true): string[] {
	try {
		return readdirSync(dir, { withFileTypes: true })
			.filter((e) => e.isDirectory() && !e.name.startsWith(".") && keep(e.name))
			.map((e) => e.name)
			.sort();
	} catch {
		return [];
	}
}
const featureNames = () => subdirs(join(BASE, ".features"));
const baseRepos = () => subdirs(BASE, (n) => existsSync(join(BASE, n, ".git")));

export default function (pi: ExtensionAPI) {
	const f = (ctx: ExtensionContext, ...args: string[]) => pi.exec("f", args, { cwd: ctx.cwd, timeout: 30_000 });
	// Never block pi on background work, and ignore a ctx that went stale.
	const background = (p: Promise<unknown>) => void p.catch(() => {});

	// --- PR footer -------------------------------------------------------------

	async function renderPRs(ctx: ExtensionContext, refresh: boolean): Promise<void> {
		if (ctx.mode !== "tui" || !featureInfo(ctx.cwd)) return;
		const res = await f(ctx, "prs", "--json", ...(refresh ? ["-r"] : []));
		if (res.code !== 0) return;
		const t = ctx.ui.theme;
		const parts = ((JSON.parse(res.stdout) as { prs: PR[] | null }).prs ?? []).map((p) => {
			const color = p.status === "merged" ? "success" : p.status === "open" ? "accent" : "dim";
			const mark = ciMark(p);
			const ci = mark && " " + t.fg(mark === "✗" ? "error" : mark === "✓" ? "success" : "accent", mark);
			return `${p.repo} #${p.number} ${t.fg(color, p.status)}${ci}`;
		});
		ctx.ui.setStatus(STATUS_KEY, parts.length ? t.fg("dim", "PRs: ") + parts.join(t.fg("dim", " · ")) : undefined);
	}

	pi.on("session_start", async (_e, ctx) => {
		background(renderPRs(ctx, false).then(() => renderPRs(ctx, true)));
		if (featureInfo(ctx.cwd)?.issue) background(f(ctx, "plan", "sync"));
	});

	pi.on("tool_result", async (event, ctx) => {
		if (!isBashToolResult(event) || event.isError || !featureInfo(ctx.cwd)) return;
		if (!/\bgh\s+pr\s+create\b/.test(String(event.input.command ?? ""))) return;
		const text = event.content.map((c) => (c.type === "text" ? c.text : "")).join("\n");
		const urls = [...new Set(text.match(PR_URL) ?? [])];
		background(
			(async () => {
				for (const url of urls) await f(ctx, "prs", "--add", url);
				await renderPRs(ctx, false);
			})(),
		);
	});

	pi.registerCommand("prs", {
		description: "Refresh this feature's PRs and open the open ones in the browser",
		handler: async (_args, ctx) => {
			if (!featureInfo(ctx.cwd)) return ctx.ui.notify("not in an f feature", "warning");
			const res = await f(ctx, "prs", "-r");
			if (res.code !== 0) ctx.ui.notify(res.stderr.trim() || "f prs failed", "error");
			await renderPRs(ctx, false);
		},
	});

	// /f <name> [repo...]: open a feature (its Herdr workspace) or start one,
	// like `f` in a terminal. A new feature without repos asks for them.
	pi.registerCommand("f", {
		description: "Open a feature, or start one: /f <name> [repo...]",
		getArgumentCompletions: (prefix) => {
			const words = prefix.split(/\s+/);
			const current = words.pop() ?? "";
			const done = words.join(" ");
			const options = words.length === 0 ? featureNames() : baseRepos().filter((r) => !words.slice(1).includes(r));
			const items = options
				.filter((o) => o.startsWith(current))
				.map((o) => ({ value: done ? `${done} ${o} ` : `${o} `, label: o }));
			return items.length ? items : null;
		},
		handler: async (args, ctx) => {
			const [name, ...repos] = args.trim().split(/\s+/).filter(Boolean);
			if (!name) return ctx.ui.notify("usage: /f <name> [repo...]", "warning");
			if (!featureNames().includes(name) && repos.length === 0) {
				const DONE = "\u2713 done";
				for (;;) {
					const left = baseRepos().filter((r) => !repos.includes(r));
					const pick = await ctx.ui.select(
						`${name}: repos${repos.length ? ` (${repos.join(", ")})` : ""}`,
						repos.length ? [DONE, ...left] : left,
					);
					if (!pick) return ctx.ui.notify("cancelled", "info");
					if (pick === DONE) break;
					repos.push(pick);
				}
			}
			ctx.ui.notify(`f ${name}\u2026`, "info");
			const res = await pi.exec("f", [name, ...repos.flatMap((r) => ["-r", r])], { cwd: ctx.cwd, timeout: 180_000 });
			const out = (res.stdout + res.stderr).replace(/\x1b\[[\d;]*m/g, "").trim();
			if (res.code !== 0) return ctx.ui.notify(out || `f ${name} failed`, "error");
			ctx.ui.notify(out.split("\n").pop() || `opened ${name}`, "info");
		},
	});

	pi.registerCommand("ci", {
		description: "Send this feature's failing CI checks (with logs) to pi: find the cause, propose a fix, don't push",
		handler: async (_args, ctx) => {
			if (!featureInfo(ctx.cwd)) return ctx.ui.notify("not in an f feature", "warning");
			ctx.ui.notify("checking CI\u2026", "info");
			const res = await pi.exec("f", ["ci", "fix", "--print"], { cwd: ctx.cwd, timeout: 120_000 });
			await renderPRs(ctx, false); // f ci fix refreshed them
			if (res.code !== 0) return ctx.ui.notify(res.stderr.trim() || "f ci fix failed", "error");
			const prompt = res.stdout.trim();
			if (!prompt) return ctx.ui.notify("no failing checks", "info");
			pi.sendUserMessage(prompt, ctx.isIdle() ? undefined : { deliverAs: "followUp" });
		},
	});

	pi.registerCommand("sync", {
		description: "After a PR merges: reset its repo to main (or rebase newer commits onto it)",
		handler: async (_args, ctx) => {
			if (!featureInfo(ctx.cwd)) return ctx.ui.notify("not in an f feature", "warning");
			const res = await pi.exec("f", ["sync"], { cwd: ctx.cwd, timeout: 120_000 });
			await renderPRs(ctx, false);
			const out = (res.stdout + res.stderr).replace(/\x1b\[[\d;]*m/g, "").trim();
			ctx.ui.notify(out || "nothing to sync", res.code !== 0 || out.includes("\u2717") ? "warning" : "info");
		},
	});

	// --- goal + checkpoints around each run ------------------------------------

	let running = false;

	pi.on("before_agent_start", async (event, ctx) => {
		const info = featureInfo(ctx.cwd);
		if (!info || running) return; // follow-ups within one run share its snapshot
		running = true;
		const prompt = event.prompt.trim();
		try {
			// Background: also sets the beads description (bd is ~0.5s).
			if (!info.goal && prompt) background(f(ctx, "goal", prompt));
			// Awaited: the snapshot must exist before pi starts editing.
			const res = await f(ctx, "checkpoint", "begin");
			if (res.code !== 0 && ctx.mode === "tui") ctx.ui.notify(`f checkpoint: ${res.stderr.trim()}`, "warning");
		} catch {
			// never block a run on bookkeeping
		}
	});

	// Background syncs (launchd) may have reset or rebased a repo after its PR
	// merged: tell pi with the next prompt, so it doesn't work from old contents.
	pi.on("before_agent_start", async (_e, ctx) => {
		if (!featureInfo(ctx.cwd)) return undefined;
		const res = await f(ctx, "sync", "notes", "--take").catch(() => undefined);
		const notes = res?.code === 0 ? res.stdout.trim() : "";
		if (!notes) return undefined;
		return {
			message: {
				customType: "f-sync",
				content: `While you were idle, f sync changed repos in this feature (re-read files before editing them):\n${notes}`,
				display: true,
			},
		};
	});

	pi.on("agent_settled", async (_e, ctx) => {
		if (!running) return;
		running = false;
		await f(ctx, "checkpoint", "end").catch(() => {});
		background(renderPRs(ctx, true));
		if (featureInfo(ctx.cwd)?.issue) background(f(ctx, "plan", "sync"));
	});
}
