/**
 * feature-checkpoint: in an `f` feature, mark the code before each pi run so
 * nvim can show "changed since last run" (<leader>pr in a repo tab).
 *
 * - When you send a prompt: `f checkpoint begin` snapshots every repo.
 * - When pi settles: `f checkpoint end` moves refs/worktree/pi-last to that
 *   snapshot in repos whose code changed (chat-only runs leave it alone).
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

function isFeature(cwd: string): boolean {
	const path = join(cwd, "AGENTS.md");
	return existsSync(path) && readFileSync(path, "utf-8").startsWith("# Feature:");
}

export default function (pi: ExtensionAPI) {
	let running = false;

	async function checkpoint(ctx: ExtensionContext, step: "begin" | "end") {
		if (!isFeature(ctx.cwd)) return;
		const res = await pi.exec("f", ["checkpoint", step], { cwd: ctx.cwd, timeout: 30_000 });
		if (res.code !== 0 && ctx.mode === "tui") {
			ctx.ui.notify(`f checkpoint ${step}: ${res.stderr.trim()}`, "warning");
		}
	}

	// Awaited: the snapshot must exist before pi starts editing.
	pi.on("before_agent_start", async (_e, ctx) => {
		if (running) return; // follow-ups within one run share its snapshot
		running = true;
		await checkpoint(ctx, "begin").catch(() => {});
	});

	pi.on("agent_settled", async (_e, ctx) => {
		if (!running) return;
		running = false;
		await checkpoint(ctx, "end").catch(() => {});
	});
}
