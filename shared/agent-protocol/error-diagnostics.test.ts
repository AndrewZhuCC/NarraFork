import { describe, expect, test } from "bun:test";
import { MAX_DIAGNOSTIC_JSON_CHARS, normalizeApiRequestDiagnostics } from "./error-diagnostics";

describe("bounded local error association", () => {
	test("keeps local IDs distinct from upstream IDs under UTF-8 byte pressure", () => {
		const result = normalizeApiRequestDiagnostics({
			recordingRequestId: "local-attempt",
			apiRequestId: "database-row",
			requestId: "upstream-request",
			providerRequestId: "upstream-provider",
			message: "界😀".repeat(8192),
			responseSnippet: "界😀".repeat(8192),
			cause: "界😀".repeat(8192),
			endpoint: "界😀".repeat(8192),
		});
		expect(result).toMatchObject({
			recordingRequestId: "local-attempt",
			apiRequestId: "database-row",
			requestId: "upstream-request",
			providerRequestId: "upstream-provider",
		});
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThanOrEqual(
			MAX_DIAGNOSTIC_JSON_CHARS,
		);
	});

	test("rejects oversized association IDs rather than truncating identity", () => {
		const result = normalizeApiRequestDiagnostics({
			recordingRequestId: "界".repeat(86),
			apiRequestId: "a".repeat(257),
			requestId: "upstream",
		});
		expect(result).not.toHaveProperty("recordingRequestId");
		expect(result).not.toHaveProperty("apiRequestId");
		expect(result?.requestId).toBe("upstream");
	});

	test("old diagnostics stay unassociated and empty diagnostics stay absent", () => {
		expect(normalizeApiRequestDiagnostics(undefined)).toBeUndefined();
		expect(normalizeApiRequestDiagnostics({})).toBeUndefined();
		expect(normalizeApiRequestDiagnostics({ requestId: "legacy-upstream" })).toEqual({
			schema: "narrafork.error-diagnostics.v1",
			requestId: "legacy-upstream",
		});
	});
});
