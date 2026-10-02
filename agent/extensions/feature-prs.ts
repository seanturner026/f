/**
 * feature-prs: in an `f` feature workspace, show the feature's PRs in the
 * footer (refreshed when pi settles), plus /prs to refresh and open them.
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type PR = { number: number; state: string; isDraft: boolean; url: string };
type RepoPRs = { repo: string; prs: PR[] | null; error?: string };

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
		for (const r of JSON.parse(res.stdout) as RepoPRs[]) {
			for (const p of r.prs ?? []) {
				const state = p.isDraft ? "draft" : p.state.toLowerCase();
				const color = state === "merged" ? "success" : state === "open" ? "accent" : "dim";
				parts.push(`${r.repo} #${p.number} ${t.fg(color, state)}`);
			}
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
