/**
 * compact-read: the built-in read tool, drawn as one line ("read <path>")
 * instead of a padded colored box. Behavior is the original tool's.
 *
 * - success: just the call line; ctrl+o expands to the first lines read
 * - error (e.g. blocked by repo-instructions): a red line with the reason
 */
import { createReadToolDefinition, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";

const EXPANDED_LINES = 20;

export default function (pi: ExtensionAPI) {
	const read = createReadToolDefinition(process.cwd());
	pi.registerTool({
		...read,
		renderShell: "self",

		renderCall(args, theme, context) {
			let text = theme.fg("toolTitle", theme.bold("read ")) + theme.fg("accent", String(args.path ?? ""));
			const range = [args.offset && `from ${args.offset}`, args.limit && `${args.limit} lines`].filter(Boolean);
			if (range.length) text += theme.fg("dim", ` (${range.join(", ")})`);
			if (!context.executionStarted || context.isPartial) text += theme.fg("dim", " \u2026");
			return new Text(text, 0, 0);
		},

		renderResult(result, { expanded, isPartial }, theme, context) {
			const first = result.content[0];
			const body = first?.type === "text" ? first.text : "";
			if (isPartial) return new Text("", 0, 0);
			if (context.isError) return new Text(theme.fg("error", `  ${body.split("\n")[0]}`), 0, 0);
			if (!expanded || first?.type !== "text") return new Text("", 0, 0);
			const lines = body.split("\n");
			let text = lines
				.slice(0, EXPANDED_LINES)
				.map((l) => theme.fg("dim", `  ${l}`))
				.join("\n");
			if (lines.length > EXPANDED_LINES) text += `\n${theme.fg("muted", `  \u2026 ${lines.length - EXPANDED_LINES} more lines`)}`;
			return new Text(text, 0, 0);
		},
	});
}
