import { afterEach, describe, expect, test } from "bun:test";
import { downloadUpdate } from "../update-client";
import { MANAGER_UPDATE_DOWNLOAD_LIMIT, managerUpdateDownloadBody } from "../update-download-body";

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
});

describe("managerUpdateDownloadBody", () => {
	test("只回传版本、摘要和来源，长发行说明不会顶破 64 KiB", () => {
		const body = managerUpdateDownloadBody({
			version: "1.2.3",
			sha512: `${"a".repeat(86)}==`,
			sourceIdentity: { source: "github", repository: "NarraFork/NarraFork" },
			releaseNotes: "记".repeat(128 * 1024),
			files: [{ url: "https://example.invalid/narrafork", size: 10, sha512: "x" }],
			path: "https://example.invalid/narrafork",
		});
		expect(body).toEqual({
			releaseInfo: {
				version: "1.2.3",
				sha512: `${"a".repeat(86)}==`,
				sourceIdentity: { source: "github", repository: "NarraFork/NarraFork" },
			},
		});
		expect(Buffer.byteLength(JSON.stringify(body))).toBeLessThan(MANAGER_UPDATE_DOWNLOAD_LIMIT);
	});

	test("下载请求发出去的就是裁剪后的身份", async () => {
		let body = "";
		let tls: unknown;
		globalThis.fetch = (async (_url: string | URL | Request, init?: RequestInit) => {
			body = String(init?.body ?? "");
			tls = (init as { tls?: unknown } | undefined)?.tls;
			const stream = new ReadableStream({
				start(controller) {
					controller.enqueue(
						new TextEncoder().encode(
							'event: complete\ndata: {"success":true,"version":"1.2.3"}\n\n',
						),
					);
					controller.close();
				},
			});
			return new Response(stream, { status: 200 });
		}) as typeof fetch;

		const outcome = await downloadUpdate(
			"https://127.0.0.1:7779",
			"token",
			{
				version: "1.2.3",
				sha512: "digest",
				releaseNotes: "x".repeat(80_000),
			},
			() => {},
		);

		expect(outcome.success).toBe(true);
		expect(body).not.toContain("releaseNotes");
		expect(JSON.parse(body)).toEqual({
			releaseInfo: { version: "1.2.3", sha512: "digest" },
		});
		expect(tls).toEqual({ rejectUnauthorized: false });
	});

	test("没有发布信息时字段留空", () => {
		expect(managerUpdateDownloadBody(null)).toEqual({
			releaseInfo: { version: undefined, sha512: undefined, sourceIdentity: undefined },
		});
	});
});
