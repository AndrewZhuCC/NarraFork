import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import {
	__setKiroModuleForTests,
	isKiroSearchUsable,
	type KiroModule,
	type ManagerSnapshot,
} from "../kiro-adapter";
import { settings } from "../settings";

function makeKiroModuleStub(options: {
	available: boolean;
	snapshot?: ManagerSnapshot | null;
}): Partial<KiroModule> {
	return {
		isKiroAvailable: () => options.available,
		kiroSnapshot: () => options.snapshot ?? null,
	};
}

const snapshotWithCredentials: ManagerSnapshot = {
	entries: [],
	currentId: "c1",
	loadBalancingMode: "priority",
	total: 1,
	available: 1,
};

describe("isKiroSearchUsable", () => {
	let originalDisabled: string[] | undefined;
	let originalEnv: string | undefined;

	beforeEach(() => {
		originalDisabled = settings.agent?.disabledProviders;
		originalEnv = process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		if (settings.agent) settings.agent.disabledProviders = [];
		delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
	});

	afterEach(() => {
		if (settings.agent) settings.agent.disabledProviders = originalDisabled;
		if (originalEnv === undefined) delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		else process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = originalEnv;
		__setKiroModuleForTests(null);
	});

	test("usable when plugin available with credentials and provider enabled", () => {
		__setKiroModuleForTests(
			makeKiroModuleStub({ available: true, snapshot: snapshotWithCredentials }),
		);
		expect(isKiroSearchUsable()).toBe(true);
	});

	test("not usable when provider disabled via settings.disabledProviders", () => {
		__setKiroModuleForTests(
			makeKiroModuleStub({ available: true, snapshot: snapshotWithCredentials }),
		);
		if (settings.agent) settings.agent.disabledProviders = ["kiro"];
		expect(isKiroSearchUsable()).toBe(false);
	});

	test("not usable when provider disabled via environment variable", () => {
		__setKiroModuleForTests(
			makeKiroModuleStub({ available: true, snapshot: snapshotWithCredentials }),
		);
		process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = "1";
		expect(isKiroSearchUsable()).toBe(false);
	});

	test("not usable when no credentials are available", () => {
		__setKiroModuleForTests(
			makeKiroModuleStub({
				available: true,
				snapshot: { ...snapshotWithCredentials, available: 0 },
			}),
		);
		expect(isKiroSearchUsable()).toBe(false);
	});

	test("not usable when plugin is unavailable", () => {
		__setKiroModuleForTests(makeKiroModuleStub({ available: false }));
		expect(isKiroSearchUsable()).toBe(false);
	});
});
