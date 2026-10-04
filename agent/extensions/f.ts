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
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { isBashToolResult, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";

// .context/feature.json (feature.Info in internal/feature/info.go).
type FeatureInfo = { name: string; root: string; repos: string[]; goal?: string };
// `f prs --json` (model.PR in internal/model).
type PRStatus = "draft" | "open" | "merged" | "closed";
type PR = { repo: string; number: number; url: string; status: PRStatus };

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
			return `${p.repo} #${p.number} ${t.fg(color, p.status)}`;
		});
		ctx.ui.setStatus(STATUS_KEY, parts.length ? t.fg("dim", "PRs: ") + parts.join(t.fg("dim", " · ")) : undefined);
	}

	pi.on("session_start", async (_e, ctx) => {
		background(renderPRs(ctx, false).then(() => renderPRs(ctx, true)));
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

	// --- goal + checkpoints around each run ------------------------------------

	let running = false;

	pi.on("before_agent_start", async (event, ctx) => {
		const info = featureInfo(ctx.cwd);
		if (!info || running) return; // follow-ups within one run share its snapshot
		running = true;
		const prompt = event.prompt.trim();
		try {
			if (!info.goal && prompt) await f(ctx, "goal", prompt);
			// Awaited: the snapshot must exist before pi starts editing.
			const res = await f(ctx, "checkpoint", "begin");
			if (res.code !== 0 && ctx.mode === "tui") ctx.ui.notify(`f checkpoint: ${res.stderr.trim()}`, "warning");
		} catch {
			// never block a run on bookkeeping
		}
	});

	pi.on("agent_settled", async (_e, ctx) => {
		if (!running) return;
		running = false;
		await f(ctx, "checkpoint", "end").catch(() => {});
		background(renderPRs(ctx, true));
	});
}
