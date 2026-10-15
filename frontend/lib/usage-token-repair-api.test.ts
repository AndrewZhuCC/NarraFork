import { afterEach, beforeEach, expect, test } from "bun:test";
import { ApiError } from "./api/client";
import { usageTokenRepairApi as api } from "./usage-token-repair-api";

const originals = new Map<string, PropertyDescriptor | undefined>();
let calls: { url: string; options?: RequestInit }[];
let status = 200;
beforeEach(() => {
	calls = [];
	status = 200;
	for (const [key, value] of Object.entries({
		localStorage: { getItem: () => "test-token", setItem: () => {}, removeItem: () => {} },
		fetch: async (input: RequestInfo | URL, options?: RequestInit) => {
			calls.push({ url: String(input), options });
			return new Response(JSON.stringify(status === 200 ? {} : { reason: "repair denied" }), {
				status,
				headers: { "content-type": "application/json" },
			});
		},
	})) {
		originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
	}
});
afterEach(() => {
	for (const [key, descriptor] of originals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	originals.clear();
});
test("apply submits candidate IDs and explicit confirmation, never arbitrary counters", async () => {
	await api.apply("job/one", ["candidate"]);
	expect(calls[0]?.url).toBe("/api/usage-history/token-repair/jobs/job%2Fone/apply");
	expect(calls[0]?.options?.method).toBe("POST");
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({
		candidateIds: ["candidate"],
		confirm: true,
	});
	expect((calls[0]?.options?.headers as Record<string, string>).Authorization).toBe(
		"Bearer test-token",
	);
});
test("GETs preserve abort signals and encode pagination cursor", async () => {
	const { signal } = new AbortController();
	await api.probe(signal);
	await api.jobs(signal);
	await api.job("one", signal);
	await api.candidates("one", "a+b/c", signal);
	expect(calls.map((call) => call.url)).toEqual([
		"/api/usage-history/token-repair/probe",
		"/api/usage-history/token-repair/jobs",
		"/api/usage-history/token-repair/jobs/one",
		"/api/usage-history/token-repair/jobs/one/candidates?cursor=a%2Bb%2Fc",
	]);
	for (const call of calls) expect(call.options?.signal).toBe(signal);
});
test("manual analysis and ending a job use the existing authenticated client", async () => {
	await api.start(true);
	await api.cancel("one");
	expect(calls.map((call) => call.options?.method)).toEqual(["POST", "POST"]);
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({
		includeReviewed: true,
		protocolOverrides: [],
	});
	expect(calls[1]?.url.endsWith("/cancel")).toBe(true);
});
test("manual API rules preserve exact provider and model identity in the start payload", async () => {
	const protocolOverrides = [
		{ provider: null, model: "same", protocol: "openai" as const },
		{ provider: "other", model: "same", protocol: "gemini" as const },
	];
	await api.start(true, protocolOverrides);
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({
		includeReviewed: true,
		protocolOverrides,
	});
});
test("reanalysis scopes both manual and automatic choices to the source job only when provided", async () => {
	const overrides = [{ provider: "p", model: "same", protocol: "anthropic" as const }];
	await api.start(true, overrides, "source-job");
	await api.start(true, [], "source-job");
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({
		includeReviewed: true,
		protocolOverrides: overrides,
		sourceJobId: "source-job",
	});
	expect(JSON.parse(String(calls[1]?.options?.body))).toEqual({
		includeReviewed: true,
		protocolOverrides: [],
		sourceJobId: "source-job",
	});
});
test("structured server failures propagate instead of displaying success", async () => {
	status = 403;
	try {
		await api.probe();
		throw new Error("expected failure");
	} catch (error) {
		expect(error).toBeInstanceOf(ApiError);
		expect((error as ApiError).status).toBe(403);
	}
});
