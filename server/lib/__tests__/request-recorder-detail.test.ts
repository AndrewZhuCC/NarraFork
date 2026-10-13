import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import { link, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type {
	RecordingSettings,
	RequestRecordingDetail,
	SavedRecording,
} from "../../../shared/request-recording";
import { AppError } from "../errors";
import { RequestRecorderClient } from "../request-recorder";
import { REQUEST_RECORDING_SAVED_DIR } from "../request-recorder-protocol";
import { RequestRecorderStore } from "../request-recorder-store";
import { settings } from "../settings";

let home: string;
let client: RequestRecorderClient;
let previousDiskSafety: typeof settings.diskSafety;
const config: RecordingSettings = {
	requestDumpRecordingEnabled: true,
	requestDumpRequestBytes: 1048576,
	requestDumpResponseBytes: 1048576,
	requestDumpEnabled: false,
	requestDumpErrorsOnly: false,
};
beforeEach(async () => {
	home = await mkdtemp(join(tmpdir(), "recording-detail-"));
	previousDiskSafety = settings.diskSafety;
	settings.diskSafety = { ...settings.diskSafety, mode: "off" } as NonNullable<
		typeof settings.diskSafety
	>;
	client = new RequestRecorderClient(home);
	client.configure(config);
});
afterEach(async () => {
	await client.close();
	settings.diskSafety = previousDiskSafety;
	await rm(home, { recursive: true, force: true });
});
async function begin(requestId: string, narratorId = "n1", continuous = false) {
	await client.call({
		type: "begin",
		identity: { requestId, narratorId, provider: "fixture", model: "fixture" },
		continuous,
		errorsOnly: false,
	});
}
async function chunk(requestId: string, side: "request" | "response", text: string, packet = 1) {
	const data = Buffer.from(text);
	await client.call({
		type: "chunk",
		requestId,
		side,
		kind: "body",
		data,
		packet,
		offset: 0,
		total: data.length,
		time: new Date().toISOString(),
	});
}
async function end(requestId: string, error?: string) {
	await client.call({ type: "end", requestId, error });
}
async function detail(requestId: string, narratorId = "n1", signal?: AbortSignal) {
	return client.call<RequestRecordingDetail>({ type: "detail", narratorId, requestId }, signal);
}
async function saveSpecific(requestId: string, narratorId = "n1", signal?: AbortSignal) {
	return client.call<SavedRecording>({ type: "save-specific", narratorId, requestId }, signal);
}
async function evict() {
	await client.call({
		type: "configure",
		settings: {
			...config,
			requestDumpRecordingEnabled: false,
			requestDumpRequestBytes: 0,
			requestDumpResponseBytes: 0,
		},
	});
}
async function manifest(saved: SavedRecording) {
	const hash = createHash("sha256").update(saved.narratorId).digest("hex");
	return JSON.parse(
		await readFile(
			join(home, REQUEST_RECORDING_SAVED_DIR, hash, saved.id, "manifest.json"),
			"utf8",
		),
	);
}

describe("真实 worker 精确请求详情与定向保存", () => {
	test("精确查找而非最新请求；GET 不创建保存且保持未完成标记", async () => {
		await begin("first");
		await chunk("first", "request", "first request");
		await chunk("first", "response", "first response");
		await client.call({ type: "bind", requestId: "first", apiRequestId: "api-first" });
		await begin("latest");
		await chunk("latest", "response", "latest response");
		const first = await detail("first");
		expect(first.requestId).toBe("first");
		expect(first.apiRequestId).toBe("api-first");
		expect(first.requestPreview).toBe("first request");
		expect(first.responsePreview).toBe("first response");
		expect(first.partial).toBe(true);
		expect(first.savedRecording).toBeUndefined();
		expect(
			await client.call<{ items: SavedRecording[]; nextCursor: string | null }>({
				type: "list",
				narratorId: "n1",
			}),
		).toEqual({
			items: [],
			nextCursor: null,
		});
		await chunk("first", "response", " after preview", 2);
		expect((await detail("first")).responsePreview).toBe("first response after preview");
	});
	test("预览每侧限制 64KiB；保留 Unicode；元数据凭据和绝对路径被脱敏", async () => {
		await begin("bounded");
		await client.call({
			type: "meta",
			requestId: "bounded",
			side: "request",
			value: {
				url: "https://example.test/path?token=hidden-token",
				headers: { authorization: "hidden-auth" },
				filePath: "/home/private-owner/private.txt",
				error: "failed /home/private-owner/private.txt",
			},
		});
		for (let i = 0; i < 3; i++) {
			await chunk("bounded", "request", "中".repeat(20000), i);
			await chunk("bounded", "response", "😀".repeat(15000), i);
		}
		await end(
			"bounded",
			`failed ${home}/private.dump and C:\\Users\\private-owner\\file ${"中".repeat(3000)}`,
		);
		const result = await detail("bounded");
		expect(Buffer.byteLength(result.requestPreview)).toBeLessThanOrEqual(65536);
		expect(Buffer.byteLength(result.responsePreview)).toBeLessThanOrEqual(65536);
		expect(result.previewTruncated).toEqual({ request: true, response: true });
		expect(result.truncated).toBe(false);
		expect(result.partial).toBe(false);
		expect(result.requestPreview).toContain("REDACTED");
		expect(result.requestPreview).not.toContain("hidden-token");
		expect(result.requestPreview).not.toContain("hidden-auth");
		expect(result.requestPreview).not.toContain("private-owner");
		expect(result.error).not.toContain(home);
		expect(result.error).not.toContain("private-owner");
		expect(Buffer.byteLength(result.error ?? "")).toBeLessThanOrEqual(2048);
	});
	test("定向保存只包含目标；保存中的进行中响应是不可变快照", async () => {
		await begin("target");
		await chunk("target", "response", "before-save");
		await begin("other");
		await chunk("other", "response", "other-secret");
		const saved = await saveSpecific("target");
		expect(saved.requestIds).toEqual(["target"]);
		expect(saved.requestCount).toBe(1);
		expect(saved.partial).toBe(true);
		await chunk("target", "response", "after-save", 2);
		await end("target");
		await end("other");
		await evict();
		const result = await detail("target");
		expect(result.responsePreview).toBe("before-save");
		expect(result.partial).toBe(true);
		expect(result.savedRecording?.id).toBe(saved.id);
		expect(
			(await manifest(saved)).requests.map(
				(r: { identity: { requestId: string } }) => r.identity.requestId,
			),
		).toEqual(["target"]);
	});
	test("持续固定请求离开 ring 及重启后仍能精确取回，不能用最近保存替代", async () => {
		await begin("continuous-first", "n1", true);
		await chunk("continuous-first", "response", "continuous evidence");
		await end("continuous-first");
		await begin("continuous-latest", "n1", true);
		await chunk("continuous-latest", "response", "wrong evidence");
		await end("continuous-latest");
		await client.call({ type: "flush" });
		await client.close();
		client = new RequestRecorderClient(home);
		client.configure(config);
		const result = await detail("continuous-first");
		expect(result.responsePreview).toBe("continuous evidence");
		expect(result.partial).toBe(false);
		expect(result.savedRecording?.continuous).toBe(true);
		expect((await saveSpecific("continuous-first")).requestIds).toEqual(["continuous-first"]);
		expect((await detail("continuous-latest")).responsePreview).toBe("wrong evidence");
	});
	test("完成缓存离开ring后跳过超32MiB历史扫描；归属隔离和删除后回退仍有效", async () => {
		await begin("cached-continuous", "n1", true);
		await chunk("cached-continuous", "response", "exact cached evidence");
		await end("cached-continuous");
		const saved = await saveSpecific("cached-continuous");
		await evict();
		const narratorDir = join(
			home,
			REQUEST_RECORDING_SAVED_DIR,
			createHash("sha256").update("n1").digest("hex"),
		);
		// Only one 512KiB physical file plus 65 hardlinks: deterministic scan-budget
		// coverage without allocating a large historical archive or unbounded data.
		const source = join(home, "oversized-history-summary.json");
		await writeFile(source, JSON.stringify({ requestIds: [] }).padEnd(512 * 1024, " "));
		for (let i = 0; i < 65; i++) {
			const directory = join(narratorDir, `history${String(i).padStart(14, "0")}`);
			await mkdir(directory);
			await link(source, join(directory, "summary.json"));
		}
		// Prove that the real worker's ordinary scan actually exceeds its byte budget.
		expect(await detail("uncached").catch((error) => error)).toMatchObject({
			statusCode: 429,
			code: "RECORDING_BUSY",
		});
		const cached = await detail("cached-continuous");
		expect(cached.responsePreview).toBe("exact cached evidence");
		expect(cached.partial).toBe(false);
		expect(cached.savedRecording?.id).toBe(saved.id);
		expect((await saveSpecific("cached-continuous")).id).toBe(saved.id);
		for (const lookup of [detail, saveSpecific]) {
			expect(await lookup("cached-continuous", "n2").catch((error) => error)).toMatchObject({
				statusCode: 404,
			});
		}
		await client.call({ type: "delete", narratorId: "n1", saveId: saved.id });
		// A cached id is never enough: a deleted manifest must resume the bounded scan.
		for (const lookup of [detail, saveSpecific]) {
			expect(await lookup("cached-continuous").catch((error) => error)).toMatchObject({
				statusCode: 429,
				code: "RECORDING_BUSY",
			});
		}
	});

	test("删除离开ring的完成缓存后回退到不可变partial快照，不伪称完成", async () => {
		await begin("cached-fallback", "n1", true);
		await chunk("cached-fallback", "response", "frozen-before");
		const partial = await saveSpecific("cached-fallback");
		await chunk("cached-fallback", "response", "-after", 2);
		await end("cached-fallback");
		const completed = await saveSpecific("cached-fallback");
		expect(completed.continuous).toBe(true);
		await evict();
		expect((await detail("cached-fallback")).responsePreview).toBe("frozen-before-after");
		await client.call({ type: "delete", narratorId: "n1", saveId: completed.id });
		const fallback = await detail("cached-fallback");
		expect(fallback.savedRecording?.id).toBe(partial.id);
		expect(fallback.responsePreview).toBe("frozen-before");
		expect(fallback.partial).toBe(true);
		const resaved = await saveSpecific("cached-fallback");
		expect(resaved.requestIds).toEqual(["cached-fallback"]);
		expect(resaved.partial).toBe(true);
		expect((await manifest(resaved)).requests[0].endedAt).toBeUndefined();
	});

	test("完成请求仍在ring时GET及重复POST复用精确单请求固定，重启后恢复索引，删除后不复用失效缓存", async () => {
		await begin("completed-target");
		await chunk("completed-target", "response", "completed evidence");
		await end("completed-target");
		const saved = await saveSpecific("completed-target");
		expect(saved.partial).toBe(false);
		await client.call({
			type: "bind",
			requestId: "completed-target",
			apiRequestId: "api-late-bound",
		});
		expect((await detail("completed-target")).apiRequestId).toBe("api-late-bound");
		await begin("completed-other");
		await chunk("completed-other", "response", "different evidence");
		await end("completed-other");
		const other = await saveSpecific("completed-other");
		expect((await detail("completed-target")).savedRecording?.id).toBe(saved.id);
		expect((await saveSpecific("completed-target")).id).toBe(saved.id);
		expect((await detail("completed-target")).responsePreview).toBe("completed evidence");
		expect((await saveSpecific("completed-other")).id).toBe(other.id);
		await client.call({ type: "flush" });
		await client.close();
		client = new RequestRecorderClient(home);
		client.configure(config);
		expect((await detail("completed-target")).savedRecording?.id).toBe(saved.id);
		expect((await saveSpecific("completed-target")).id).toBe(saved.id);
		const listed = await client.call<{ items: SavedRecording[] }>({
			type: "list",
			narratorId: "n1",
		});
		expect(listed.items).toHaveLength(2);
		await client.call({ type: "delete", narratorId: "n1", saveId: saved.id });
		expect((await detail("completed-target")).savedRecording).toBeUndefined();
		const replacement = await saveSpecific("completed-target");
		expect(replacement.id).not.toBe(saved.id);
		expect((await detail("completed-target")).savedRecording?.id).toBe(replacement.id);
	});
	test("旧partial快照不会替换仍在ring的实时详情，也不阻止结束后固定完整请求", async () => {
		await begin("live-target");
		await chunk("live-target", "response", "before");
		const partial = await saveSpecific("live-target");
		await chunk("live-target", "response", "after", 2);
		const live = await detail("live-target");
		expect(live.responsePreview).toBe("beforeafter");
		expect(live.partial).toBe(true);
		expect(live.savedRecording).toBeUndefined();
		await end("live-target");
		const ended = await detail("live-target");
		expect(ended.responsePreview).toBe("beforeafter");
		expect(ended.partial).toBe(false);
		expect(ended.savedRecording).toBeUndefined();
		const complete = await saveSpecific("live-target");
		expect(complete.id).not.toBe(partial.id);
		expect(complete.partial).toBe(false);
		expect((await detail("live-target")).savedRecording?.id).toBe(complete.id);
		expect((await saveSpecific("live-target")).id).toBe(complete.id);
	});
	test("多请求固定包需按请求筛选预览与另存，不能返回整个包", async () => {
		for (const id of ["target", "other"]) {
			await begin(id);
			await chunk(id, "response", `${id} evidence`);
			await end(id);
		}
		const bundle = await client.call<SavedRecording>({ type: "save", narratorId: "n1" });
		expect(bundle.requestCount).toBe(2);
		await evict();
		expect((await detail("target")).responsePreview).toBe("target evidence");
		const selected = await saveSpecific("target");
		expect(selected.id).not.toBe(bundle.id);
		expect(selected.requestIds).toEqual(["target"]);
		expect((await manifest(selected)).requests).toHaveLength(1);
		await client.call({ type: "delete", narratorId: "n1", saveId: bundle.id });
		expect((await detail("target")).responsePreview).toBe("target evidence");
		expect(await detail("other").catch((error) => error)).toMatchObject({ statusCode: 404 });
	});
	test("不存在、全量淘汰和跨叙述者归属 GET/POST 都是404", async () => {
		await begin("owned", "n2");
		await chunk("owned", "response", "private");
		await end("owned");
		for (const requestId of ["missing", "owned"]) {
			expect(await detail(requestId).catch((error) => error)).toMatchObject({ statusCode: 404 });
			expect(await saveSpecific(requestId).catch((error) => error)).toMatchObject({
				statusCode: 404,
			});
		}
		await begin("evicted");
		await chunk("evicted", "response", "gone");
		await end("evicted");
		await evict();
		expect(await detail("evicted").catch((error) => error)).toMatchObject({ statusCode: 404 });
		expect(await saveSpecific("evicted").catch((error) => error)).toMatchObject({
			statusCode: 404,
		});
	});
	test("持续固定保存也逐请求验归属，部分流完成状态不会被误称完整", async () => {
		await begin("saved-private", "n2", true);
		await chunk("saved-private", "response", "private saved evidence");
		await client.call({
			type: "meta",
			requestId: "saved-private",
			side: "response",
			value: { bodyIncomplete: true },
		});
		await end("saved-private");
		const own = await detail("saved-private", "n2");
		expect(own.status).toBe("complete");
		expect(own.partial).toBe(true);
		expect(own.responsePreview).toContain("private saved evidence");
		expect(await detail("saved-private").catch((error) => error)).toMatchObject({
			statusCode: 404,
		});
		expect(await saveSpecific("saved-private").catch((error) => error)).toMatchObject({
			statusCode: 404,
		});
	});
	test("持续录制进行中的定向保存不跟随未来写入；取消不发布固定证据", async () => {
		await begin("active-continuous", "n1", true);
		await chunk("active-continuous", "response", "before");
		const snapshot = await saveSpecific("active-continuous");
		expect(snapshot.continuous).toBeUndefined();
		await chunk("active-continuous", "response", "after", 2);
		await end("active-continuous");
		const frozen = await manifest(snapshot);
		expect(frozen.requests[0].segments).toHaveLength(1);
		expect(frozen.summary.partial).toBe(true);
		const controller = new AbortController();
		controller.abort();
		await expect(saveSpecific("active-continuous", "n1", controller.signal)).rejects.toThrow();
		await expect(detail("active-continuous", "n1", controller.signal)).rejects.toThrow();
	});
});

test("已结束但部分/截断的证据也可只读复用，不重复创建保存", async () => {
	await client.call({ type: "configure", settings: { ...config, requestDumpResponseBytes: 2048 } });
	await begin("ended-partial");
	await chunk("ended-partial", "request", "request body");
	await client.call({
		type: "meta",
		requestId: "ended-partial",
		side: "request",
		value: { bodyIncomplete: true },
	});
	await chunk("ended-partial", "response", "x".repeat(8192));
	await end("ended-partial", "upstream failed");
	const saved = await saveSpecific("ended-partial");
	expect(saved.partial).toBe(true);
	expect(saved.truncated).toBe(true);
	expect((await detail("ended-partial")).savedRecording?.id).toBe(saved.id);
	expect((await saveSpecific("ended-partial")).id).toBe(saved.id);
	await client.close();
	client = new RequestRecorderClient(home);
	client.configure(config);
	expect((await detail("ended-partial")).savedRecording?.id).toBe(saved.id);
	expect((await saveSpecific("ended-partial")).id).toBe(saved.id);
});

test("可选固定历史查找超预算时仍可查看和定向保存ring证据", async () => {
	const store = new RequestRecorderStore(join(home, "lookup-fallback"));
	await store.execute({ type: "configure", settings: config });
	await store.execute({
		type: "begin",
		identity: {
			requestId: "ring-available",
			narratorId: "n1",
			provider: "fixture",
			model: "fixture",
		},
		continuous: false,
		errorsOnly: false,
	});
	const data = Buffer.from("ring evidence");
	await store.execute({
		type: "chunk",
		requestId: "ring-available",
		side: "response",
		kind: "body",
		data,
		packet: 1,
		offset: 0,
		total: data.length,
		time: new Date().toISOString(),
	});
	await store.execute({ type: "end", requestId: "ring-available", error: "failed" });
	const lookup = spyOn(
		store as unknown as { findSavedRequest: () => Promise<undefined> },
		"findSavedRequest",
	).mockRejectedValue(
		new AppError("Recording lookup exceeds safety budget", 429, "RECORDING_BUSY"),
	);
	try {
		expect((await store.detail("n1", "ring-available")).responsePreview).toBe("ring evidence");
		const saved = (await store.execute({
			type: "save-specific",
			narratorId: "n1",
			requestId: "ring-available",
		})) as SavedRecording;
		expect(saved.requestIds).toEqual(["ring-available"]);
		// Without ring evidence the lookup limit is authoritative, never a false success.
		await expect(store.detail("n1", "not-in-ring")).rejects.toThrow("safety budget");
	} finally {
		lookup.mockRestore();
	}
});
