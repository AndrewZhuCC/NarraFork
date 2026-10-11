import { describe, expect, test } from "bun:test";
import {
	listGitHubReleaseSummaries,
	listGitHubRestReleaseSummaries,
} from "../../scripts/lib/github-release-summary";

const repository = "Example/Custom";
function restPage(start = 1, length = 1) {
	return Array.from({ length }, (_, index) => ({
		id: start + index,
		url: `https://api.github.com/repos/${repository}/releases/${start + index}`,
		tagName: `v1.${start + index}.0`,
		draft: true,
		prerelease: false,
		publishedAt: null as string | null,
		assetCount: 0,
	}));
}
function page(start = 1, length = 1, hasNextPage = false, endCursor: string | null = null) {
	return {
		data: {
			repository: {
				nameWithOwner: repository,
				releases: {
					nodes: Array.from({ length }, (_, index) => ({
						databaseId: start + index,
						tagName: `v1.${start + index}.0`,
						isDraft: false,
						isPrerelease: false,
						publishedAt: "2026-10-08T00:00:00Z",
						releaseAssets: { totalCount: 160 },
					})),
					pageInfo: { hasNextPage, endCursor },
				},
			},
		},
	};
}

describe("bounded authenticated REST release summaries", () => {
	test("requests REST pages with jq output projection, including drafts invisible to GraphQL", async () => {
		const calls: string[][] = [];
		const result = await listGitHubRestReleaseSummaries((args) => {
			calls.push(args);
			return JSON.stringify(restPage());
		}, repository);
		expect(result).toEqual([
			{
				id: 1,
				tagName: "v1.1.0",
				draft: true,
				prerelease: false,
				publishedAt: null,
				assetCount: 0,
			},
		]);
		expect(calls).toHaveLength(1);
		expect(calls[0]?.slice(0, 3)).toEqual([
			"api",
			`repos/${repository}/releases?per_page=100&page=1`,
			"--jq",
		]);
		const projection = calls[0]?.[3] ?? "";
		expect(projection).toContain("assetCount:(.assets|length)");
		expect(projection).toContain("publishedAt:.published_at");
		expect(projection).not.toContain("body");
		expect(calls.flat()).not.toContain("--paginate");
	});
	test("full pages are followed until a short page, including an empty terminal page", async () => {
		let calls = 0;
		const result = await listGitHubRestReleaseSummaries((args) => {
			calls++;
			expect(args[1]).toBe(`repos/${repository}/releases?per_page=100&page=${calls}`);
			return JSON.stringify(calls === 1 ? restPage(1, 100) : []);
		}, repository);
		expect(result).toHaveLength(100);
		expect(calls).toBe(2);
	});
	test("ten full pages fail closed, even when a wanted release was on page one", async () => {
		let calls = 0;
		await expect(
			listGitHubRestReleaseSummaries(
				() => JSON.stringify(restPage(++calls * 100, 100)),
				repository,
			),
		).rejects.toThrow("pagination limit");
		expect(calls).toBe(10);
	});
	for (const duplicate of ["id", "tag"])
		test(`rejects cross-page duplicate ${duplicate}`, async () => {
			let calls = 0;
			await expect(
				listGitHubRestReleaseSummaries(() => {
					if (++calls === 1) return JSON.stringify(restPage(1, 100));
					const node = restPage(101)[0] as ReturnType<typeof restPage>[number];
					if (duplicate === "id") {
						node.id = 1;
						node.url = `https://api.github.com/repos/${repository}/releases/1`;
					} else node.tagName = "v1.1.0";
					return JSON.stringify([node]);
				}, repository),
			).rejects.toThrow(duplicate === "id" ? "Duplicate" : "Ambiguous");
		});
	test("invalid JSON, repository/ID identity, state and fields never prove absence", async () => {
		const node = restPage()[0] as ReturnType<typeof restPage>[number];
		const invalid: unknown[] = [
			null,
			{},
			[null],
			restPage(1, 101),
			[{ ...node, id: 0 }],
			[{ ...node, id: Number.MAX_SAFE_INTEGER + 1 }],
			[{ ...node, assetCount: -1 }],
			[{ ...node, draft: "true" }],
			[{ ...node, prerelease: null }],
			[{ ...node, tagName: "" }],
			[{ ...node, publishedAt: "2026-10-09T00:00:00Z" }],
			[{ ...node, draft: false }],
			[{ ...node, publishedAt: "not-a-date" }],
			[{ ...node, url: "https://api.github.com/repos/Other/repo/releases/1" }],
			[{ ...node, url: `https://api.github.com/repos/${repository}/releases/2` }],
			[{ ...node, url: `${node.url}?repo=other` }],
			[{ ...node, url: node.url.replace("api.github.com", "api.github.com.evil.test") }],
		];
		for (const value of invalid)
			await expect(
				listGitHubRestReleaseSummaries(() => JSON.stringify(value), repository),
			).rejects.toThrow();
		await expect(listGitHubRestReleaseSummaries(() => "{", repository)).rejects.toThrow();
		await expect(
			listGitHubRestReleaseSummaries(() => " ".repeat(128 * 1024 + 1), repository),
		).rejects.toThrow("limit");
	});
	test("403, 401 and transport errors propagate instead of an empty history", async () => {
		for (const message of ["HTTP 403", "HTTP 401", "network timeout"])
			await expect(
				listGitHubRestReleaseSummaries(() => {
					throw new Error(message);
				}, repository),
			).rejects.toThrow(message);
	});
});

describe("bounded GitHub release summaries", () => {
	test("requests only lightweight GraphQL fields and maps releases", async () => {
		const calls: string[][] = [];
		const result = await listGitHubReleaseSummaries((args) => {
			calls.push(args);
			return JSON.stringify(page());
		}, repository);
		expect(result).toEqual([
			{
				id: 1,
				tagName: "v1.1.0",
				draft: false,
				prerelease: false,
				publishedAt: "2026-10-08T00:00:00Z",
				assetCount: 160,
			},
		]);
		const args = calls[0];
		expect(args.slice(0, 2)).toEqual(["api", "graphql"]);
		expect(args).toContain("owner=Example");
		expect(args).toContain("name=Custom");
		const query = args.find((arg) => arg.startsWith("query=")) ?? "";
		expect(query).toContain("releases(first:100,after:$cursor)");
		expect(query).toContain("releaseAssets(first:1){totalCount}");
		expect(query).not.toMatch(/\b(body|description|downloadUrl|url)\b/);
	});
	test("cursor pagination accepts a complete last full page", async () => {
		let calls = 0;
		const result = await listGitHubReleaseSummaries((args) => {
			calls++;
			if (calls === 2) expect(args).toContain("cursor=next");
			return JSON.stringify(calls === 1 ? page(1, 100, true, "next") : page(101, 100));
		}, repository);
		expect(result).toHaveLength(200);
		expect(calls).toBe(2);
	});
	test("ten pages cannot silently truncate additional releases", async () => {
		let calls = 0;
		await expect(
			listGitHubReleaseSummaries(() => {
				calls++;
				return JSON.stringify(page(calls * 100, 100, true, String(calls)));
			}, repository),
		).rejects.toThrow("pagination limit");
		expect(calls).toBe(10);
	});
	test("fails on invalid nodes, foreign repository, GraphQL errors and bad pages", async () => {
		const invalid: unknown[] = [null, {}, { errors: [{ message: "rate limit" }], ...page() }];
		for (const mutate of [
			(value: ReturnType<typeof page>) => {
				value.data.repository.nameWithOwner = "Other/Repo";
			},
			(value: ReturnType<typeof page>) => {
				value.data.repository.releases.nodes[0].databaseId = 0;
			},
			(value: ReturnType<typeof page>) => {
				value.data.repository.releases.nodes[0].releaseAssets.totalCount = -1;
			},
			(value: ReturnType<typeof page>) => {
				value.data.repository.releases.nodes.push(value.data.repository.releases.nodes[0]);
			},
			(value: ReturnType<typeof page>) => {
				value.data.repository.releases.pageInfo.hasNextPage = true;
			},
		]) {
			const value = page();
			mutate(value);
			invalid.push(value);
		}
		for (const value of invalid)
			await expect(
				listGitHubReleaseSummaries(() => JSON.stringify(value), repository),
			).rejects.toThrow();
		await expect(listGitHubReleaseSummaries(() => "{", repository)).rejects.toThrow();
		await expect(
			listGitHubReleaseSummaries(() => " ".repeat(1024 * 1024 + 1), repository),
		).rejects.toThrow("limit");
	});
	test("repeated cursors and transport errors are not an empty history", async () => {
		let calls = 0;
		await expect(
			listGitHubReleaseSummaries(
				() => JSON.stringify(page(++calls * 100, 100, true, "same")),
				repository,
			),
		).rejects.toThrow("pagination");
		await expect(
			listGitHubReleaseSummaries(() => {
				throw new Error("HTTP 403");
			}, repository),
		).rejects.toThrow("403");
	});
});
