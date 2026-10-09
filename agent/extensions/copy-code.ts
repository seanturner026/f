/**
 * copy-code: /code copies code blocks from the last assistant message exactly
 * as written (no terminal wrapping, no indentation from the transcript), so
 * snippets paste cleanly.
 *
 *   /code      one block: copy it; several: pick one (or all)
 *   /code 2    copy block 2
 *   /slack     the whole reply, converted for Slack's message box (*bold*,
 *              headings as bold lines, links as "text (url)"); code untouched
 */
import { spawnSync } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

type Block = { lang: string; code: string };

/** codeBlocks extracts fenced code blocks (``` or ~~~) from markdown. */
export function codeBlocks(markdown: string): Block[] {
	const blocks: Block[] = [];
	const re = /^ {0,3}(```+|~~~+)[ \t]*([^\n`]*)\n([\s\S]*?)\n {0,3}\1[ \t]*$/gm;
	for (const m of markdown.matchAll(re)) blocks.push({ lang: m[2].trim(), code: m[3] });
	return blocks;
}

/** inlineToSlack converts one line of markdown prose (no code) to Slack. */
function inlineToSlack(line: string): string {
	return (
		line
			// italic *x* → _x_ (before bold, which becomes single asterisks)
			.replace(/(?<![*\w])\*(?![\s*])([^*\n]+?)(?<!\s)\*(?![*\w])/g, "_$1_")
			.replace(/\*\*(?!\s)(.+?)(?<!\s)\*\*/g, "*$1*")
			.replace(/__(?!\s)(.+?)(?<!\s)__/g, "*$1*")
			.replace(/~~(.+?)~~/g, "~$1~")
			.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, text: string, url: string) => (text === url ? url : `${text} (${url})`))
	);
}

/** toSlack converts markdown to what Slack's message box renders. Fenced
 * blocks and `inline code` are left as they are (Slack renders both). */
export function toSlack(markdown: string): string {
	const out: string[] = [];
	let fence = "";
	for (let line of markdown.split("\n")) {
		const f = line.match(/^ {0,3}(```+|~~~+)/);
		if (fence) {
			out.push(line);
			if (f && line.trim().startsWith(fence)) fence = "";
			continue;
		}
		if (f) {
			fence = f[1];
			out.push(line.replace(/^ {0,3}/, "").replace(/^(```+|~~~+).*/, "```")); // Slack ignores the language
			continue;
		}
		if (/^ {0,3}([-*_])( *\1){2,} *$/.test(line)) continue; // horizontal rule
		const heading = line.match(/^ {0,3}#{1,6}\s+(.*?)\s*#*$/);
		if (heading) {
			out.push(`*${heading[1].replace(/\*\*/g, "")}*`);
			continue;
		}
		// pasted "- " / "* " stay literal in Slack: use bullet characters
		line = line.replace(/^(\s*)[-*+] (?=\S)/, (_m, indent: string) => `${indent}\u2022 `);
		// convert prose between `inline code` spans only
		out.push(line.split(/(`[^`]*`)/).map((part, i) => (i % 2 ? part : inlineToSlack(part))).join(""));
	}
	return out.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

function lastAssistantText(entries: unknown[]): string | undefined {
	for (let i = entries.length - 1; i >= 0; i--) {
		const e = entries[i] as { type?: string; message?: { role?: string; content?: unknown } };
		if (e.type !== "message" || e.message?.role !== "assistant") continue;
		const content = e.message.content;
		const text = Array.isArray(content)
			? content.map((c: { type?: string; text?: string }) => (c.type === "text" ? (c.text ?? "") : "")).join("\n")
			: typeof content === "string"
				? content
				: "";
		if (text.trim()) return text;
	}
	return undefined;
}

function copy(text: string): boolean {
	return spawnSync("pbcopy", { input: text }).status === 0;
}

export default function (pi: ExtensionAPI) {
	pi.registerCommand("slack", {
		description: "Copy the last reply formatted for Slack (*bold*, plain headings and links; code untouched)",
		handler: async (_args, ctx) => {
			const text = lastAssistantText(ctx.sessionManager.getBranch());
			if (!text) return ctx.ui.notify("no reply to copy", "info");
			if (!copy(toSlack(text))) return ctx.ui.notify("pbcopy failed", "error");
			ctx.ui.notify("copied for Slack", "info");
		},
	});

	pi.registerCommand("code", {
		description: "Copy code block(s) from the last reply, unwrapped: /code [n]",
		handler: async (args, ctx) => {
			const text = lastAssistantText(ctx.sessionManager.getBranch());
			const blocks = text ? codeBlocks(text) : [];
			if (blocks.length === 0) return ctx.ui.notify("no code blocks in the last reply", "info");

			let picked: Block[] | undefined;
			const n = Number.parseInt(args.trim(), 10);
			if (!Number.isNaN(n)) {
				if (n < 1 || n > blocks.length) return ctx.ui.notify(`there are ${blocks.length} code blocks`, "warning");
				picked = [blocks[n - 1]];
			} else if (blocks.length === 1) {
				picked = blocks;
			} else {
				const ALL = `all ${blocks.length}`;
				const labels = blocks.map((b, i) => {
					const first = b.code.split("\n")[0].trim();
					return `${i + 1}. ${b.lang ? `[${b.lang}] ` : ""}${first.length > 70 ? `${first.slice(0, 69)}\u2026` : first}`;
				});
				const choice = await ctx.ui.select("copy which code block?", [...labels, ALL]);
				if (!choice) return;
				picked = choice === ALL ? blocks : [blocks[labels.indexOf(choice)]];
			}
			const out = picked.map((b) => b.code).join("\n\n");
			if (!copy(out)) return ctx.ui.notify("pbcopy failed", "error");
			const lines = out.split("\n").length;
			ctx.ui.notify(`copied ${picked.length > 1 ? `${picked.length} blocks, ` : ""}${lines} line${lines === 1 ? "" : "s"}`, "info");
		},
	});
}
