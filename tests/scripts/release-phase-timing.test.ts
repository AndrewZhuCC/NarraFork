import { afterEach, expect, spyOn, test } from "bun:test";
import { type ReleasePhaseDetails, timeReleasePhase } from "../../scripts/lib/ci-release-io";

const restores: Array<() => void> = [];
afterEach(() => {
	for (const restore of restores.splice(0).reverse()) restore();
});
function capture() {
	const lines: string[] = [];
	const spy = spyOn(console, "info").mockImplementation((line: unknown) => {
		lines.push(String(line));
	});
	restores.push(() => spy.mockRestore());
	return { lines, records: () => lines.map((line) => JSON.parse(line)) };
}

test("phase boundaries retain result identity and end output byte counts", async () => {
	const logs = capture();
	const details = { platform: "linux-x64", version: "0.9.2", inputBytes: 100, outputBytes: 0 };
	const value = { verified: true };
	expect(
		await timeReleasePhase("patch.generate", details, () => {
			details.outputBytes = 23;
			return value;
		}),
	).toBe(value);
	const [start, end] = logs.records();
	expect(start).toMatchObject({ event: "release.phase", phase: "patch.generate", state: "start" });
	expect(end).toMatchObject({ id: start.id, state: "end", status: "success", outputBytes: 23 });
	expect(end.durationMs).toBeGreaterThanOrEqual(0);
	expect(end.controllerRssBytes).toBeGreaterThan(0);
});

test("nested concurrent phases keep their own parent identities", async () => {
	const logs = capture();
	await timeReleasePhase("assemble", {}, () =>
		Promise.all([
			timeReleasePhase("platform.first", {}, async () => {
				await Promise.resolve();
				return timeReleasePhase("platform.first.hash", {}, () => 1);
			}),
			timeReleasePhase("platform.second", {}, async () => {
				await Promise.resolve();
				return timeReleasePhase("platform.second.hash", {}, () => 2);
			}),
		]),
	);
	const records = logs.records();
	for (const prefix of ["first", "second"]) {
		const parent = records.find((r) => r.phase === `platform.${prefix}` && r.state === "start");
		const child = records.find((r) => r.phase === `platform.${prefix}.hash` && r.state === "start");
		expect(child.parentId).toBe(parent.id);
	}
	expect(records.filter((r) => r.state === "start")).toHaveLength(5);
	expect(records.filter((r) => r.state === "end")).toHaveLength(5);
});

test("error, cancellation and timeout retain the exact thrown object without error messages", async () => {
	const logs = capture();
	for (const name of ["Error", "AbortError", "TimeoutError"]) {
		const error = new Error("secret signed URL and credential");
		error.name = name;
		const controller = new AbortController();
		if (name === "AbortError") controller.abort(error);
		let actual: unknown;
		try {
			await timeReleasePhase(
				"asset.read",
				{},
				() => {
					throw error;
				},
				controller.signal,
			);
		} catch (caught) {
			actual = caught;
		}
		expect(actual).toBe(error);
		const end = logs.records().at(-1);
		expect(end.errorKind).toBe(name);
		expect(end.status).toBe(name === "AbortError" ? "cancelled" : "error");
	}
	expect(logs.lines.join("\n")).not.toContain("secret");
});

test("logging and clock failures do not change the operation result or failure", async () => {
	const logs = capture();
	const clock = spyOn(performance, "now").mockImplementation(() => Number.NaN);
	restores.push(() => clock.mockRestore());
	expect(await timeReleasePhase("index.prepare", {}, () => 7)).toBe(7);
	expect(logs.records().at(-1)).toMatchObject({ status: "success", timingUnavailable: true });
	const broken = spyOn(console, "info").mockImplementation(() => {
		throw new Error("sink failed");
	});
	restores.push(() => broken.mockRestore());
	expect(await timeReleasePhase("index.write", {}, () => 8)).toBe(8);
	const error = new Error("original integrity failure");
	let actual: unknown;
	try {
		await timeReleasePhase("index.write", {}, () => {
			throw error;
		});
	} catch (caught) {
		actual = caught;
	}
	expect(actual).toBe(error);
});

test("untrusted fields, paths, URLs and invalid numbers never enter the bounded phase record", async () => {
	const logs = capture();
	const details = {
		platform: "https://secret.test/token",
		version: "credential",
		fromVersion: "0.9.1",
		inputBytes: -1,
		outputBytes: Number.NaN,
		fileCount: Infinity,
		token: "secret",
		path: "/home/user/secret",
		url: "https://secret.test",
	} as ReleasePhaseDetails;
	await timeReleasePhase("https://secret.test/token", details, () => true);
	for (const record of logs.records()) {
		expect(record.phase).toBe("unknown");
		expect(record.fromVersion).toBe("0.9.1");
		expect(record.platform).toBeUndefined();
		expect(record.inputBytes).toBeUndefined();
		expect(record.token).toBeUndefined();
	}
	expect(logs.lines.join("\n")).not.toContain("secret");
	expect(logs.lines.every((line) => Buffer.byteLength(line) < 2048)).toBe(true);
});
