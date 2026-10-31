export type Block = { lang: string; code: string };

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

/** slackText is what /slack copies: the reply's longest blockquote (a drafted
 * message, without its "> " markers) if it has one, else the whole reply. */
export function slackText(markdown: string): string {
	let best: string[] = [];
	let cur: string[] = [];
	for (const line of [...markdown.split("\n"), ""]) {
		const q = line.match(/^ {0,3}> ?(.*)$/);
		if (q) {
			cur.push(q[1]);
			continue;
		}
		if (cur.length > best.length) best = cur;
		cur = [];
	}
	return toSlack(best.length ? best.join("\n") : markdown);
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

