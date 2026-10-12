/**
 * tool-display.live-output-chars.test.ts — the GENERIC live output counter in
 * header summaries: any running tool whose `_streamingOutput` is a bare
 * cumulative count (the ContextAsk convention) shows "N chars" on its folded
 * row / header. Agent and Await are the first non-ContextAsk consumers; the
 * server streams the count from the subagent's text deltas
 * (narrator-event-handler's `noteSubagentOutputText`).
 */

import { describe, expect, it } from "bun:test";
import { getSummary } from "./tool-display";

describe("Agent header summary live output counter", () => {
	const input = { subagent_type: "explore", description: "survey auth flow" };

	it("appends the live count while the subagent streams", () => {
		expect(getSummary("Agent", input, { _streamingOutput: "1240" })).toBe(
			"explore: survey auth flow · 1240 chars",
		);
	});

	it("localizes the counter fragment", () => {
		expect(
			getSummary("Agent", input, { _streamingOutput: "1240" }, { liveOutputChars: "{count} 字符" }),
		).toBe("explore: survey auth flow · 1240 字符");
	});

	it("ignores a non-numeric streaming body and terminal cards", () => {
		expect(getSummary("Agent", input, { _streamingOutput: "thinking…" })).toBe(
			"explore: survey auth flow",
		);
		expect(getSummary("Agent", input)).toBe("explore: survey auth flow");
	});

	it("keeps the bare tool name when no type/description stream in", () => {
		expect(getSummary("Agent", {}, { _streamingOutput: "12" })).toBe("Agent · 12 chars");
	});
});

describe("Await header summary live output counter", () => {
	const input = { type: "agent", id: "worker-1" };

	it("appends the awaited child's live count", () => {
		expect(getSummary("Await", input, { _streamingOutput: "512" })).toBe(
			"agent: worker-1 · 512 chars",
		);
	});

	it("localizes the counter fragment", () => {
		expect(
			getSummary("Await", input, { _streamingOutput: "512" }, { liveOutputChars: "{count} 字符" }),
		).toBe("agent: worker-1 · 512 字符");
	});

	it("never appends for a non-numeric streaming body", () => {
		expect(getSummary("Await", input, { _streamingOutput: "waiting" })).toBe("agent: worker-1");
	});
});
