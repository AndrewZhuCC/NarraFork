import { afterEach, describe, expect, test } from "bun:test";
import { ApiError } from "./client";
import { assertKiroChatRouteSupported, guardedKiroStreamChat, kiroStreamChat } from "./streams";

describe("kiroStreamChat", () => {
	const g = globalThis as typeof globalThis & {
		localStorage?: Storage;
		fetch: typeof fetch;
	};
	const originalFetch = g.fetch;
	const originalLocalStorage = g.localStorage;

	afterEach(() => {
		Object.defineProperty(g, "fetch", { value: originalFetch, configurable: true });
		if (originalLocalStorage === undefined) {
			Reflect.deleteProperty(g, "localStorage");
		} else {
			Object.defineProperty(g, "localStorage", { value: originalLocalStorage, configurable: true });
		}
	});

	test("surfaces structured Kiro chat errors", async () => {
		Object.defineProperty(g, "localStorage", {
			value: {
				getItem: () => null,
				setItem: () => {},
				removeItem: () => {},
			},
			configurable: true,
		});
		Object.defineProperty(g, "fetch", {
			value: async () =>
				new Response(
					JSON.stringify({
						code: "KIRO_CHAT_UNAVAILABLE",
						reason: "Kiro chat is unavailable",
					}),
					{
						status: 503,
						statusText: "Service Unavailable",
						headers: { "content-type": "application/json" },
					},
				),
			configurable: true,
		});

		const generator = kiroStreamChat("hello", "model-1");
		try {
			await generator.next();
			throw new Error("expected Kiro chat to fail");
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).status).toBe(503);
			expect((err as Error).message).toBe("Kiro chat is unavailable");
			expect((err as ApiError).data?.code).toBe("KIRO_CHAT_UNAVAILABLE");
		}
	});

	test("blocks Kiro chat before fetch when route capability is disabled", async () => {
		let fetched = false;
		Object.defineProperty(g, "fetch", {
			value: async () => {
				fetched = true;
				return new Response("");
			},
			configurable: true,
		});

		const capabilities = {
			providers: {
				kiro: {
					routes: {
						supported: true,
						chat: false,
						chatReason: "Kiro chat streaming is disabled on this backend",
					},
				},
			},
		};

		expect(() => assertKiroChatRouteSupported(capabilities)).toThrow(
			"Kiro chat streaming is disabled on this backend",
		);
		const generator = guardedKiroStreamChat(capabilities, "hello", "model-1");
		try {
			await generator.next();
			throw new Error("expected guarded Kiro chat to fail");
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).status).toBe(501);
			expect((err as ApiError).data?.feature).toBe("providers.kiro.routes.chat");
			expect(fetched).toBe(false);
		}
	});

	test("surfaces structured Kiro stream error events", async () => {
		Object.defineProperty(g, "localStorage", {
			value: {
				getItem: () => null,
				setItem: () => {},
				removeItem: () => {},
			},
			configurable: true,
		});
		Object.defineProperty(g, "fetch", {
			value: async () =>
				new Response(
					'event: error\ndata: {"code":"KIRO_STREAM_ERROR","reason":"Kiro stream is unavailable"}\n\n',
					{
						status: 200,
						headers: { "content-type": "text/event-stream" },
					},
				),
			configurable: true,
		});

		const generator = kiroStreamChat("hello", "model-1");
		try {
			await generator.next();
			throw new Error("expected Kiro stream to fail");
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).status).toBe(500);
			expect((err as Error).message).toBe("Kiro stream is unavailable");
			expect((err as ApiError).data?.code).toBe("KIRO_STREAM_ERROR");
		}
	});
});
