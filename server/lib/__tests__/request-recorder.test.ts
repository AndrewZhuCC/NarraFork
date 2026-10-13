import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import {
	appendFile,
	mkdir,
	mkdtemp,
	readdir,
	readFile,
	rm,
	symlink,
	writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { inflateRawSync } from "node:zlib";
import type { RecordingSettings, SavedRecording } from "../../../shared/request-recording";
import {
	RequestRecorderClient,
	recordingArchiveResponse,
	sanitizeRecordingMetadata,
	sanitizeRecordingUrl,
} from "../request-recorder";
import {
	RECORDING_ARCHIVE_BYTES,
	RECORDING_DOWNLOAD_LEASE_MS,
	RECORDING_MAX_DOWNLOADS,
	RECORDING_QUEUE_BYTES,
	REQUEST_RECORDING_DIR,
	REQUEST_RECORDING_SAVED_DIR,
} from "../request-recorder-protocol";
import { RequestRecorderStore } from "../request-recorder-store";
import { settings } from "../settings";

let home: string;
let store: RequestRecorderStore;
const config: RecordingSettings = {
	requestDumpRecordingEnabled: true,
	requestDumpRequestBytes: 1024 * 1024,
	requestDumpResponseBytes: 1024 * 1024,
	requestDumpEnabled: false,
	requestDumpErrorsOnly: false,
};
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
beforeEach(async () => {
	home = await mkdtemp(join(tmpdir(), "recording-test-"));
	store = new RequestRecorderStore(home);
	await store.execute({ type: "configure", settings: config });
});
afterEach(async () => {
	await rm(home, { recursive: true, force: true });
});
async function begin(id: string, narratorId = "n1", continuous = false, errorsOnly = false) {
	await store.execute({
		type: "begin",
		identity: { requestId: id, narratorId, provider: "fixture", model: "fixture-model" },
		continuous,
		errorsOnly,
	});
}
async function chunk(id: string, side: "request" | "response", text: string, packet = 1) {
	const data = new TextEncoder().encode(text);
	await store.execute({
		type: "chunk",
		requestId: id,
		side,
		kind: "body",
		data,
		packet,
		offset: 0,
		total: data.byteLength,
		time: "2026-10-07T00:00:00.000Z",
	});
}
async function save(narratorId = "n1") {
	return (await store.execute({ type: "save", narratorId })) as SavedRecording;
}
async function savedManifest(item: SavedRecording) {
	return JSON.parse(
		await readFile(
			join(home, REQUEST_RECORDING_SAVED_DIR, hash(item.narratorId), item.id, "manifest.json"),
			"utf8",
		),
	);
}
async function savedTexts(item: SavedRecording): Promise<string[]> {
	const manifest = await savedManifest(item);
	const texts: string[] = [];
	for (const record of manifest.requests) {
		for (const segment of record.segments) {
			const contents = await readFile(
				join(
					home,
					REQUEST_RECORDING_SAVED_DIR,
					hash(item.narratorId),
					item.id,
					"chunks",
					segment.file,
				),
				"utf8",
			);
			for (const line of contents.trim().split("\n"))
				texts.push(Buffer.from(JSON.parse(line).data, "base64").toString("utf8"));
		}
	}
	return texts;
}
// Minimal reader for small, bounded fixture ZIPs; not a production archive parser.
function unzipFixture(bytes: Buffer): Map<string, Buffer> {
	let end = bytes.length - 22;
	while (end >= 0 && bytes.readUInt32LE(end) !== 0x06054b50) end--;
	if (end < 0) throw new Error("Invalid ZIP fixture");
	let offset = bytes.readUInt32LE(end + 16);
	const result = new Map<string, Buffer>();
	while (bytes.readUInt32LE(offset) === 0x02014b50) {
		const method = bytes.readUInt16LE(offset + 10);
		const size = bytes.readUInt32LE(offset + 20);
		const nameLength = bytes.readUInt16LE(offset + 28);
		const extraLength = bytes.readUInt16LE(offset + 30);
		const commentLength = bytes.readUInt16LE(offset + 32);
		const local = bytes.readUInt32LE(offset + 42);
		const name = bytes.subarray(offset + 46, offset + 46 + nameLength).toString();
		const start = local + 30 + bytes.readUInt16LE(local + 26) + bytes.readUInt16LE(local + 28);
		const body = bytes.subarray(start, start + size);
		result.set(name, method === 8 ? inflateRawSync(body) : body);
		offset += 46 + nameLength + extraLength + commentLength;
	}
	return result;
}

describe("循环录制与固定保存", () => {
	test("进行中请求可保存，之后写入不会改变快照或硬链接内容", async () => {
		await begin("request-one");
		await store.execute({
			type: "meta",
			requestId: "request-one",
			side: "response",
			value: { status: 200, headers: { "content-type": "text/event-stream" } },
		});
		await chunk("request-one", "request", "request-body");
		await chunk("request-one", "response", "before-save");
		const snapshot = await save();
		expect(snapshot.partial).toBe(true);
		const before = await savedManifest(snapshot);
		await chunk("request-one", "response", "after-save", 2);
		await store.execute({ type: "end", requestId: "request-one" });
		expect(await savedManifest(snapshot)).toEqual(before);
		expect(await savedTexts(snapshot)).toEqual(["request-body", "before-save"]);
		expect((await save()).partial).toBe(false);
	});
	test("请求和返回独立淘汰，保留最新而非响应开头", async () => {
		await store.execute({
			type: "configure",
			settings: { ...config, requestDumpRequestBytes: 500, requestDumpResponseBytes: 900 },
		});
		await begin("old");
		await chunk("old", "request", "old-request".repeat(20));
		await chunk("old", "response", "old-response".repeat(30));
		await store.execute({ type: "end", requestId: "old" });
		await begin("new");
		await chunk("new", "request", "new-request".repeat(20));
		await chunk("new", "response", "latest-response".repeat(30));
		const snapshot = await save();
		const texts = await savedTexts(snapshot);
		expect(snapshot.truncated).toBe(true);
		expect(texts.join("")).toContain("new-request");
		expect(texts.join("")).not.toContain("old-request");
		expect(texts.join("")).toContain("latest-response");
		const manifest = await savedManifest(snapshot);
		for (const side of ["request", "response"]) {
			const sum = manifest.requests
				.flatMap((record: { segments: Array<{ side: string; bytes: number }> }) => record.segments)
				.filter((segment: { side: string }) => segment.side === side)
				.reduce((total: number, segment: { bytes: number }) => total + segment.bytes, 0);
			expect(sum).toBeLessThanOrEqual(side === "request" ? 500 : 900);
		}
	});
	test("超大单包只保留末尾并标明原始长度/偏移", async () => {
		await store.execute({
			type: "configure",
			settings: { ...config, requestDumpResponseBytes: 600 },
		});
		await begin("oversized");
		await chunk("oversized", "response", `${"a".repeat(5000)}TAIL`);
		const snapshot = await save();
		expect(snapshot.bytes).toBeLessThanOrEqual(600);
		expect(snapshot.truncated).toBe(true);
		expect((await savedTexts(snapshot)).join("")).toEndWith("TAIL");
	});
	test("字节准确保存 Unicode 和原始二进制", async () => {
		await begin("bytes");
		const data = new Uint8Array([0, 255, 0xc3, 0x28]);
		await store.execute({
			type: "chunk",
			requestId: "bytes",
			side: "response",
			kind: "binary-message",
			data,
			packet: 1,
			offset: 0,
			total: 4,
			time: "2026-10-07T00:00:00.000Z",
		});
		await chunk("bytes", "response", "中文😀", 2);
		const item = await save();
		const manifest = await savedManifest(item);
		const segment = manifest.requests[0].segments[0];
		const lines = (
			await readFile(
				join(home, REQUEST_RECORDING_SAVED_DIR, hash("n1"), item.id, "chunks", segment.file),
				"utf8",
			)
		)
			.trim()
			.split("\n")
			.map((line) => JSON.parse(line));
		expect(Buffer.from(lines[0].data, "base64")).toEqual(Buffer.from(data));
		expect(lines[0].type).toBe("binary-message");
		expect(lines[1].totalBytes).toBe(10);
		expect(Buffer.from(lines[1].data, "base64").toString()).toBe("中文😀");
	});
	test("固定内容在原循环数据被淘汰之后仍可归档下载", async () => {
		await begin("evidence");
		await chunk("evidence", "response", "original-evidence");
		await store.execute({ type: "end", requestId: "evidence" });
		const snapshot = await save();
		await store.execute({
			type: "configure",
			settings: { ...config, requestDumpRequestBytes: 0, requestDumpResponseBytes: 0 },
		});
		const job = await store.prepareArchive("n1", snapshot.id);
		const output = await store.buildArchive(job, new AbortController().signal);
		const zip = unzipFixture(await readFile(output.path));
		expect(JSON.parse(zip.get("manifest.json")?.toString() ?? "{}").summary.id).toBe(snapshot.id);
		const body = [...zip.entries()]
			.filter(([name]) => name.startsWith("chunks/"))
			.map(([, bytes]) => bytes.toString())
			.join("");
		expect(body).toContain(Buffer.from("original-evidence").toString("base64"));
		expect(zip.get("manifest.json")?.toString()).not.toContain(home);
	});
	test("跨叙述者与路径穿越不能下载或删除", async () => {
		await begin("one");
		await chunk("one", "response", "private");
		const snapshot = await save();
		await expect(store.prepareArchive("n2", snapshot.id)).rejects.toThrow("not found");
		await expect(
			store.execute({ type: "delete", narratorId: "n2", saveId: snapshot.id }),
		).rejects.toThrow("not found");
		await expect(store.prepareArchive("n1", "../../settings.json")).rejects.toThrow("not found");
	});
	test("拒绝被替换成符号链接的保存文件", async () => {
		await begin("link");
		await chunk("link", "response", "evidence");
		const item = await save();
		const manifest = await savedManifest(item);
		const path = join(
			home,
			REQUEST_RECORDING_SAVED_DIR,
			hash("n1"),
			item.id,
			"chunks",
			manifest.requests[0].segments[0].file,
		);
		await rm(path);
		await symlink(join(home, "secret"), path);
		await expect(store.prepareArchive("n1", item.id)).rejects.toThrow();
	});
	test("强制持续保留不受循环预算回收，结束后记录可列出", async () => {
		await store.execute({
			type: "configure",
			settings: { ...config, requestDumpRequestBytes: 0, requestDumpResponseBytes: 0 },
		});
		await begin("continuous", "n1", true);
		await chunk("continuous", "response", "always-kept");
		await store.execute({ type: "end", requestId: "continuous" });
		const listed = (await store.execute({ type: "list", narratorId: "n1" })) as {
			items: SavedRecording[];
		};
		expect(listed.items).toHaveLength(1);
		expect(listed.items[0].continuous).toBe(true);
		expect(listed.items[0].partial).toBe(false);
		expect((await savedTexts(listed.items[0])).join("")).toBe("always-kept");
	});
	test("错误筛选只控制自动保存，成功请求仍可手动固定", async () => {
		await begin("success", "n1", true, true);
		await chunk("success", "response", "success-data");
		await store.execute({ type: "end", requestId: "success" });
		expect(
			((await store.execute({ type: "list", narratorId: "n1" })) as { items: SavedRecording[] })
				.items,
		).toHaveLength(0);
		const manual = await save();
		expect((await savedTexts(manual)).join("")).toContain("success-data");
		await begin("failure", "n1", true, true);
		await chunk("failure", "response", "failure-data");
		await store.execute({ type: "end", requestId: "failure", error: "upstream rejected" });
		expect(
			((await store.execute({ type: "list", narratorId: "n1" })) as { items: SavedRecording[] })
				.items,
		).toHaveLength(2);
	});
	test("重启保留已落盘部分并注明 interrupted", async () => {
		await begin("crash");
		await chunk("crash", "response", "before-crash");
		await store.execute({ type: "bind", requestId: "crash", apiRequestId: "usage-id" });
		await store.flush();
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		const item = await save();
		expect(item.partial).toBe(true);
		expect(item.apiRequestIds).toEqual(["usage-id"]);
		expect((await savedManifest(item)).requests[0].status).toBe("interrupted");
		expect((await savedTexts(item)).join("")).toBe("before-crash");
	});
	test("游标分页与删除只作用于当前叙述者", async () => {
		await begin("record");
		await chunk("record", "response", "data");
		for (let i = 0; i < 3; i++) await save();
		await begin("other", "n2");
		await chunk("other", "response", "other-data");
		await save("n2");
		const first = (await store.execute({ type: "list", narratorId: "n1", limit: 2 })) as {
			items: SavedRecording[];
			nextCursor: string;
		};
		expect(first.items).toHaveLength(2);
		const second = (await store.execute({
			type: "list",
			narratorId: "n1",
			limit: 2,
			cursor: first.nextCursor,
		})) as { items: SavedRecording[]; nextCursor: null };
		expect(second.items).toHaveLength(1);
		expect(second.nextCursor).toBeNull();
		expect(new Set([...first.items, ...second.items].map((item) => item.id)).size).toBe(3);
		await store.execute({ type: "delete", narratorId: "n1", saveId: first.items[0].id });
		expect(
			((await store.execute({ type: "list", narratorId: "n1" })) as { items: SavedRecording[] })
				.items,
		).toHaveLength(2);
		expect(
			((await store.execute({ type: "list", narratorId: "n2" })) as { items: SavedRecording[] })
				.items,
		).toHaveLength(1);
	});
	test("磁盘安全阻止写入，空窗口不给出伪成功", async () => {
		await expect(save()).rejects.toThrow("No recorded");
		const blocked = new RequestRecorderStore(join(home, "blocked"), async () => false);
		await expect(
			blocked.execute({
				type: "begin",
				identity: { requestId: "blocked", narratorId: "n1", provider: "x", model: "x" },
				continuous: false,
				errorsOnly: false,
			}),
		).rejects.toThrow("disk safety");
		await expect(blocked.execute({ type: "save", narratorId: "n1" })).rejects.toThrow(
			"disk pressure",
		);
	});
	test("归档取消释放保留锁且不留下载产物", async () => {
		await begin("cancel");
		await chunk("cancel", "response", "data");
		const item = await save();
		const job = await store.prepareArchive("n1", item.id);
		const controller = new AbortController();
		controller.abort();
		await expect(store.buildArchive(job, controller.signal)).rejects.toThrow();
		await store.execute({ type: "delete", narratorId: "n1", saveId: item.id });
		expect(await readdir(join(home, REQUEST_RECORDING_DIR, ".exports"))).toEqual([]);
	});
});

describe("录制元数据脱敏与 worker 邮箱", () => {
	test("敏感 header、URL 凭证和宿主指针不进入包", () => {
		const result = sanitizeRecordingMetadata({
			url: "https://user:password@example.test/stream?token=secret&ok=yes",
			headers: {
				Authorization: "Bearer super-secret",
				Cookie: "sid=secret",
				"x-api-key": "secret",
				"content-type": "application/json",
			},
			filePath: "/private/home/settings.json",
			spill: { filePath: "/private" },
			bodyText: "not-metadata",
		});
		expect(JSON.stringify(result)).not.toContain("super-secret");
		expect(JSON.stringify(result)).not.toContain("password");
		expect(JSON.stringify(result)).not.toContain("/private");
		expect(JSON.stringify(result)).not.toContain("not-metadata");
		expect(result.headers).toEqual({
			Authorization: "[REDACTED]",
			Cookie: "[REDACTED]",
			"x-api-key": "[REDACTED]",
			"content-type": "application/json",
		});
		expect(sanitizeRecordingUrl("https://test.invalid/?key=value")).not.toContain("value");
	});
	test("超大元数据有硬上限", () => {
		const input = Object.fromEntries(
			Array.from({ length: 200 }, (_, i) => [`field-${i}`, "x".repeat(10000)]),
		);
		expect(Buffer.byteLength(JSON.stringify(sanitizeRecordingMetadata(input)))).toBeLessThanOrEqual(
			8192,
		);
	});
	test("真实 worker 在过载后仍可固定最新部分，且报告 gap", async () => {
		settings.diskSafety = { ...settings.diskSafety, mode: "off" } as NonNullable<
			typeof settings.diskSafety
		>;
		const client = new RequestRecorderClient(home);
		try {
			client.configure(config);
			await client.call({
				type: "begin",
				identity: { requestId: "mailbox", narratorId: "n1", provider: "fixture", model: "fixture" },
				continuous: false,
				errorsOnly: false,
			});
			for (let i = 0; i < RECORDING_QUEUE_BYTES / 65536 + 40; i++) {
				const data = new Uint8Array(65536).fill(i);
				client.capture({
					type: "chunk",
					requestId: "mailbox",
					side: "response",
					kind: "message",
					data,
					packet: i,
					offset: 0,
					total: data.byteLength,
					time: "2026-10-07T00:00:00.000Z",
				});
			}
			const snapshot = await client.call<SavedRecording>({ type: "save", narratorId: "n1" });
			expect(snapshot.partial).toBe(true);
			expect(snapshot.truncated).toBe(true);
			expect(snapshot.bytes).toBeLessThanOrEqual(config.requestDumpResponseBytes);
			expect((await savedManifest(snapshot)).requests[0].gapReasons).toContain("mailbox overload");
		} finally {
			await client.close();
		}
	}, 30_000);
});

describe("归档资源与恢复边界", () => {
	test("保存前拒绝无法通过唯一下载出口取回的超限窗口", async () => {
		await begin("oversized-window");
		await chunk("oversized-window", "response", "small fixture");
		// Alter only the byte index, without allocating a half-GiB fixture.
		const index = store as unknown as {
			records: Map<string, { segments: Array<{ bytes: number }> }>;
		};
		const record = index.records.get("oversized-window");
		if (!record) throw new Error("Missing fixture record");
		record.segments[0].bytes = RECORDING_ARCHIVE_BYTES;
		const error = await save().catch((failure) => failure);
		expect(error.code).toBe("RECORDING_ARCHIVE_LIMIT");
		expect(await Bun.file(join(home, REQUEST_RECORDING_SAVED_DIR)).exists()).toBe(false);
	});
	test("完成压缩但未消费的下载仍占配额，过期后释放", async () => {
		await begin("downloads");
		await chunk("downloads", "response", "body");
		const item = await save();
		let now = 1000;
		const clock = spyOn(performance, "now").mockImplementation(() => now);
		try {
			for (let i = 0; i < RECORDING_MAX_DOWNLOADS; i++) {
				const job = await store.prepareArchive("n1", item.id);
				await store.buildArchive(job, new AbortController().signal);
			}
			const busy = await store.prepareArchive("n1", item.id).catch((error) => error);
			expect(busy.code).toBe("RECORDING_BUSY");
			now += RECORDING_DOWNLOAD_LEASE_MS + 1;
			await store.flush();
			expect(await readdir(join(home, REQUEST_RECORDING_DIR, ".exports"))).toEqual([]);
			const again = await store.prepareArchive("n1", item.id);
			const controller = new AbortController();
			controller.abort();
			await store.buildArchive(again, controller.signal).catch(() => {});
		} finally {
			clock.mockRestore();
		}
	});
	test("HTTP body无人读取时租约到期也删除临时文件", async () => {
		const path = join(home, "download.zip");
		await writeFile(path, "zip-body");
		const released = mock(() => {});
		const response = await recordingArchiveResponse(
			{ path, bytes: 8 },
			"fixture",
			undefined,
			15,
			released,
		);
		const reader = response.body?.getReader();
		if (!reader) throw new Error("Missing response body");
		const error = await reader.closed.catch((failure) => failure);
		expect(error.code).toBe("RECORDING_TIMEOUT");
		expect(await Bun.file(path).exists()).toBe(false);
		expect(released).toHaveBeenCalledTimes(1);
		reader.releaseLock();
	});
	test("读取完成与客户端取消都即时清理文件", async () => {
		const first = join(home, "complete.zip");
		await writeFile(first, "zip");
		const completed = await recordingArchiveResponse({ path: first, bytes: 3 }, "fixture");
		expect(await completed.text()).toBe("zip");
		expect(await Bun.file(first).exists()).toBe(false);
		const second = join(home, "cancelled.zip");
		await writeFile(second, "zip");
		const controller = new AbortController();
		const cancelled = await recordingArchiveResponse(
			{ path: second, bytes: 3 },
			"fixture",
			controller.signal,
		);
		const reader = cancelled.body?.getReader();
		if (!reader) throw new Error("No body");
		controller.abort();
		const error = await reader.closed.catch((failure) => failure);
		expect(error.code).toBe("RECORDING_CANCELLED");
		expect(await Bun.file(second).exists()).toBe(false);
		reader.releaseLock();
	});
	test("启动回收崩溃遗留ZIP而不删除固定证据", async () => {
		await begin("restart-export");
		await chunk("restart-export", "response", "evidence");
		const item = await save();
		const job = await store.prepareArchive("n1", item.id);
		const output = await store.buildArchive(job, new AbortController().signal);
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		expect(await Bun.file(output.path).exists()).toBe(false);
		expect(await savedTexts(item)).toEqual(["evidence"]);
	});
	test("恢复拒绝导出目录symlink，不删除区外文件", async () => {
		const outside = join(home, "outside");
		await mkdir(outside);
		const protectedFile = join(outside, "12345678-1234-1234-1234-123456789abc.zip");
		await writeFile(protectedFile, "do not delete");
		await symlink(outside, join(home, REQUEST_RECORDING_DIR, ".exports"));
		const recovery = new RequestRecorderStore(home);
		const error = await recovery
			.execute({ type: "configure", settings: config })
			.catch((failure) => failure);
		expect(error instanceof Error ? error.message : String(error)).toContain(
			"Unsafe recording export",
		);
		expect(await Bun.file(protectedFile).text()).toBe("do not delete");
	});
});

test("空 WebSocket 消息仍保留包边界与顺序，不被当作没有消息", async () => {
	await begin("empty-packet");
	await chunk("empty-packet", "response", "", 1);
	await chunk("empty-packet", "response", "second", 2);
	const item = await save();
	expect(await savedTexts(item)).toEqual(["", "second"]);
});

describe("review 崩溃恢复回归", () => {
	const ringChunks = (id: string) => join(home, REQUEST_RECORDING_DIR, hash(id), "chunks");
	const fixedParent = () => join(home, REQUEST_RECORDING_SAVED_DIR, hash("n1"));
	test("checkpoint后新建的完整段被恢复计账且保留原快照", async () => {
		await begin("checkpoint-tail");
		await chunk("checkpoint-tail", "response", "first", 1);
		await store.flush();
		const original = await save();
		await chunk("checkpoint-tail", "response", "second", 2);
		expect((await readdir(ringChunks("checkpoint-tail"))).length).toBe(2);
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		const recovered = await save();
		expect(await savedTexts(recovered)).toEqual(["first", "second"]);
		expect(await savedTexts(original)).toEqual(["first"]);
		expect(recovered.partial).toBe(true);
	});
	test("重启后预算0会同时淘汰checkpoint和未checkpoint段", async () => {
		await begin("zero-after-restart");
		await chunk("zero-after-restart", "response", "first", 1);
		await store.flush();
		const original = await save();
		await chunk("zero-after-restart", "response", "second", 2);
		store = new RequestRecorderStore(home);
		await store.execute({
			type: "configure",
			settings: { ...config, requestDumpResponseBytes: 0 },
		});
		expect(await readdir(ringChunks("zero-after-restart"))).toEqual([]);
		expect((await save()).bytes).toBe(0);
		expect(await savedTexts(original)).toEqual(["first"]);
	});
	test("新段撕裂尾部仅丢弃不完整帧，合法前缀仍可下载", async () => {
		await begin("torn-tail");
		await chunk("torn-tail", "response", "first", 1);
		await store.flush();
		await save();
		await chunk("torn-tail", "response", "second", 2);
		const checkpoint = JSON.parse(
			await readFile(join(home, REQUEST_RECORDING_DIR, hash("torn-tail"), "record.json"), "utf8"),
		);
		const known = new Set(checkpoint.segments.map((segment: { file: string }) => segment.file));
		const orphan = (await readdir(ringChunks("torn-tail"))).find((name) => !known.has(name));
		if (!orphan) throw new Error("No uncheckpointed segment");
		await appendFile(join(ringChunks("torn-tail"), orphan), '{"requestId":"torn-tail"');
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		const recovered = await save();
		expect(await savedTexts(recovered)).toEqual(["first", "second"]);
		expect((await savedManifest(recovered)).requests[0].gapReasons.join(";")).toContain(
			"incomplete segment tail",
		);
	});
	test("开始即持久化归属和发布状态，首checkpoint前连续录制崩溃仍可管理", async () => {
		await store.execute({ type: "configure", settings: { ...config, requestDumpEnabled: true } });
		await begin("early-continuous", "n1", true);
		await chunk("early-continuous", "response", "before-checkpoint");
		const ids = await readdir(fixedParent());
		expect(ids).toHaveLength(1);
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		const listed = (await store.execute({ type: "list", narratorId: "n1" })) as {
			items: SavedRecording[];
		};
		expect(listed.items).toHaveLength(1);
		expect(listed.items[0].id).toBe(ids[0]);
		expect(listed.items[0].partial).toBe(true);
		expect(await savedTexts(listed.items[0])).toEqual(["before-checkpoint"]);
		await store.execute({ type: "delete", narratorId: "n1", saveId: ids[0] });
		expect(await readdir(fixedParent())).toEqual([]);
	});
	test("旧版无receipt固定目录和未rename pending启动回收，但已发布证据不删", async () => {
		await begin("published-evidence");
		await chunk("published-evidence", "response", "keep");
		const published = await save();
		const orphan = join(fixedParent(), "legacyOrphan".padEnd(21, "0"));
		const pending = join(fixedParent(), `.pending-${"pendingSave".padEnd(21, "0")}`);
		await mkdir(join(orphan, "chunks"), { recursive: true });
		await writeFile(join(orphan, "chunks", "orphan.jsonl"), "old data");
		await mkdir(join(pending, "chunks"), { recursive: true });
		await writeFile(
			join(pending, "manifest.json"),
			await readFile(join(fixedParent(), published.id, "manifest.json")),
		);
		await writeFile(
			join(pending, "summary.json"),
			await readFile(join(fixedParent(), published.id, "summary.json")),
		);
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		expect(await readdir(fixedParent())).toEqual([published.id]);
		expect(await savedTexts(published)).toEqual(["keep"]);
	});
	test("manifest先发布后summary丢失会修复列表，不删除证据", async () => {
		await begin("missing-summary");
		await chunk("missing-summary", "response", "keep");
		const published = await save();
		await rm(join(fixedParent(), published.id, "summary.json"));
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		const result = (await store.execute({ type: "list", narratorId: "n1" })) as {
			items: SavedRecording[];
		};
		expect(result.items.map((item) => item.id)).toEqual([published.id]);
		expect(await savedTexts(published)).toEqual(["keep"]);
	});
	test("大量pending分批回收，后台checkpoint能继续完成而不删除已发布目录", async () => {
		await begin("many-pending");
		await chunk("many-pending", "response", "keep");
		const published = await save();
		for (let i = 0; i < 270; i++)
			await mkdir(join(fixedParent(), `.pending-${String(i).padStart(21, "0")}`));
		store = new RequestRecorderStore(home);
		await store.execute({ type: "configure", settings: config });
		await store.flush();
		await store.flush();
		expect(await readdir(fixedParent())).toEqual([published.id]);
		expect(await savedTexts(published)).toEqual(["keep"]);
	});
});

test("continuous receipt存在但chunks未创建的崩溃窗口仍恢复为可下载可删除的部分保存", async () => {
	await store.execute({ type: "configure", settings: { ...config, requestDumpEnabled: true } });
	const target = store as unknown as { chunksDir(record: unknown): string };
	const crash = spyOn(target, "chunksDir").mockImplementation(() => {
		throw new Error("simulated crash before chunks creation");
	});
	try {
		const error = await begin("before-chunks", "n1", true).catch((failure) => failure);
		expect(error.message).toContain("before chunks creation");
	} finally {
		crash.mockRestore();
	}
	store = new RequestRecorderStore(home);
	await store.execute({ type: "configure", settings: config });
	const result = (await store.execute({ type: "list", narratorId: "n1" })) as {
		items: SavedRecording[];
	};
	expect(result.items).toHaveLength(1);
	expect(result.items[0].partial).toBe(true);
	const job = await store.prepareArchive("n1", result.items[0].id);
	const archive = await store.buildArchive(job, new AbortController().signal);
	expect(await Bun.file(archive.path).exists()).toBe(true);
	await store.execute({ type: "delete", narratorId: "n1", saveId: result.items[0].id });
	expect(await readdir(join(home, REQUEST_RECORDING_SAVED_DIR, hash("n1")))).toEqual([]);
});
