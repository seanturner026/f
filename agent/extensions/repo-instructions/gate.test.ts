// Run: node --test agent/extensions/repo-instructions/gate.test.ts
import { deepStrictEqual, strictEqual } from "node:assert/strict";
import { test } from "node:test";
import { blockReason, Coverage, lineCount, linesReturned, reposInCommand, repoOfPath } from "./gate.ts";

const root = "/f/webapp";
const repos = ["argocd", "core", "terraform-deployments"];

test("repoOfPath", () => {
	strictEqual(repoOfPath(root, repos, "argocd/apps/x.yaml"), "argocd");
	strictEqual(repoOfPath(root, repos, "/f/webapp/core"), "core");
	strictEqual(repoOfPath(root, repos, "AGENTS.md"), undefined);
	strictEqual(repoOfPath(root, repos, "."), undefined);
	strictEqual(repoOfPath(root, repos, "../other/argocd/x"), undefined);
	strictEqual(repoOfPath(root, repos, "argocd-old/x"), undefined);
});

test("reposInCommand", () => {
	deepStrictEqual(reposInCommand(repos, "cd argocd && git diff origin/HEAD..."), ["argocd"]);
	deepStrictEqual(reposInCommand(repos, "git -C terraform-deployments log"), ["terraform-deployments"]);
	deepStrictEqual(reposInCommand(repos, "gh pr diff 12 -R foat/argocd"), ["argocd"]);
	deepStrictEqual(reposInCommand(repos, "rg foo argocd/apps core/src"), ["argocd", "core"]);
	deepStrictEqual(reposInCommand(repos, "cat 'argocd/CLAUDE.md'"), ["argocd"]);
	deepStrictEqual(reposInCommand(repos, "helm search repo argocd-image-updater"), []);
	deepStrictEqual(reposInCommand(repos, "ls .context"), []);
});

test("lineCount ignores the empty line after a trailing newline", () => {
	strictEqual(lineCount(""), 0);
	strictEqual(lineCount("a"), 1);
	strictEqual(lineCount("a\nb\n"), 2);
	strictEqual(lineCount("a\nb"), 2);
});

test("linesReturned", () => {
	strictEqual(linesReturned(100, undefined, undefined), 100);
	strictEqual(linesReturned(100, 41, undefined), 60);
	strictEqual(linesReturned(100, 41, 10), 10);
	strictEqual(linesReturned(100, 95, 10), 6);
	strictEqual(linesReturned(3000, undefined, undefined, 2000), 2000); // truncated by pi
});

test("Coverage: paged reads complete it", () => {
	const c = new Coverage(2400);
	strictEqual(c.complete, false);
	strictEqual(c.missing(), "1-2400");
	c.mark(undefined, 2000);
	strictEqual(c.missing(), "2001-2400");
	c.mark(2001, 400);
	strictEqual(c.complete, true);
	strictEqual(c.missing(), "");
});

test("Coverage: gaps and single lines", () => {
	const c = new Coverage(10);
	c.mark(2, 3); // 2-4
	c.mark(6, 4); // 6-9
	strictEqual(c.missing(), "1, 5, 10");
	strictEqual(new Coverage(0).complete, true);
});

test("blockReason", () => {
	strictEqual(blockReason("argocd", []), "");
	const r = blockReason("argocd", [{ rel: "argocd/CLAUDE.md", missing: "2001-2400", total: 2400 }]);
	strictEqual(r.includes("`argocd/CLAUDE.md` (it exists; lines 2001-2400 still unread)"), true);
	const whole = blockReason("argocd", [{ rel: "argocd/CLAUDE.md", missing: "1-500", total: 500 }]);
	strictEqual(whole.startsWith("Read `argocd/CLAUDE.md` (it exists: 500 lines) in full before working in argocd/"), true);
});
