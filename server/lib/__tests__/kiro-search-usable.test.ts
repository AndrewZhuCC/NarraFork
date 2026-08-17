import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import {
	__setKiroModuleForTests,
	isKiroSearchUsable,
	type KiroModule,
	kiroRemoveCredentials,
	kiroRemoveUnhealthyCredentials,
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
	paidEntries: [],
	enterpriseEntries: [],
	freeEntries: [],
	paidTotal: 0,
	enterpriseTotal: 0,
	freeTotal: 1,
	unhealthyTotal: 0,
	currentId: "c1",
	loadBalancingMode: "priority",
	total: 1,
	available: 1,
};

describe("Kiro adapter credential removal compatibility", () => {
	test("falls back to single deletes for older plugin modules", () => {
		const existing = new Set(["one", "two"]);
		__setKiroModuleForTests({
			kiroRemoveCredential: (id) => {
				if (!existing.delete(id)) throw new Error(`Credential not found: ${id}`);
			},
		});

		expect(kiroRemoveCredentials(["one", "missing", "one", "two"])).toEqual({
			removed: ["one", "two"],
			notFound: ["missing"],
		});
	});

	test("rejects oversized legacy fallback deletes before mutating credentials", () => {
		let deleteCalls = 0;
		const entries = Array.from({ length: 101 }, (_, index) => ({
			id: `legacy-${index}`,
			priority: 0,
			disabled: true,
			disabledReason: "too_many_failures" as const,
			failureCount: 5,
			successCount: 0,
			hasProfileArn: false,
			hasStartUrl: false,
			tier: "free" as const,
		}));
		__setKiroModuleForTests({
			kiroSnapshot: () => ({
				entries,
				currentId: entries[0]?.id ?? "",
				loadBalancingMode: "priority",
				total: entries.length,
				available: 0,
			}),
			kiroRemoveCredential: () => {
				deleteCalls++;
			},
		});

		expect(() => kiroRemoveCredentials(entries.map((entry) => entry.id))).toThrow(
			"Legacy Kiro module supports at most 100 credential deletions per request",
		);
		expect(() => kiroRemoveUnhealthyCredentials()).toThrow(
			"Legacy Kiro module supports at most 100 credential deletions per request",
		);
		expect(deleteCalls).toBe(0);
	});

	test("derives unhealthy IDs before using the single-delete fallback", () => {
		const removed: string[] = [];
		const entry = (id: string, disabledReason?: "manual" | "too_many_failures") => ({
			id,
			priority: 0,
			disabled: !!disabledReason,
			disabledReason,
			failureCount: 0,
			successCount: 0,
			hasProfileArn: false,
			hasStartUrl: false,
			tier: "free" as const,
		});
		const fail = entry("fail", "too_many_failures");
		const manual = entry("manual", "manual");
		const healthy = entry("healthy");
		__setKiroModuleForTests({
			kiroSnapshot: () => ({
				...snapshotWithCredentials,
				entries: [fail, manual, healthy],
				freeEntries: [fail, manual, healthy],
				total: 3,
				available: 1,
				unhealthyTotal: 1,
			}),
			kiroRemoveCredential: (id) => removed.push(id),
		});

		expect(kiroRemoveUnhealthyCredentials()).toEqual({
			removed: ["fail"],
			reasons: ["too_many_failures", "account_suspended"],
		});
		expect(removed).toEqual(["fail"]);
	});
});

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
