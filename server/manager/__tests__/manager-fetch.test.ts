import { afterEach, describe, expect, test } from "bun:test";
import { requestManagedShutdown } from "../control-client";
import { managerFetch } from "../manager-fetch";

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
});

describe("managerFetch", () => {
	test("本机请求放行自签证书，并保留调用方的方法和请求头", async () => {
		let seen: { url: string; init?: RequestInit } | undefined;
		globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
			seen = { url: String(url), init };
			return new Response(null, { status: 204 });
		}) as typeof fetch;

		const response = await managerFetch("https://127.0.0.1:7779/api/health", {
			method: "GET",
			headers: { authorization: "Bearer token" },
		});

		expect(response.status).toBe(204);
		expect(seen?.url).toBe("https://127.0.0.1:7779/api/health");
		expect(seen?.init?.method).toBe("GET");
		expect(seen?.init?.headers).toEqual({ authorization: "Bearer token" });
		expect(seen?.init).toMatchObject({ tls: { rejectUnauthorized: false } });
	});
});

describe("requestManagedShutdown", () => {
	test("退出请求打到 signal，并带上自签证书放行", async () => {
		let seen: { url: string; init?: RequestInit } | undefined;
		globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
			seen = { url: String(url), init };
			return new Response(null, { status: 202 });
		}) as typeof fetch;

		expect(await requestManagedShutdown("https://127.0.0.1:7779", "token")).toBe(true);
		expect(seen?.url).toBe("https://127.0.0.1:7779/api/system/lifecycle/signal");
		expect(seen?.init?.method).toBe("POST");
		expect(seen?.init?.headers).toEqual({ authorization: "Bearer token" });
		expect(seen?.init).toMatchObject({ tls: { rejectUnauthorized: false } });
	});
});
