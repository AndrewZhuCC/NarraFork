import { afterAll, afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Hono } from "hono";
import type { KiroModule } from "../../lib/kiro-adapter";

const previousHome = process.env.NARRAFORK_HOME;
const testHome = mkdtempSync(join(tmpdir(), "narrafork-kiro-admin-route-"));
process.env.NARRAFORK_HOME = testHome;

const actualKiroModule = await import("../../lib/kiro/index");
const { __setKiroModuleForTests, initKiro } = await import("../../lib/kiro-adapter");
const { releaseDatabaseInstanceLockOnly } = await import("../../db");
const { kiroRoutes } = await import("../kiro");

const actualKiroFacade: Partial<KiroModule> = {
	initKiro: actualKiroModule.initKiro,
	isKiroAvailable: actualKiroModule.isKiroAvailable,
	kiroSnapshot: actualKiroModule.kiroSnapshot,
	kiroGetAllUsageCache: actualKiroModule.kiroGetAllUsageCache,
	kiroQueueUsageFetch: actualKiroModule.kiroQueueUsageFetch,
	kiroUsageQueueSnapshot: actualKiroModule.kiroUsageQueueSnapshot,
	kiroRemoveCredential: actualKiroModule.kiroRemoveCredential,
	kiroRemoveCredentials: actualKiroModule.kiroRemoveCredentials,
	kiroRemoveUnhealthyCredentials: actualKiroModule.kiroRemoveUnhealthyCredentials,
};

const credentialsPath = join(testHome, "kiro", "credentials.json");
const configPath = join(testHome, "kiro", "config.json");
const usagePath = join(testHome, "kiro", "kiro_usage.json");
const future = new Date(Date.now() + 60 * 60_000).toISOString();

function credential(id: string, overrides: Record<string, unknown> = {}): Record<string, unknown> {
	return {
		id,
		refreshToken: `refresh-${id}`.padEnd(120, "x"),
		accessToken: `access-${id}`,
		expiresAt: future,
		meterUsage: 0,
		...overrides,
	};
}

function initialize(credentials: Array<Record<string, unknown>>): void {
	mkdirSync(join(testHome, "kiro"), { recursive: true });
	writeFileSync(credentialsPath, JSON.stringify(credentials, null, 2));
	writeFileSync(configPath, JSON.stringify({ region: "us-east-1" }, null, 2));
	writeFileSync(
		usagePath,
		JSON.stringify(
			Object.fromEntries(
				credentials.map((entry, index) => [
					String(entry.id),
					{
						id: entry.id,
						currentUsage: index + 1,
						usageLimit: 100,
						remaining: 99 - index,
						usagePercentage: index + 1,
						queriedAt: new Date().toISOString(),
					},
				]),
			),
			null,
			2,
		),
	);
	__setKiroModuleForTests(actualKiroFacade);
	expect(initKiro({ credentialsPath, configPath })).toBe(true);
}

function createApp(): Hono {
	const app = new Hono();
	app.use("*", async (c, next) => {
		const user = { sub: "admin-id", role: "admin" as const, iat: 0, exp: Number.MAX_SAFE_INTEGER };
		c.set("auth", { type: "session", user });
		c.set("user", user);
		await next();
	});
	app.route("/kiro", kiroRoutes);
	return app;
}

beforeEach(() => {
	initialize([
		credential("free-1", { subscriptionTitle: "Free" }),
		credential("paid-1", { subscriptionTitle: "Kiro Pro" }),
		credential("enterprise-1", { profileArn: "arn:aws:codewhisperer:profile/one" }),
		credential("paid-2", { subscriptionTitle: "Kiro Pro Plus" }),
		credential("free-2", { subscriptionTitle: "Free" }),
	]);
});

afterEach(() => {
	__setKiroModuleForTests(actualKiroFacade);
});

afterAll(() => {
	releaseDatabaseInstanceLockOnly();
	if (previousHome === undefined) delete process.env.NARRAFORK_HOME;
	else process.env.NARRAFORK_HOME = previousHome;
	try {
		rmSync(testHome, { recursive: true, force: true });
	} catch {
		// The process-wide SQLite handle remains open until the Bun test worker exits on Windows.
	}
});

describe("Kiro credential administration routes", () => {
	test("status independently paginates tiers and limits usage cache to returned entries", async () => {
		const response = await createApp().request(
			"/kiro/status?paidPage=2&enterprisePage=1&freePage=1&pageSize=1",
		);
		const body = (await response.json()) as {
			snapshot: {
				entries: Array<{ id: string }>;
				paidEntries: Array<{ id: string }>;
				enterpriseEntries: Array<{ id: string }>;
				freeEntries: Array<{ id: string }>;
				paidTotal: number;
				enterpriseTotal: number;
				freeTotal: number;
			};
			usageCache: Record<string, unknown>;
		};

		expect(response.status).toBe(200);
		expect(body.snapshot.paidEntries.map((entry) => entry.id)).toEqual(["paid-2"]);
		expect(body.snapshot.enterpriseEntries.map((entry) => entry.id)).toEqual(["enterprise-1"]);
		expect(body.snapshot.freeEntries.map((entry) => entry.id)).toEqual(["free-1"]);
		expect(body.snapshot.entries.map((entry) => entry.id)).toEqual([
			"paid-2",
			"enterprise-1",
			"free-1",
		]);
		expect(body.snapshot.paidTotal).toBe(2);
		expect(body.snapshot.enterpriseTotal).toBe(1);
		expect(body.snapshot.freeTotal).toBe(2);
		expect(Object.keys(body.usageCache)).toEqual(["paid-2", "enterprise-1", "free-1"]);

		const defaultPageResponse = await createApp().request("/kiro/status?pageSize=1");
		const defaultPageBody = (await defaultPageResponse.json()) as {
			snapshot: {
				paidEntries: Array<{ id: string }>;
				enterpriseEntries: Array<{ id: string }>;
				freeEntries: Array<{ id: string }>;
			};
		};
		expect(defaultPageResponse.status).toBe(200);
		expect(defaultPageBody.snapshot.paidEntries.map((entry) => entry.id)).toEqual(["paid-1"]);
		expect(defaultPageBody.snapshot.enterpriseEntries.map((entry) => entry.id)).toEqual([
			"enterprise-1",
		]);
		expect(defaultPageBody.snapshot.freeEntries.map((entry) => entry.id)).toEqual(["free-1"]);

		const fullResponse = await createApp().request("/kiro/status");
		const fullBody = (await fullResponse.json()) as {
			snapshot: { entries: Array<{ id: string }> };
		};
		expect(fullBody.snapshot.entries.map((entry) => entry.id)).toEqual([
			"free-1",
			"paid-1",
			"enterprise-1",
			"paid-2",
			"free-2",
		]);
	});

	test("status rejects invalid or oversized pagination parameters", async () => {
		for (const query of [
			"pageSize=0",
			"paidPage=-1&pageSize=1",
			"freePage=1.5&pageSize=1",
			"pageSize=101",
		]) {
			const response = await createApp().request(`/kiro/status?${query}`);
			expect(response.status).toBe(400);
		}
	});

	test("paginated status still queues usage for an off-page credential", async () => {
		const queued: string[] = [];
		let snapshotCalls = 0;
		const fullEntry = {
			id: "off-page-usage",
			priority: 0,
			disabled: false,
			failureCount: 0,
			successCount: 0,
			hasProfileArn: false,
			hasStartUrl: false,
			tier: "free" as const,
		};
		const pageEntry = { ...fullEntry, id: "current-page", meterUsage: 0 };
		const baseSnapshot = {
			currentId: "current-page",
			loadBalancingMode: "priority" as const,
			total: 2,
			available: 2,
		};
		__setKiroModuleForTests({
			isKiroAvailable: () => true,
			kiroSnapshot: () => {
				snapshotCalls++;
				return {
					...baseSnapshot,
					entries: [pageEntry, fullEntry],
				};
			},
			kiroGetAllUsageCache: () => ({}),
			kiroQueueUsageFetch: (id) => queued.push(id),
			kiroUsageQueueSnapshot: () => ({ items: [], isRunning: false }),
		});

		const response = await createApp().request("/kiro/status?freePage=1&pageSize=1");
		const body = (await response.json()) as {
			snapshot: { freeEntries: Array<{ id: string }>; freeTotal: number };
		};
		expect(response.status).toBe(200);
		expect(snapshotCalls).toBe(1);
		expect(body.snapshot.freeEntries.map((entry) => entry.id)).toEqual(["current-page"]);
		expect(body.snapshot.freeTotal).toBe(2);
		expect(queued).toEqual(["off-page-usage"]);
	});

	test("batch deletion validates IDs and reports missing credentials", async () => {
		const invalid = await createApp().request("/kiro/credentials/batch", {
			method: "DELETE",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ ids: ["paid-1", "   "] }),
		});
		expect(invalid.status).toBe(400);

		const largeBatch = await createApp().request("/kiro/credentials/batch", {
			method: "DELETE",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ ids: Array.from({ length: 101 }, (_, index) => `id-${index}`) }),
		});
		const largeBatchBody = (await largeBatch.json()) as { notFound: string[] };
		expect(largeBatch.status).toBe(200);
		expect(largeBatchBody.notFound).toHaveLength(101);

		const response = await createApp().request("/kiro/credentials/batch", {
			method: "DELETE",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ ids: ["paid-1", "missing", "paid-1"] }),
		});
		expect(response.status).toBe(200);
		expect(await response.json()).toEqual({ removed: ["paid-1"], notFound: ["missing"] });

		const missing = await createApp().request("/kiro/credentials/paid-1", {
			method: "DELETE",
		});
		expect(missing.status).toBe(404);
	});

	test("legacy modules reject oversized fallback deletes before mutation", async () => {
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
			isKiroAvailable: () => true,
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

		const batchResponse = await createApp().request("/kiro/credentials/batch", {
			method: "DELETE",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ ids: entries.map((entry) => entry.id) }),
		});
		expect(batchResponse.status).toBe(409);

		const unhealthyResponse = await createApp().request("/kiro/credentials/unhealthy", {
			method: "DELETE",
		});
		expect(unhealthyResponse.status).toBe(409);
		expect(deleteCalls).toBe(0);
	});

	test("unhealthy deletion excludes manual and quota disables", async () => {
		initialize([
			credential("suspended", {
				disabled: true,
				disabledReason: "account_suspended",
				disabledAt: new Date().toISOString(),
			}),
			credential("manual", {
				disabled: true,
				disabledReason: "manual",
				disabledAt: new Date().toISOString(),
			}),
			credential("quota", {
				disabled: true,
				disabledReason: "quota_exhausted",
				disabledAt: new Date().toISOString(),
			}),
			credential("healthy"),
		]);

		const response = await createApp().request("/kiro/credentials/unhealthy", {
			method: "DELETE",
		});
		expect(response.status).toBe(200);
		expect(await response.json()).toEqual({
			removed: ["suspended"],
			reasons: ["too_many_failures", "account_suspended"],
		});

		const status = await createApp().request("/kiro/status");
		const body = (await status.json()) as { snapshot: { entries: Array<{ id: string }> } };
		expect(body.snapshot.entries.map((entry) => entry.id)).toEqual(["manual", "quota", "healthy"]);
	});
});
