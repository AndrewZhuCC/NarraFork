import { afterEach, describe, expect, test } from "bun:test";
import { api } from "./index";

describe("misc APIs", () => {
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

	function installEnvironment(response: Response) {
		Object.defineProperty(g, "localStorage", {
			value: {
				getItem: () => null,
				setItem: () => {},
				removeItem: () => {},
			},
			configurable: true,
		});
		Object.defineProperty(g, "fetch", {
			value: async () => response,
			configurable: true,
		});
	}

	test("sends optional search sort without changing legacy requests", async () => {
		installEnvironment(new Response());
		const urls: string[] = [];
		Object.defineProperty(g, "fetch", {
			value: async (input: string) => {
				urls.push(input);
				return Response.json({ results: [] });
			},
			configurable: true,
		});
		await api.search("中文", "narrators", "time");
		await api.search("ab", "messages", "relevance");
		await api.search("legacy", "narrators");
		await api.search("wider", "messages", "time", undefined, ["subagents", "tools"]);
		expect(urls[0]).toContain("q=%E4%B8%AD%E6%96%87&entities=narrators&sort=time");
		expect(urls[1]).toContain("q=ab&entities=messages&sort=relevance");
		expect(urls[2]).toContain("q=legacy&entities=narrators");
		expect(urls[2]).not.toContain("sort=");
		expect(urls[2]).not.toContain("include=");
		expect(urls[3]).toContain("q=wider&entities=messages&sort=time&include=subagents,tools");
	});

	test("preserves search degraded metadata", async () => {
		installEnvironment(
			new Response(
				JSON.stringify({
					results: [],
					degraded: true,
					fallbacks: [
						{ entity: "narrators", from: "fts5", to: "like", reason: "fts_query_failed" },
					],
					searchMetadata: {
						degraded: true,
						mode: "degraded-like-fallback",
						ftsReady: false,
						shortQuery: false,
						requestedEntities: ["narrators"],
						fallbacks: [
							{ entity: "narrators", from: "fts5", to: "like", reason: "fts_query_failed" },
						],
					},
				}),
				{ status: 200, headers: { "content-type": "application/json" } },
			),
		);

		const response = await api.search("AlphaBeta", "narrators");
		expect(response.degraded).toBe(true);
		expect(response.searchMetadata?.mode).toBe("degraded-like-fallback");
		expect(response.searchMetadata?.fallbacks?.[0]?.reason).toBe("fts_query_failed");
	});

	test("preserves additive backend search metadata and entity arrays", async () => {
		installEnvironment(
			new Response(
				JSON.stringify({
					results: [
						{
							type: "narrator",
							id: "n_search_large_title",
							title: "auroraanchor Narrator North",
							snippet: "auroraanchor ranking fixture",
							matchField: "title",
							matchScore: 999,
						},
					],
					projects: [{ id: "p_search_fixture", name: "Search Fixture Project" }],
					narrators: [{ id: "n_search_large_title", type: "narrator" }],
					messages: [],
					degraded: false,
					fallbacks: [],
					searchMetadata: {
						degraded: false,
						mode: "fts5-with-like-fallback",
						ftsReady: true,
						shortQuery: false,
						requestedEntities: ["messages", "narrators"],
						fallbacks: [],
						backendDiagnostic: "ignored-by-frontend",
					},
				}),
				{ status: 200, headers: { "content-type": "application/json" } },
			),
		);

		const response = await api.search("auroraanchor", "messages,narrators");
		expect(response.results).toHaveLength(1);
		expect(response.degraded).toBe(false);
		expect(response.fallbacks).toEqual([]);
		expect(response.searchMetadata?.ftsReady).toBe(true);
		expect(response.searchMetadata?.backendDiagnostic).toBe("ignored-by-frontend");
		expect("projects" in response).toBe(true);
		expect("narrators" in response).toBe(true);
		expect("messages" in response).toBe(true);
	});

	test("surfaces structured storage cleanup errors", async () => {
		installEnvironment(
			new Response(
				JSON.stringify({
					code: "STORAGE_CLEANUP_WORKTREES_UNSUPPORTED",
					reason: "Storage cleanup does not remove git worktrees on this backend",
				}),
				{
					status: 403,
					statusText: "Forbidden",
					headers: { "content-type": "application/json" },
				},
			),
		);

		await expect(api.cleanupStorage("worktrees")).rejects.toThrow(
			"Storage cleanup does not remove git worktrees on this backend",
		);
	});

	test("surfaces structured runtime cleanup errors", async () => {
		installEnvironment(
			new Response(
				JSON.stringify({
					code: "RUNTIME_CLEANUP_CONTAINERS_UNSUPPORTED",
					reason: "Runtime cleanup is limited to safe semantics on this backend",
				}),
				{
					status: 403,
					statusText: "Forbidden",
					headers: { "content-type": "application/json" },
				},
			),
		);

		await expect(api.cleanupRuntime("containers")).rejects.toThrow(
			"Runtime cleanup is limited to safe semantics on this backend",
		);
	});

	test("surfaces structured database cleanup errors", async () => {
		installEnvironment(
			new Response(
				JSON.stringify({
					code: "STORAGE_DATABASE_CLEANUP_DISABLED",
					reason: "Database vacuum is disabled on this backend",
				}),
				{
					status: 403,
					statusText: "Forbidden",
					headers: { "content-type": "application/json" },
				},
			),
		);

		await expect(
			api.cleanupDatabase({ target: "toolCallPayloads", olderThanDays: 7 }),
		).rejects.toThrow("Database vacuum is disabled on this backend");
	});

	test("surfaces structured notification webhook errors", async () => {
		for (const tc of [
			{
				provider: "dingtalk",
				code: "NOTIFICATION_DINGTALK_WEBHOOK_FAILED",
				call: () => api.testDingtalkWebhook("https://example.test/dingtalk"),
			},
			{
				provider: "feishu",
				code: "NOTIFICATION_FEISHU_WEBHOOK_FAILED",
				call: () => api.testFeishuWebhook("https://example.test/feishu"),
			},
		]) {
			const diagnostic = `${tc.provider} upstream returned 500`;
			installEnvironment(
				new Response(
					JSON.stringify({
						ok: false,
						code: tc.code,
						reason: diagnostic,
						error: diagnostic,
						message: diagnostic,
					}),
					{
						status: 502,
						statusText: "Bad Gateway",
						headers: { "content-type": "application/json" },
					},
				),
			);

			await expect(tc.call()).rejects.toThrow(diagnostic);
		}
	});
});
