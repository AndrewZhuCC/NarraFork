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
			return new Response(JSON.stringify(status < 400 ? {} : { reason: "repair denied" }), {
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
test("apply submits immutable preview version and explicit confirmation, accepts 202", async () => {
	status = 202;
	await api.apply("job/one", "immutable-version");
	expect(calls[0]?.url).toBe("/api/usage-history/token-repair/jobs/job%2Fone/apply");
	expect(calls[0]?.options?.method).toBe("POST");
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({
		previewVersion: "immutable-version",
		confirm: true,
	});
	expect((calls[0]?.options?.headers as Record<string, string>).Authorization).toBe(
		"Bearer test-token",
	);
});
test("GETs preserve abort signals and only models are paginated", async () => {
	const { signal } = new AbortController();
	await api.probe(signal);
	await api.jobs(signal);
	await api.job("one", signal);
	await api.models("one", "a+b/c", signal);
	expect(calls.map((call) => call.url)).toEqual([
		"/api/usage-history/token-repair/probe",
		"/api/usage-history/token-repair/jobs",
		"/api/usage-history/token-repair/jobs/one",
		"/api/usage-history/token-repair/jobs/one/models?cursor=a%2Bb%2Fc",
	]);
	for (const call of calls) expect(call.options?.signal).toBe(signal);
	expect("candidates" in api).toBe(false);
});
test("scan includes reviewed by default and cancel uses existing authenticated client", async () => {
	await api.start();
	await api.cancel("one");
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({ includeReviewed: true });
	expect(calls[1]?.url.endsWith("/cancel")).toBe(true);
});
test("dry-run posts exact provider/model rules and accepts asynchronous response", async () => {
	status = 202;
	const protocolOverrides = [
		{ provider: null, model: "same", protocol: "openai" as const },
		{ provider: "other", model: "same", protocol: "gemini" as const },
	];
	await api.dryRun("job/one", protocolOverrides);
	expect(calls[0]?.url).toBe("/api/usage-history/token-repair/jobs/job%2Fone/dry-run");
	expect(JSON.parse(String(calls[0]?.options?.body))).toEqual({ protocolOverrides });
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
