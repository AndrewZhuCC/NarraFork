import { describe, expect, spyOn, test } from "bun:test";
import { resolve } from "node:path";
import { getDbPath } from "../server/db/connection";
import { narraforkDir, settings } from "../server/lib/settings";
import { repairMissingSettingsBranches, testEnvironment } from "./preload";

function normalizedPath(path: string): string {
	const resolved = resolve(path);
	return process.platform === "win32" ? resolved.toLowerCase() : resolved;
}

describe("global test data isolation", () => {
	test("valid settings repair does not deep-clone the entire defaults", () => {
		const clone = spyOn(globalThis, "structuredClone");
		try {
			repairMissingSettingsBranches();
			expect(clone).not.toHaveBeenCalled();
		} finally {
			clone.mockRestore();
		}
	});

	test("missing branches are restored from the isolated snapshot, not shipped defaults", () => {
		const expected = structuredClone(settings.agent);
		expect(expected.requestDumpRecordingEnabled).toBe(false);
		for (const value of [undefined, null]) {
			Reflect.set(settings, "agent", value);
			repairMissingSettingsBranches();
			expect(settings.agent).toEqual(expected);
			expect(settings.agent).not.toBe(expected);
		}
	});

	test("redirects database and home-based storage away from the real ~/.narrafork", () => {
		expect(normalizedPath(process.env.HOME ?? "")).toBe(
			normalizedPath(testEnvironment.isolatedHome),
		);
		expect(normalizedPath(process.env.USERPROFILE ?? "")).toBe(
			normalizedPath(testEnvironment.isolatedHome),
		);
		expect(normalizedPath(process.env.NARRAFORK_HOME ?? "")).toBe(
			normalizedPath(testEnvironment.narraforkHome),
		);
		expect(normalizedPath(getDbPath())).toBe(
			normalizedPath(resolve(testEnvironment.narraforkHome, "narrafork.db")),
		);
		expect(normalizedPath(narraforkDir)).toBe(normalizedPath(testEnvironment.narraforkHome));
		expect(normalizedPath(getDbPath())).not.toBe(
			normalizedPath(resolve(testEnvironment.realNarraforkHome, "narrafork.db")),
		);
		expect(normalizedPath(narraforkDir)).not.toBe(
			normalizedPath(testEnvironment.realNarraforkHome),
		);
	});
});
