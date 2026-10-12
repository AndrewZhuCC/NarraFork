import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import type { RequestRecordingDetail, SavedRecording } from "@shared/request-recording";
import { api } from "../../lib/api";
import { downloadErrorRequestDump, errorRequestDownloadState } from "./useErrorRequestDump";

const saved: SavedRecording = {
	id: "saved-id",
	narratorId: "n",
	createdAt: "2026-10-08T00:00:00Z",
	requestIds: ["req_failed"],
	apiRequestIds: ["api-id"],
	requestCount: 1,
	bytes: 20,
	partial: false,
	truncated: false,
};
const detail: RequestRecordingDetail = {
	requestId: "req_failed",
	narratorId: "n",
	provider: "openai",
	model: "test",
	startedAt: saved.createdAt,
	partial: false,
	truncated: false,
	requestPreview: "request",
	responsePreview: "error",
	previewTruncated: { request: false, response: false },
};
let get: ReturnType<typeof spyOn<typeof api, "getRequestRecordingDetail">>;
let save: ReturnType<typeof spyOn<typeof api, "saveSpecificRequestRecording">>;
let download: ReturnType<typeof spyOn<typeof api, "downloadRequestRecording">>;
beforeEach(() => {
	get = spyOn(api, "getRequestRecordingDetail").mockResolvedValue(detail);
	save = spyOn(api, "saveSpecificRequestRecording").mockResolvedValue(saved);
	download = spyOn(api, "downloadRequestRecording").mockResolvedValue({
		blob: new Blob(["zip"]),
		fileName: "dump.zip",
	});
});
afterEach(() => {
	for (const spy of [get, save, download]) spy.mockRestore();
});

describe("exact error request download", () => {
	test("one click snapshots and downloads only the failed attempt", async () => {
		const signal = new AbortController().signal;
		expect((await downloadErrorRequestDump("n", "req_failed", signal)).fileName).toBe("dump.zip");
		expect(get).toHaveBeenCalledWith("n", "req_failed", signal);
		expect(save).toHaveBeenCalledWith("n", "req_failed", signal);
		expect(download).toHaveBeenCalledWith("n", "saved-id", signal);
	});
	test("existing single-request evidence needs no write access", async () => {
		get.mockResolvedValue({ ...detail, savedRecording: saved });
		await downloadErrorRequestDump("n", "req_failed", new AbortController().signal);
		expect(save).not.toHaveBeenCalled();
		expect(download).toHaveBeenCalledTimes(1);
	});
	test("does not reuse a bundle containing other attempts", async () => {
		get.mockResolvedValue({
			...detail,
			savedRecording: { ...saved, requestIds: ["req_failed", "req_other"], requestCount: 2 },
		});
		await downloadErrorRequestDump("n", "req_failed", new AbortController().signal);
		expect(save).toHaveBeenCalledTimes(1);
	});
	test("unavailable evidence never falls back to the current narrator window", async () => {
		get.mockRejectedValue(new Error("Recording unavailable"));
		await expect(
			downloadErrorRequestDump("n", "req_failed", new AbortController().signal),
		).rejects.toThrow("Recording unavailable");
		expect(save).not.toHaveBeenCalled();
		expect(download).not.toHaveBeenCalled();
	});
	test("checks saved evidence identity before fetching any ZIP", async () => {
		save.mockResolvedValue({ ...saved, requestIds: ["req_different"] });
		await expect(
			downloadErrorRequestDump("n", "req_failed", new AbortController().signal),
		).rejects.toThrow("identity mismatch");
		expect(download).not.toHaveBeenCalled();
	});
	test("closing or switching sessions while details load prevents a late save", async () => {
		const controller = new AbortController();
		get.mockImplementation(async () => {
			controller.abort();
			return detail;
		});
		await expect(downloadErrorRequestDump("n", "req_failed", controller.signal)).rejects.toThrow();
		expect(save).not.toHaveBeenCalled();
		expect(download).not.toHaveBeenCalled();
	});
	test("cancellation after saving prevents ZIP download", async () => {
		const controller = new AbortController();
		save.mockImplementation(async () => {
			controller.abort();
			return saved;
		});
		await expect(downloadErrorRequestDump("n", "req_failed", controller.signal)).rejects.toThrow();
		expect(download).not.toHaveBeenCalled();
	});
	test("a cancelled or timed-out transfer cannot yield a late browser download", async () => {
		const controller = new AbortController();
		download.mockImplementation(async () => {
			controller.abort();
			return { blob: new Blob(["late ZIP"]), fileName: "late.zip" };
		});
		await expect(downloadErrorRequestDump("n", "req_failed", controller.signal)).rejects.toThrow();
	});
});

test("download controls distinguish this request from a transfer on another error card", () => {
	expect(errorRequestDownloadState("req_A", null)).toEqual({
		downloadingDump: false,
		downloadDumpDisabled: false,
	});
	expect(errorRequestDownloadState("req_A", "req_A")).toEqual({
		downloadingDump: true,
		downloadDumpDisabled: false,
	});
	expect(errorRequestDownloadState("req_B", "req_A")).toEqual({
		downloadingDump: false,
		downloadDumpDisabled: true,
	});
	expect(errorRequestDownloadState("req_B", null)).toEqual({
		downloadingDump: false,
		downloadDumpDisabled: false,
	});
});
