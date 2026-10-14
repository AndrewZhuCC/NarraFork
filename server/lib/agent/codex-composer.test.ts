import { describe, expect, test } from "bun:test";
import { parseCodexComposerSuggestion } from "@shared/codex-composer";
import {
	buildCodexComposerRequest,
	CODEX_COMPOSER_MAX_SEED_BYTES,
	createCodexComposerCapture,
} from "./codex-composer";

function ordinaryRequest() {
	return {
		model: "gpt-6.1-sol",
		instructions: "Keep this exact ordinary prefix.",
		tools: [{ type: "function", name: "Read", parameters: { type: "object" } }],
		reasoning: { effort: "xhigh", summary: "auto" },
		text: { verbosity: "low" },
		prompt_cache_key: "ordinary-key",
		client_metadata: { session_id: "session", thread_id: "thread", extra: "preserved" },
		input: [{ type: "message", role: "user", content: "Explain the cache." }],
	};
}

describe("Codex Composer request", () => {
	test("preserves cache-bearing fields and changes only the prediction suffix and metadata", () => {
		const source = ordinaryRequest();
		const before = structuredClone(source);
		const body = buildCodexComposerRequest(
			{ ...source, type: "response.create", previous_response_id: "resp_old" },
			"Next message",
		);
		for (const key of [
			"model",
			"instructions",
			"tools",
			"reasoning",
			"text",
			"prompt_cache_key",
		] as const) {
			expect(body[key]).toEqual(source[key]);
		}
		expect(body.previous_response_id).toBeUndefined();
		expect(body.type).toBeUndefined();
		expect(body.tool_choice).toBe("none");
		const metadata = body.client_metadata as Record<string, string>;
		expect(metadata.extra).toBe("preserved");
		expect(JSON.parse(metadata["x-codex-turn-metadata"] ?? "")).toEqual({
			session_id: "session",
			thread_id: "thread",
			turn_id: metadata.turn_id,
			request_kind: "turn",
			thread_source: "composer_predictions",
		});
		const input = body.input as unknown[];
		expect(input.slice(0, -1)).toEqual(source.input);
		expect(JSON.stringify(input.at(-1))).toContain("Return only JSON matching this schema");
		expect(source).toEqual(before);
	});

	test("captures the final assistant through the ordinary encoder and detaches the snapshot", async () => {
		const request = ordinaryRequest();
		let sent: Record<string, unknown> | undefined;
		const tail = { type: "message", role: "assistant", content: "Here is the explanation." };
		const capture = createCodexComposerCapture({
			request,
			providerId: "test",
			model: request.model,
			conversationId: "thread",
			configSignature: "test",
			prompt: "Next message",
			serializeAssistant: (items) => items.map(() => tail),
			send: async (body) => {
				sent = body;
				return { text: '{"suggestion":null}', usage: null };
			},
		});
		const seed = capture.finalize([{ id: "host-only-id" }]);
		expect(seed).toBeDefined();
		if (!seed || !request.input[0]) throw new Error("Expected a captured seed and request input");
		request.input[0].content = "A later mutation must not enter the snapshot.";
		await seed.run({ signal: new AbortController().signal });
		if (!sent) throw new Error("Expected Composer request to be sent");
		expect((sent.input as unknown[]).slice(0, -1)).toEqual([...ordinaryRequest().input, tail]);
		expect(JSON.stringify(sent)).not.toContain("host-only-id");
		request.instructions = "x".repeat(CODEX_COMPOSER_MAX_SEED_BYTES + 1);
		expect(capture.finalize([tail])).toBeUndefined();
		expect(capture.finalize([])).toBeUndefined();
	});

	test("requires the ordinary Codex identity rather than silently selecting another conversation", () => {
		expect(() => buildCodexComposerRequest({ input: [] }, "Next")).toThrow("identity");
	});

	test("accepts one short user message or abstention, not malformed model output", () => {
		expect(parseCodexComposerSuggestion('{"suggestion":"  继续说明缓存  "}')).toEqual({
			suggestion: "继续说明缓存",
		});
		expect(parseCodexComposerSuggestion('{"suggestion":null}')).toEqual({ suggestion: null });
		for (const value of [
			[],
			{},
			{ suggestion: 1 },
			{ suggestion: "ok", extra: true },
			{ suggestion: "a\nb" },
			{ suggestion: "x".repeat(241) },
		]) {
			expect(() => parseCodexComposerSuggestion(JSON.stringify(value))).toThrow();
		}
	});
});
