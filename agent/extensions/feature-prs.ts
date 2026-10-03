/**
 * feature-prs: in an `f` feature workspace, show the feature's PRs in the footer.
 *
 * - Footer renders from `.context/state.json` (via `f prs --json`), instantly.
 * - After every turn, refreshes live in the background (`f prs -r --json`).
 * - When pi runs `gh pr create`, records the new PR immediately (`f prs --add`).
 * - /prs refreshes and opens the open PRs in the browser.
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { isBashToolResult, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";

// Shape of `f prs --json` (model.PR in internal/model).
type PRStatus = "draft" | "open" | "merged" | "closed";
type PR = { repo: string; number: number; url: string; status: PRStatus };
type PRsOutput = { prs: PR[] | null };

const KEY = "feature-prs";
const PR_URL = /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/pull\/\d+/g;

function isFeature(cwd: string): boolean {
	const path = join(cwd, "AGENTS.md");
	return existsSync(path) && readFileSync(path, "utf-8").startsWith("# Feature:");
}

export default function (pi: ExtensionAPI) {
	async function f(ctx: ExtensionContext, ...args: string[]) {
		return pi.exec("f", args, { cwd: ctx.cwd, timeout: 30_000 });
	}

	async function render(ctx: ExtensionContext, refresh: boolean): Promise<void> {
		if (ctx.mode !== "tui" || !isFeature(ctx.cwd)) return;
		const res = await f(ctx, "prs", "--json", ...(refresh ? ["-r"] : []));
		if (res.code !== 0) return;
		const t = ctx.ui.theme;
		const parts = ((JSON.parse(res.stdout) as PRsOutput).prs ?? []).map((p) => {
			const color = p.status === "merged" ? "success" : p.status === "open" ? "accent" : "dim";
			return `${p.repo} #${p.number} ${t.fg(color, p.status)}`;
		});
		ctx.ui.setStatus(KEY, parts.length ? t.fg("dim", "PRs: ") + parts.join(t.fg("dim", " · ")) : undefined);
	}

	// Never block pi, and ignore a ctx that went stale (session replaced or pi
	// exited) before the command answered.
	const background = (p: Promise<unknown>) => void p.catch(() => {});

	pi.on("session_start", async (_e, ctx) => {
		background(render(ctx, false).then(() => render(ctx, true)));
	});
	pi.on("agent_settled", async (_e, ctx) => background(render(ctx, true)));

	pi.on("tool_result", async (event, ctx) => {
		if (!isBashToolResult(event) || event.isError || !isFeature(ctx.cwd)) return;
		const command = String(event.input.command ?? "");
		if (!/\bgh\s+pr\s+create\b/.test(command)) return;
		const text = event.content.map((c) => (c.type === "text" ? c.text : "")).join("\n");
		const urls = [...new Set(text.match(PR_URL) ?? [])];
		background(
			(async () => {
				for (const url of urls) await f(ctx, "prs", "--add", url);
				await render(ctx, false);
			})(),
		);
	});

	pi.registerCommand("prs", {
		description: "Refresh this feature's PRs and open the open ones in the browser",
		handler: async (_args, ctx) => {
			if (!isFeature(ctx.cwd)) return ctx.ui.notify("not in an f feature", "warning");
			const res = await f(ctx, "prs", "-r");
			if (res.code !== 0) ctx.ui.notify(res.stderr.trim() || "f prs failed", "error");
			await render(ctx, false);
		},
	});
}
