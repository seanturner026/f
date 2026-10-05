/**
 * repo-instructions: in an f feature, pi must read a repo's instruction files
 * (AGENTS.md / CLAUDE.md) in full before it touches that repo in any way:
 * reading or editing its files, or a bash command that names it (`cd argocd`,
 * `git -C argocd`, `gh pr diff -R org/argocd`, ...). Reviews and questions
 * count too. Until then those tool calls are blocked with what's left to read.
 *
 * "In full" means every line was returned by the read tool (paging with
 * offset counts). Reading via bash (`cat`) doesn't count: it's blocked like any
 * other command naming the repo. After a compaction the files may have been
 * summarized away, so they have to be read again.
 */
import { existsSync, readFileSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join, relative, resolve } from "node:path";
import { isReadToolResult, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { blockReason, Coverage, INSTRUCTION_FILES, lineCount, linesReturned, reposInCommand, repoOfPath } from "./gate.ts";

type FeatureInfo = { root: string; repos: string[] };

function featureInfo(cwd: string): FeatureInfo | undefined {
	const path = join(cwd, ".context", "feature.json");
	if (!existsSync(path)) return undefined;
	try {
		return JSON.parse(readFileSync(path, "utf-8")) as FeatureInfo;
	} catch {
		return undefined;
	}
}

// Paths as the read tool takes them: relative to cwd, ~ for home, a leading @.
function toAbs(cwd: string, path: string): string {
	const p = path.replace(/^@/, "");
	return p === "~" || p.startsWith("~/") ? join(homedir(), p.slice(1)) : resolve(cwd, p);
}

// inRoot resolves symlinks in a path's directories but not the file itself,
// so an AGENTS.md symlinked elsewhere still counts as the repo's.
const inRoot = (path: string) => join(real(dirname(path)), basename(path));

// real resolves symlinks, also for paths that don't exist yet (a file about to
// be written): the nearest existing parent is resolved. pi's cwd and the root
// in feature.json can differ by a symlink (e.g. /var vs /private/var).
function real(path: string): string {
	try {
		return realpathSync(path);
	} catch {
		const parent = dirname(path);
		return parent === path ? path : join(real(parent), basename(path));
	}
}

export default function (pi: ExtensionAPI) {
	// Keyed by real path, so an AGENTS.md symlinked to CLAUDE.md is one file.
	const coverage = new Map<string, Coverage>();
	pi.on("session_start", async () => coverage.clear());
	pi.on("session_compact", async () => coverage.clear());

	// The repo's instruction files (deduplicated), each with its coverage.
	function instructions(root: string, repo: string): { rel: string; cov: Coverage }[] {
		const out: { rel: string; cov: Coverage }[] = [];
		const seen = new Set<string>();
		for (const name of INSTRUCTION_FILES) {
			const abs = join(root, repo, name);
			if (!existsSync(abs)) continue;
			const key = real(abs);
			if (seen.has(key)) continue;
			seen.add(key);
			let cov = coverage.get(key);
			if (!cov) {
				cov = new Coverage(lineCount(readFileSync(abs, "utf-8")));
				coverage.set(key, cov);
			}
			out.push({ rel: `${repo}/${name}`, cov });
		}
		return out;
	}

	function isInstructionFile(root: string, repos: string[], abs: string): boolean {
		const repo = repoOfPath(root, repos, abs);
		return !!repo && INSTRUCTION_FILES.some((n) => relative(join(root, repo), abs) === n);
	}

	pi.on("tool_call", async (event, ctx) => {
		const info = featureInfo(ctx.cwd);
		if (!info) return undefined;
		const root = real(info.root);
		const repos = info.repos;
		let touched: string[] = [];
		if (event.toolName === "bash") {
			touched = reposInCommand(repos, String(event.input.command ?? ""));
		} else {
			const path = (event.input as { path?: unknown }).path;
			if (typeof path !== "string") return undefined;
			const abs = inRoot(toAbs(ctx.cwd, path));
			if (isInstructionFile(root, repos, abs)) return undefined; // that's how you satisfy the gate
			const repo = repoOfPath(root, repos, abs);
			if (repo) touched = [repo];
		}
		for (const repo of touched) {
			const unread = instructions(root, repo)
				.filter((i) => !i.cov.complete)
				.map((i) => ({ rel: i.rel, missing: i.cov.missing(), total: i.cov.total }));
			const reason = blockReason(repo, unread);
			if (reason) return { block: true, reason };
		}
		return undefined;
	});

	pi.on("tool_result", async (event, ctx) => {
		if (!isReadToolResult(event) || event.isError) return undefined;
		const info = featureInfo(ctx.cwd);
		const input = event.input as { path?: string; offset?: number; limit?: number };
		if (!info || typeof input.path !== "string") return undefined;
		const root = real(info.root);
		const abs = inRoot(toAbs(ctx.cwd, input.path));
		if (!isInstructionFile(root, info.repos, abs)) return undefined;
		const repo = repoOfPath(root, info.repos, abs)!;
		instructions(root, repo); // make sure its coverage exists
		const cov = coverage.get(real(abs));
		if (!cov) return undefined;
		const t = event.details?.truncation;
		cov.mark(input.offset, linesReturned(cov.total, input.offset, input.limit, t?.truncated ? t.outputLines : undefined));
		return undefined;
	});
}
