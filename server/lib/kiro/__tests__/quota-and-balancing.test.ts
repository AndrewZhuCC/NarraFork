import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { KiroApiClient } from "../api-client";
import {
	__resetKiroConcurrencyForTests,
	acquireSlot,
	getHeadroom,
	onSuccess,
	onThrottle,
	releaseSlot,
	updateLimits,
} from "../concurrency-control";
import { classifyKiroCredentialTier, MultiTokenManager } from "../multi-token-manager";
import { isMonthlyRequestLimit } from "../quota-check";
import type { KiroConfig, KiroCredentials } from "../types";

const FUTURE = new Date(Date.now() + 60 * 60_000).toISOString();
const BASE_CONFIG: KiroConfig = {
	region: "us-east-1",
	kiroVersion: "1.26.1",
	systemVersion: "darwin#24.6.0",
	nodeVersion: "22.21.1",
	loadBalancingMode: "balanced",
	tierPriority: ["free", "enterprise", "paid"],
	concurrency: { default: { initialMax: 3, ceiling: 8, floor: 1 } },
	requestInterval: { free: 0, enterprise: 0, paid: 0 },
};

let tempDirs: string[] = [];

function makeCredential(id: string, overrides: Partial<KiroCredentials> = {}): KiroCredentials {
	return {
		id,
		refreshToken: `refresh-${id}`.padEnd(120, "x"),
		accessToken: `access-${id}`,
		expiresAt: FUTURE,
		priority: 0,
		...overrides,
	};
}

function createManagerFixture(
	credentials: KiroCredentials[],
	config: Partial<KiroConfig> = {},
	usageCache: Record<string, unknown> = {},
	isMultipleFormat = true,
): { manager: MultiTokenManager; credentialsPath: string; usagePath: string } {
	const dir = mkdtempSync(join(tmpdir(), "narrafork-kiro-manager-"));
	tempDirs.push(dir);
	const credentialsPath = join(dir, "credentials.json");
	const usagePath = join(dir, "kiro_usage.json");
	writeFileSync(
		credentialsPath,
		JSON.stringify(isMultipleFormat ? credentials : credentials[0], null, 2),
	);
	writeFileSync(usagePath, JSON.stringify(usageCache, null, 2));
	return {
		manager: new MultiTokenManager(
			{ ...BASE_CONFIG, ...config },
			credentials,
			credentialsPath,
			isMultipleFormat,
		),
		credentialsPath,
		usagePath,
	};
}

function createManager(
	credentials: KiroCredentials[],
	config: Partial<KiroConfig> = {},
	usageCache: Record<string, unknown> = {},
): MultiTokenManager {
	return createManagerFixture(credentials, config, usageCache).manager;
}

function delay(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

beforeEach(() => {
	__resetKiroConcurrencyForTests();
	tempDirs = [];
});

afterEach(() => {
	__resetKiroConcurrencyForTests();
	for (const dir of tempDirs.splice(0)) {
		rmSync(dir, { recursive: true, force: true });
	}
});

describe("Kiro quota error detection", () => {
	test("detects overage request limit exhaustion", () => {
		expect(isMonthlyRequestLimit("OVERAGE_REQUEST_LIMIT_EXCEEDED")).toBe(true);
		expect(
			isMonthlyRequestLimit(
				JSON.stringify({
					__type: "ServiceQuotaExceededException",
					reason: "OVERAGE_REQUEST_LIMIT_EXCEEDED",
				}),
			),
		).toBe(true);
	});
});

describe("Kiro credential balancing", () => {
	test("classifies legacy builder-id credentials from subscription title", () => {
		expect(
			classifyKiroCredentialTier(
				makeCredential("builder-free", { authMethod: "builder-id", subscriptionTitle: "Free" }),
			),
		).toBe("free");
		expect(
			classifyKiroCredentialTier(
				makeCredential("builder-pro", { authMethod: "builder-id", subscriptionTitle: "Kiro Pro" }),
			),
		).toBe("paid");
	});

	test("honors tier priority before usage balancing", async () => {
		const manager = createManager(
			[
				makeCredential("paid", { subscriptionTitle: "Kiro Pro" }),
				makeCredential("free", { subscriptionTitle: "Free" }),
			],
			{},
			{
				paid: { currentUsage: 0 },
				free: { currentUsage: 5000 },
			},
		);

		const ctx = await manager.acquireContext("claude-sonnet-4.5");

		expect(ctx.id).toBe("free");
	});

	test("can ignore tier priority and balance across all credentials by usage", async () => {
		const manager = createManager(
			[
				makeCredential("free", { subscriptionTitle: "Free", meterUsage: 5000 }),
				makeCredential("paid", { subscriptionTitle: "Kiro Pro", meterUsage: 10 }),
			],
			{ ignoreTierPriority: true },
		);

		const ctx = await manager.acquireContext("claude-sonnet-4.5");

		expect(ctx.id).toBe("paid");
	});

	test("uses meterUsage over stale lower usage cache for balancing", async () => {
		const manager = createManager(
			[
				makeCredential("stale-cache", { subscriptionTitle: "Free", meterUsage: 1000 }),
				makeCredential("low-usage", { subscriptionTitle: "Free", meterUsage: 10 }),
			],
			{ ignoreTierPriority: true },
			{
				"stale-cache": {
					id: "stale-cache",
					currentUsage: 1,
					usageLimit: 10_000,
					remaining: 9999,
					usagePercentage: 0.01,
					queriedAt: new Date().toISOString(),
				},
			},
		);

		const ctx = await manager.acquireContext("claude-sonnet-4.5");

		expect(ctx.id).toBe("low-usage");
	});

	test("does not reuse a failed credential within one chat request", async () => {
		const manager = createManager([
			makeCredential("first", { subscriptionTitle: "Free", priority: 0 }),
			makeCredential("second", { subscriptionTitle: "Free", priority: 1 }),
		]);
		const client = new KiroApiClient(manager);
		const originalFetch = globalThis.fetch;
		const requestedTokens: string[] = [];
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			requestedTokens.push(String((init?.headers as Record<string, string>)?.Authorization ?? ""));
			return requestedTokens.length === 1
				? new Response("unauthorized", { status: 401 })
				: new Response("", { status: 200 });
		}) as typeof fetch;

		try {
			const response = await client.sendChatRequest({
				conversationState: {
					conversationId: "conv-1",
					currentMessage: {
						userInputMessage: { content: "hi", modelId: "claude-sonnet-4.5" },
					},
				},
			});
			response.release();
			expect(response.credentialId).toBe("second");
			expect(requestedTokens).toEqual(["Bearer access-first", "Bearer access-second"]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("Kiro credential persistence", () => {
	test("migrates single-object credentials to array format", () => {
		const { credentialsPath } = createManagerFixture([makeCredential("legacy")], {}, {}, false);

		const persisted = JSON.parse(readFileSync(credentialsPath, "utf-8"));

		expect(Array.isArray(persisted)).toBe(true);
		expect(persisted[0].id).toBe("legacy");
		expect(typeof persisted[0].machineId).toBe("string");
	});

	test("persists usage cache updates when metering is accumulated", () => {
		const { manager, usagePath } = createManagerFixture(
			[makeCredential("metered", { meterUsage: 1 })],
			{},
			{
				metered: {
					id: "metered",
					currentUsage: 1,
					usageLimit: 100,
					remaining: 99,
					usagePercentage: 1,
					queriedAt: new Date().toISOString(),
				},
			},
		);

		manager.addMeterUsage("metered", 4);
		const persisted = JSON.parse(readFileSync(usagePath, "utf-8"));

		expect(persisted.metered.currentUsage).toBe(5);
		expect(persisted.metered.remaining).toBe(95);
	});
});

describe("Kiro adaptive concurrency", () => {
	test("releases headroom and shrinks/grows around throttles", async () => {
		updateLimits({ default: { initialMax: 2, ceiling: 3, floor: 1 } });

		await acquireSlot("cred-1");
		expect(getHeadroom("cred-1")).toBe(1);
		releaseSlot("cred-1");
		expect(getHeadroom("cred-1")).toBe(2);

		onThrottle("cred-1");
		expect(getHeadroom("cred-1")).toBe(1);

		for (let i = 0; i < 3; i++) onSuccess("cred-1");
		expect(getHeadroom("cred-1")).toBe(2);
	});

	test("holds queued requests during credential backoff after throttle", async () => {
		updateLimits({ default: { initialMax: 1, ceiling: 1, floor: 1 } });

		await acquireSlot("throttled");
		let secondResolved = false;
		const second = acquireSlot("throttled").then(() => {
			secondResolved = true;
		});

		onThrottle("throttled");
		releaseSlot("throttled");
		await delay(50);
		expect(secondResolved).toBe(false);

		await second;
		expect(secondResolved).toBe(true);
		releaseSlot("throttled");
	});
});
