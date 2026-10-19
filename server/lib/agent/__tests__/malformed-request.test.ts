import { describe, expect, test } from "bun:test";
import { isMalformedRequestBodyError, summarizeRequestBody } from "../malformed-request";
import { ApiError } from "../types";

const UPSTREAM_BODY = JSON.stringify({
	__type: "ValidationException",
	message: "Improperly formed request.",
	reason: "REQUEST_BODY_INVALID",
});

describe("isMalformedRequestBodyError", () => {
	test("detects the raw upstream ValidationException body", () => {
		expect(isMalformedRequestBodyError(UPSTREAM_BODY)).toBe(true);
	});

	test("detects the error as thrown by the api client (message carries the body)", () => {
		const err = new Error(`Error: ${UPSTREAM_BODY}`);
		expect(isMalformedRequestBodyError(err)).toBe(true);
	});

	test("detects an ApiError-shaped object with the body in message", () => {
		expect(
			isMalformedRequestBodyError({
				name: "ApiError",
				code: "BAD_REQUEST",
				statusCode: 400,
				message: UPSTREAM_BODY,
			}),
		).toBe(true);
	});

	test("detects an ApiError carrying diagnostics from the NUG relay", () => {
		const err = new ApiError(400, "NUG chat error 400", {
			schema: "narrafork.error-diagnostics.v1",
			statusCode: 400,
			reason: "REQUEST_BODY_INVALID",
			responseSnippet: "Improperly formed request.",
		});
		expect(isMalformedRequestBodyError(err)).toBe(true);
	});

	test("detects the invalidState shape the loop builds from a stream error event", () => {
		expect(
			isMalformedRequestBodyError({
				reason: "validation_exception",
				message: "Improperly formed request.",
				diagnostics: { schema: "narrafork.error-diagnostics.v1", statusCode: 400 },
			}),
		).toBe(true);
	});

	test("ignores unrelated errors", () => {
		expect(isMalformedRequestBodyError(new Error("429 Too Many Requests"))).toBe(false);
		expect(isMalformedRequestBodyError({ message: "context length exceeded" })).toBe(false);
		expect(isMalformedRequestBodyError(null)).toBe(false);
		expect(isMalformedRequestBodyError(undefined)).toBe(false);
		expect(isMalformedRequestBodyError("")).toBe(false);
	});
});

describe("summarizeRequestBody", () => {
	test("flags an orphaned toolResult and a toolUse without a result", () => {
		const summary = summarizeRequestBody({
			model: "claude-sonnet-4.5",
			messages: [
				{ role: "user", content: "hi" },
				{
					role: "assistant",
					content: [{ type: "tool_use", id: "tu-1", name: "Bash", input: {} }],
				},
				{
					role: "user",
					content: [{ type: "tool_result", tool_use_id: "tu-orphan", content: "x" }],
				},
			],
		});

		expect(summary?.modelId).toBe("claude-sonnet-4.5");
		expect(summary?.historyLength).toBe(3);
		expect(summary?.roleSequence).toBe("UAU");
		expect(summary?.unmatchedToolUseIds).toEqual(["tu-1"]);
		expect(summary?.unmatchedToolResultIds).toEqual(["tu-orphan"]);
		expect(summary?.notes).toContain("toolResult without a matching toolUse (orphaned result)");
	});

	test("flags reasoning blocks without a signature and empty image bytes", () => {
		const summary = summarizeRequestBody({
			model: "claude-opus-4.5",
			messages: [
				{
					role: "assistant",
					content: [{ type: "thinking", thinking: "abc" }],
				},
				{
					role: "user",
					content: [
						{ type: "text", text: "look" },
						{ type: "image", source: { type: "base64", media_type: "image/png", data: "" } },
					],
				},
			],
		});

		expect(summary?.reasoningBlocks).toBe(1);
		expect(summary?.reasoningWithoutSignature).toBe(1);
		expect(summary?.imageCount).toBe(1);
		expect(summary?.emptyImageCount).toBe(1);
		expect(summary?.notes).toContain("image with empty source data present");
	});

	test("reports a non-conversational body instead of throwing", () => {
		const summary = summarizeRequestBody({ foo: "bar" });
		expect(summary?.notes?.[0]).toContain("no messages array");
	});

	test("returns undefined for a non-object body", () => {
		expect(summarizeRequestBody("not-a-body")).toBeUndefined();
		expect(summarizeRequestBody(undefined)).toBeUndefined();
	});
});
