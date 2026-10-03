/**
 * feature-prs: in an `f` feature workspace, show the feature's PRs in the
 * footer (refreshed when pi settles), plus /prs to refresh and open them.
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

// Shape of `f prs --json` (model.PR in internal/model).
type PRStatus = "draft" | "open" | "merged" | "closed";
type PR = { repo: string; number: number; url: string; status: PRStatus };
type PRsOutput = { prs: PR[] | null; errors?: Record<string, string> };

const KEY = "feature-prs";

function isFeature(cwd: string): boolean {
	const path = join(cwd, "AGENTS.md");
	return existsSync(path) && readFileSync(path, "utf-8").startsWith("# Feature:");
}

export default function (pi: ExtensionAPI) {
	async function refresh(ctx: ExtensionContext, open = false): Promise<void> {
		if (ctx.mode !== "tui" || !isFeature(ctx.cwd)) return;
		const args = open ? ["prs"] : ["prs", "--json"];
		const res = await pi.exec("f", args, { cwd: ctx.cwd, timeout: 30_000 });
		if (open) {
			if (res.code !== 0) ctx.ui.notify(res.stderr.trim() || "f prs failed", "error");
			return refresh(ctx); // re-render the footer from JSON
		}
		if (res.code !== 0) return;
		const t = ctx.ui.theme;
		const parts: string[] = [];
		const out = JSON.parse(res.stdout) as PRsOutput;
		for (const p of out.prs ?? []) {
			const color = p.status === "merged" ? "success" : p.status === "open" ? "accent" : "dim";
			parts.push(`${p.repo} #${p.number} ${t.fg(color, p.status)}`);
		}
		ctx.ui.setStatus(KEY, parts.length ? t.fg("dim", "PRs: ") + parts.join(t.fg("dim", " · ")) : undefined);
	}

	// Background refresh: never block pi, and ignore a ctx that went stale
	// (session replaced or pi exited) before gh answered.
	const background = (ctx: ExtensionContext) => void refresh(ctx).catch(() => {});
	pi.on("session_start", async (_e, ctx) => background(ctx));
	pi.on("agent_settled", async (_e, ctx) => background(ctx));

	pi.registerCommand("prs", {
		description: "Refresh this feature's PRs and open the open ones in the browser",
		handler: async (_args, ctx) => {
			if (!isFeature(ctx.cwd)) return ctx.ui.notify("not in an f feature", "warning");
			await refresh(ctx, true);
		},
	});
}
