import { afterAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";

const previousHome = process.env.NARRAFORK_HOME;
const home = mkdtempSync(resolve(tmpdir(), "narrafork-recording-storage-"));
process.env.NARRAFORK_HOME = home;
process.env.NARRAFORK_ALLOW_MULTIPLE = "1";
const { scanRequestRecordings } = await import("../../../server/services/storage-service");
const { REQUEST_RECORDING_DIR, REQUEST_RECORDING_SAVED_DIR } = await import(
	"../../../server/lib/request-recorder"
);

afterAll(() => {
	if (previousHome === undefined) delete process.env.NARRAFORK_HOME;
	else process.env.NARRAFORK_HOME = previousHome;
	rmSync(home, { recursive: true, force: true });
});

describe("separate rolling and saved recording storage categories", () => {
	test("missing directories report zero without creating anything", async () => {
		expect(await scanRequestRecordings()).toEqual({
			key: "requestRecordings",
			sizeBytes: 0,
			details: { retention: "rolling" },
		});
		expect(await scanRequestRecordings(true)).toEqual({
			key: "savedRequestRecordings",
			sizeBytes: 0,
			details: { retention: "manual" },
		});
	});
	test("budgets are accounted independently and fixed saves survive rolling removal", async () => {
		const rolling = resolve(home, REQUEST_RECORDING_DIR, "request-a");
		const saved = resolve(home, REQUEST_RECORDING_SAVED_DIR, "saved-a");
		mkdirSync(rolling, { recursive: true });
		mkdirSync(saved, { recursive: true });
		await Bun.write(resolve(rolling, "request.json"), new Uint8Array(17));
		await Bun.write(resolve(rolling, "response.ndjson"), new Uint8Array(29));
		await Bun.write(resolve(saved, "manifest.json"), new Uint8Array(31));
		await Bun.write(resolve(saved, "response.ndjson"), new Uint8Array(41));
		expect((await scanRequestRecordings()).sizeBytes).toBe(46);
		expect((await scanRequestRecordings(true)).sizeBytes).toBe(72);
		rmSync(resolve(home, REQUEST_RECORDING_DIR), { recursive: true, force: true });
		expect((await scanRequestRecordings()).sizeBytes).toBe(0);
		expect((await scanRequestRecordings(true)).sizeBytes).toBe(72);
	});
	test("saved measurement skips symlink loops and surfaces its depth cap", async () => {
		const root = resolve(home, REQUEST_RECORDING_SAVED_DIR);
		symlinkSync(root, resolve(root, "loop"), "dir");
		expect((await scanRequestRecordings(true)).sizeBytes).toBe(72);
		const deep = resolve(root, ...Array.from({ length: 70 }, (_, index) => `level-${index}`));
		mkdirSync(deep, { recursive: true });
		await Bun.write(resolve(deep, "response.ndjson"), new Uint8Array(999));
		const measured = await scanRequestRecordings(true);
		expect(measured.truncated).toBe(true);
		expect(measured.sizeBytes).toBeLessThan(999);
	});
});
