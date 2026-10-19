import { afterEach, expect, test } from "bun:test";
import {
	getCachedCommandCodeModel,
	invalidateCommandCodeModelsCache,
	listCommandCodeModels,
} from "../commandcode-models";

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
	invalidateCommandCodeModelsCache();
});

test("a failed model fetch is not repeated inside the negative cache window", async () => {
	let calls = 0;
	globalThis.fetch = (async () => {
		calls += 1;
		throw new Error("upstream down");
	}) as unknown as typeof fetch;

	const first = await listCommandCodeModels("user_test_key", "https://models.example/provider/v1");
	const second = await listCommandCodeModels("user_test_key", "https://models.example/provider/v1");

	expect(first).toEqual({ models: [], ok: false });
	expect(second).toEqual({ models: [], ok: false });
	expect(calls).toBe(1);
});

test("a successful model fetch is reused and keeps supported endpoints available", async () => {
	let calls = 0;
	globalThis.fetch = (async () => {
		calls += 1;
		return new Response(
			JSON.stringify({
				data: [{ id: "claude-haiku-5-5", supported_endpoints: ["/messages"] }],
			}),
			{ status: 200, headers: { "Content-Type": "application/json" } },
		);
	}) as unknown as typeof fetch;

	const first = await listCommandCodeModels("user_test_key");
	const second = await listCommandCodeModels("user_test_key");
	expect(first).toEqual({ models: ["claude-haiku-5-5"], ok: true });
	expect(second.ok).toBe(true);
	expect(calls).toBe(1);
	expect(getCachedCommandCodeModel("claude-haiku-5-5")?.endpoints).toEqual(["/messages"]);
});
