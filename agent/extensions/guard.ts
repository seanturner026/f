/**
 * guard: keep pi away from admin AWS profiles and production kube contexts.
 *
 * - On start: `f creds` writes filtered AWS config/credentials and kubeconfig
 *   (rules: internal/guard defaults + ~/.config/f/guard.json) and pi's
 *   environment is pointed at them, so every command and script pi runs
 *   (aws, kubectl, terraform, boto3, ...) only sees allowed profiles/contexts.
 * - Before each bash command: `f guard check` blocks commands that name a
 *   denied profile/context/EKS cluster or point back at the real files.
 * If the guard can't set up, bash is blocked rather than run unguarded.
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

type Creds = { env: Record<string, string>; removed_profiles: string[] | null; removed_contexts: string[] | null };
type Verdict = { block: boolean; reason?: string };

export default function (pi: ExtensionAPI) {
	let ready = false;
	let setupError = "";

	pi.on("session_start", async (_e, ctx) => {
		if (ready) return;
		const res = await pi.exec("f", ["creds"], { timeout: 30_000 });
		if (res.code !== 0) {
			setupError = res.stderr.trim() || `f creds exited ${res.code}`;
			if (ctx.mode === "tui") ctx.ui.notify(`guard: ${setupError}; bash is blocked`, "error");
			return;
		}
		const creds = JSON.parse(res.stdout) as Creds;
		for (const [k, v] of Object.entries(creds.env)) {
			if (v) process.env[k] = v;
			else delete process.env[k];
		}
		ready = true;
	});

	pi.on("tool_call", async (event) => {
		if (event.toolName !== "bash") return undefined;
		if (!ready) return { block: true, reason: `guard not set up: ${setupError || "starting"}` };
		const command = String(event.input.command ?? "");
		const res = await pi.exec("f", ["guard", "check", "--", command], { timeout: 10_000 });
		if (res.code !== 0) return { block: true, reason: `guard check failed: ${res.stderr.trim()}` };
		const v = JSON.parse(res.stdout) as Verdict;
		return v.block ? { block: true, reason: `guard: ${v.reason}` } : undefined;
	});
}
