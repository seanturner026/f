/**
 * feature-goal: in an `f` feature workspace, record the first prompt as the
 * feature goal in <feature>/AGENTS.md (replacing the placeholder `f` writes).
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// Must match goalPlaceholder in cmd/f/agentsmd.go.
const PLACEHOLDER = "_TBD: set from the first prompt._";

export default function (pi: ExtensionAPI) {
	pi.on("before_agent_start", async (event, ctx) => {
		const prompt = event.prompt.trim();
		if (!prompt) return;
		const path = join(ctx.cwd, "AGENTS.md");
		if (!existsSync(path)) return;
		const text = readFileSync(path, "utf-8");
		if (!text.startsWith("# Feature:") || !text.includes(PLACEHOLDER)) return;
		writeFileSync(path, text.replace(PLACEHOLDER, prompt));
	});
}
