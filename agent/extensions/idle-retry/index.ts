/**
 * idle-retry: makes pi retry requests that stalled.
 *
 * When httpIdleTimeoutMs fires (no bytes from the provider), the request is
 * cancelled and the error reads "This operation was aborted". Pi's auto-retry
 * only retries errors matching its list ("timed out", "overloaded", ...), not
 * "aborted", so the run just stopped and the prompt looked like it did nothing.
 * This rewrites that error to say it timed out, so pi's own retry (backoff,
 * retry.maxRetries) takes over. Esc is a different case (stopReason "aborted",
 * not "error") and is left alone.
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

type Msg = { role?: string; stopReason?: string; errorMessage?: string };

/** asTimeout returns msg with a retryable error message, or undefined to leave it. */
export function asTimeout<T extends Msg>(msg: T): T | undefined {
	if (msg.role !== "assistant" || msg.stopReason !== "error") return undefined;
	const err = msg.errorMessage ?? "";
	if (!/operation was aborted/i.test(err) || /timed? ?out/i.test(err)) return undefined;
	return { ...msg, errorMessage: `Request timed out (no response within httpIdleTimeoutMs): ${err}` };
}

export default function (pi: ExtensionAPI) {
	pi.on("message_end", (event) => {
		const message = asTimeout(event.message as Msg);
		return message ? { message: message as typeof event.message } : undefined;
	});
}
