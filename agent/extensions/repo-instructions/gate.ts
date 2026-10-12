/**
 * Pure logic for the repo-instructions gate (no pi imports, so it's testable
 * with `node --test`). See index.ts.
 */
import { isAbsolute, relative, resolve, sep } from "node:path";

/** Instruction files a repo may have, in the order f lists them in AGENTS.md. */
export const INSTRUCTION_FILES = ["AGENTS.md", "CLAUDE.md"] as const;

/** repoOfPath: the feature repo a path is in (root/<repo>/...), if any. */
export function repoOfPath(root: string, repos: string[], path: string): string | undefined {
	const rel = relative(root, resolve(root, path));
	if (rel === "" || rel.startsWith("..") || isAbsolute(rel)) return undefined;
	const top = rel.split(sep)[0];
	return repos.includes(top) ? top : undefined;
}

const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/**
 * reposInCommand: repos a shell command names as a word (`cd argocd`,
 * `git -C argocd`, `argocd/apps/x.yaml`, `gh pr view -R org/argocd`), not as
 * part of a longer name (`argocd-image-updater`).
 */
export function reposInCommand(repos: string[], command: string): string[] {
	return repos.filter((r) => new RegExp(`(?<![\\w.-])${escapeRegExp(r)}(?![\\w.-])`).test(command));
}

/** lineCount counts lines the way the read tool numbers them. */
export function lineCount(text: string): number {
	if (text === "") return 0;
	const n = text.split("\n").length;
	return text.endsWith("\n") ? n - 1 : n;
}

/**
 * linesReturned: how many lines a read returned, from its offset/limit and
 * whether pi truncated the output (then outputLines says how many it kept).
 */
export function linesReturned(total: number, offset: number | undefined, limit: number | undefined, truncatedLines?: number): number {
	const start = Math.max(1, offset ?? 1);
	if (truncatedLines !== undefined) return truncatedLines;
	const rest = Math.max(0, total - start + 1);
	return limit === undefined ? rest : Math.min(limit, rest);
}

/** Coverage tracks which lines of an instruction file have been read. */
export class Coverage {
	readonly total: number;
	private read: boolean[];
	constructor(total: number) {
		this.total = total;
		this.read = new Array(total).fill(false);
	}
	mark(offset: number | undefined, count: number): void {
		const start = Math.max(1, offset ?? 1);
		for (let i = start; i < start + count && i <= this.total; i++) this.read[i - 1] = true;
	}
	get complete(): boolean {
		return this.read.every(Boolean);
	}
	/** missing: unread line ranges, like "1-40, 2001-2400". */
	missing(): string {
		const ranges: string[] = [];
		let from = 0;
		for (let i = 1; i <= this.total + 1; i++) {
			const unread = i <= this.total && !this.read[i - 1];
			if (unread && !from) from = i;
			if (!unread && from) {
				ranges.push(from === i - 1 ? `${from}` : `${from}-${i - 1}`);
				from = 0;
			}
		}
		return ranges.join(", ");
	}
}

/** blockReason explains which files still need reading, or "" if none. */
export function blockReason(repo: string, unread: { rel: string; missing: string; total: number }[]): string {
	if (unread.length === 0) return "";
	// Say the file exists: a read that raced with the repo being added (f add
	// in the same turn) fails, and pi then wrongly concludes there's no file.
	const files = unread.map((u) =>
		u.missing === `1-${u.total}` || u.total <= 1
			? `\`${u.rel}\` (it exists: ${u.total} lines)`
			: `\`${u.rel}\` (it exists; lines ${u.missing} still unread)`,
	);
	return (
		`Read ${files.join(" and ")} in full before working in ${repo}/ (reviews and questions included). ` +
		"Use the read tool, paging with offset until the end; then retry. If an earlier read failed, the repo was still being added: read it again now."
	);
}
