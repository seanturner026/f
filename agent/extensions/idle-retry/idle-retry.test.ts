import assert from "node:assert/strict";
import { test } from "node:test";
import { asTimeout } from "./index.ts";

test("idle timeout error becomes retryable", () => {
	const got = asTimeout({ role: "assistant", stopReason: "error", errorMessage: "This operation was aborted" });
	assert.match(got?.errorMessage ?? "", /timed out/);
	assert.equal(got?.stopReason, "error");
});

test("leaves everything else alone", () => {
	const cases = [
		{ role: "assistant", stopReason: "aborted", errorMessage: "Operation aborted" }, // Esc
		{ role: "assistant", stopReason: "error", errorMessage: "invalid_request_error: prompt is too long" },
		{ role: "assistant", stopReason: "error", errorMessage: "Request timed out: This operation was aborted" }, // already rewritten
		{ role: "assistant", stopReason: "stop" },
		{ role: "toolResult", stopReason: "error", errorMessage: "This operation was aborted" },
	];
	for (const c of cases) assert.equal(asTimeout(c), undefined, JSON.stringify(c));
});
