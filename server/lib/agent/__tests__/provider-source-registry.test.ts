import { describe, expect, test } from "bun:test";
import type { ProviderAdapter } from "../provider";
import { type ProviderSource, ProviderSourceRegistry } from "../provider-source-registry";

const entry = { id: "future:instance", prefix: "future", name: "Future", ready: true };
const adapter = {} as ProviderAdapter;
const source = (): ProviderSource => ({
	resolve: () => adapter,
	listReadiness: () => [entry],
});

describe("ProviderSourceRegistry", () => {
	test("requires both routing and readiness at registration", () => {
		const registry = new ProviderSourceRegistry();
		for (const incomplete of [{ resolve: () => adapter }, { listReadiness: () => [entry] }]) {
			expect(() =>
				registry.register("incomplete", incomplete as unknown as ProviderSource),
			).toThrow(TypeError);
		}
		expect(registry.resolve("future", "future:chat")).toBeNull();
	});

	test("isolates throwing and rejected readiness sources", async () => {
		const registry = new ProviderSourceRegistry();
		registry.register("throws", {
			resolve: () => null,
			listReadiness: () => {
				throw new Error("local source failed");
			},
		});
		registry.register("rejects", {
			resolve: () => null,
			listReadiness: async () => {
				throw new Error("async source failed");
			},
		});
		registry.register("healthy", source());
		expect(await registry.listReadiness(new Set())).toEqual([entry]);
	});

	test("times out and aborts a stalled source before continuing", async () => {
		const registry = new ProviderSourceRegistry();
		let signal: AbortSignal | undefined;
		let abortCount = 0;
		registry.register("stalled", {
			resolve: () => null,
			listReadiness: (receivedSignal) => {
				signal = receivedSignal;
				receivedSignal.addEventListener("abort", () => {
					abortCount++;
				});
				return new Promise(() => {});
			},
		});
		registry.register("healthy", source());
		expect(await registry.listReadiness(new Set())).toEqual([entry]);
		expect(signal?.aborted).toBe(true);
		expect(abortCount).toBe(1);
	}, 5_000);

	test("a replaced registration cannot be removed by its old disposer", async () => {
		const registry = new ProviderSourceRegistry();
		const disposeOld = registry.register("external", source());
		const replacementAdapter = {} as ProviderAdapter;
		const disposeNew = registry.register("external", {
			resolve: () => replacementAdapter,
			listReadiness: () => [{ ...entry, id: "replacement" }],
		});
		disposeOld();
		expect(registry.resolve("future", "future:chat")).toBe(replacementAdapter);
		expect(await registry.listReadiness(new Set())).toEqual([{ ...entry, id: "replacement" }]);
		disposeNew();
		disposeNew();
		expect(registry.resolve("future", "future:chat")).toBeNull();
		expect(await registry.listReadiness(new Set())).toEqual([]);
	});

	test("propagates a known provider routing error instead of falling through", () => {
		const registry = new ProviderSourceRegistry();
		const error = Object.assign(new Error("provider unavailable"), {
			code: "PROVIDER_UNAVAILABLE",
		});
		let fallbackCalls = 0;
		registry.register("known", {
			resolve: () => {
				throw error;
			},
			listReadiness: () => [],
		});
		registry.register("fallback", {
			resolve: () => {
				fallbackCalls++;
				return adapter;
			},
			listReadiness: () => [],
		});
		expect(() => registry.resolve("future", "future:chat")).toThrow(error);
		expect(fallbackCalls).toBe(0);
	});

	test("accepts exact ID and prefix limits, rejects oversized values, and truncates names", async () => {
		const registry = new ProviderSourceRegistry();
		const boundary = {
			id: "i".repeat(256),
			prefix: "p".repeat(128),
			name: "名".repeat(257),
			ready: true,
			apiKey: "must-not-leak",
		};
		registry.register("bounded", {
			resolve: () => null,
			listReadiness: () => [
				{ ...entry, id: "i".repeat(257) },
				{ ...entry, id: "oversized-prefix", prefix: "p".repeat(129) },
				{ ...entry, id: "" },
				{ ...entry, prefix: "" },
				boundary,
				entry,
			],
		});
		expect(await registry.listReadiness(new Set())).toEqual([
			{ id: boundary.id, prefix: boundary.prefix, name: "名".repeat(256), ready: true },
			entry,
		]);
	});

	test("bounds the total UTF-8 JSON output to 512 KiB across multiple sources", async () => {
		const registry = new ProviderSourceRegistry();
		const entries = Array.from({ length: 360 }, (_, index) => ({
			id: `${index}:${"标".repeat(256 - `${index}:`.length)}`,
			prefix: "前".repeat(128),
			name: "名".repeat(256),
			ready: true,
		}));
		registry.register("first", { resolve: () => null, listReadiness: () => entries.slice(0, 180) });
		registry.register("second", { resolve: () => null, listReadiness: () => entries.slice(180) });
		const readiness = await registry.listReadiness(new Set());
		expect(readiness.length).toBeGreaterThan(180);
		expect(readiness.length).toBeLessThan(entries.length);
		expect(readiness).toEqual(entries.slice(0, readiness.length));
		expect(Buffer.byteLength(JSON.stringify(readiness))).toBeLessThanOrEqual(512 * 1024);
		expect(
			Buffer.byteLength(JSON.stringify([...readiness, entries[readiness.length]])),
		).toBeGreaterThan(512 * 1024);
	});

	test("projects only public fields and applies disabled prefixes without constructing adapters", async () => {
		const registry = new ProviderSourceRegistry();
		let resolveCalls = 0;
		registry.register("external", {
			resolve: () => {
				resolveCalls++;
				throw new Error("must not construct an adapter");
			},
			listReadiness: () => [{ ...entry, apiKey: "secret", config: { token: "secret" } }],
		});
		expect(await registry.listReadiness(new Set(["future"]))).toEqual([{ ...entry, ready: false }]);
		expect(resolveCalls).toBe(0);
	});
});
