import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { KiroApiClient } from "../api-client";
import {
	apiKeyOf,
	applyKiroTokenTypeHeader,
	type CanonicalKiroAuthMethod,
	canonicalKiroAuthMethod,
	isApiKeyAuthMethod,
	isApiKeyCredential,
} from "../auth-method";
import { __resetKiroConcurrencyForTests } from "../concurrency-control";
import { loadAllCredentials } from "../config";
import { hasImportableSecret, normalizeImportCredentials } from "../import-normalize";
import { generateMachineId } from "../machine-id";
import { KiroMcpClient } from "../mcp-client";
import { MultiTokenManager } from "../multi-token-manager";
import { refreshCredentials } from "../token-manager";
import type { KiroConfig, KiroCredentials } from "../types";

const BASE_CONFIG: KiroConfig = {
	region: "us-east-1",
	kiroVersion: "1.26.1",
	systemVersion: "darwin#24.6.0",
	nodeVersion: "22.21.1",
	loadBalancingMode: "priority",
	tierPriority: ["free", "enterprise", "paid"],
	concurrency: { default: { initialMax: 3, ceiling: 8, floor: 1 } },
	requestInterval: { free: 0, enterprise: 0, paid: 0 },
};

let tempDirs: string[] = [];

function makeApiKeyCredential(
	id: string,
	overrides: Partial<KiroCredentials> = {},
): KiroCredentials {
	return {
		id,
		authMethod: "api_key",
		apiKey: `ksk-${id}`,
		accessToken: `ksk-${id}`,
		...overrides,
	};
}

function createManagerFixture(credentials: KiroCredentials[]): {
	manager: MultiTokenManager;
	credentialsPath: string;
} {
	const dir = mkdtempSync(join(tmpdir(), "narrafork-kiro-apikey-"));
	tempDirs.push(dir);
	const credentialsPath = join(dir, "credentials.json");
	writeFileSync(credentialsPath, JSON.stringify(credentials, null, 2));
	writeFileSync(join(dir, "kiro_usage.json"), "{}");
	return {
		manager: new MultiTokenManager(BASE_CONFIG, credentials, credentialsPath, true),
		credentialsPath,
	};
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

describe("Kiro auth method canonicalization", () => {
	test("recognizes all api_key spellings", () => {
		for (const method of ["api_key", "apikey", "api-key", "API_KEY", " Api-Key "]) {
			expect(isApiKeyAuthMethod(method)).toBe(true);
		}
		for (const method of ["social", "idc", "", undefined, "builder-id"]) {
			expect(isApiKeyAuthMethod(method)).toBe(false);
		}
	});

	test("canonicalizes auth methods like NUG", () => {
		const cases: Array<[Partial<KiroCredentials>, CanonicalKiroAuthMethod]> = [
			[{ authMethod: "api_key", apiKey: "ksk-1" }, "api_key"],
			[{ authMethod: "APIKEY", apiKey: "ksk-1" }, "api_key"],
			[{ authMethod: "iam" }, "idc"],
			[{ authMethod: "builder-id" }, "idc"],
			[{ authMethod: "social" }, "social"],
			[{ clientId: "cid", clientSecret: "secret" }, "idc"],
			[{ refreshToken: "rt" }, "social"],
			// No refresh token but a key present → inferred api_key
			[{ apiKey: "ksk-only" }, "api_key"],
		];
		for (const [creds, want] of cases) {
			expect(canonicalKiroAuthMethod(creds as KiroCredentials)).toBe(want);
		}
	});

	test("reads the api key from apiKey or accessToken", () => {
		expect(apiKeyOf({ authMethod: "api_key", apiKey: "ksk-a" })).toBe("ksk-a");
		expect(apiKeyOf({ authMethod: "api_key", accessToken: "ksk-b" })).toBe("ksk-b");
		// A refresh-token credential's accessToken is not an API key
		expect(apiKeyOf({ authMethod: "social", accessToken: "at" })).toBeUndefined();
		expect(isApiKeyCredential({ authMethod: "api_key" })).toBe(false);
		expect(isApiKeyCredential(makeApiKeyCredential("x"))).toBe(true);
	});

	test("sets the TokenType header only for api_key credentials", () => {
		const apiKeyHeaders: Record<string, string> = {};
		applyKiroTokenTypeHeader(apiKeyHeaders, makeApiKeyCredential("a"));
		expect(apiKeyHeaders.TokenType).toBe("API_KEY");

		const socialHeaders: Record<string, string> = {};
		applyKiroTokenTypeHeader(socialHeaders, { authMethod: "social", refreshToken: "rt" });
		expect(socialHeaders.TokenType).toBeUndefined();
	});
});

describe("Kiro api_key token lifecycle", () => {
	test("never refreshes over the network and stamps a far-future expiry", async () => {
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () => {
			throw new Error("api_key credentials must not hit the refresh endpoint");
		}) as unknown as typeof fetch;

		try {
			const refreshed = await refreshCredentials(makeApiKeyCredential("static"), BASE_CONFIG);
			expect(refreshed.accessToken).toBe("ksk-static");
			expect(refreshed.apiKey).toBe("ksk-static");
			expect(refreshed.authMethod).toBe("api_key");
			expect(new Date(refreshed.expiresAt ?? "").getTime()).toBeGreaterThan(Date.now());
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("rejects an api_key credential with no stored key", async () => {
		await expect(
			refreshCredentials({ authMethod: "api_key" } as KiroCredentials, BASE_CONFIG),
		).rejects.toThrow(/missing stored key/);
	});

	test("acquires a context without expiresAt and without refreshing", async () => {
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () => {
			throw new Error("no network expected");
		}) as unknown as typeof fetch;

		try {
			// No expiresAt at all — a refresh-token credential would be treated as expired.
			const { manager } = createManagerFixture([makeApiKeyCredential("static")]);
			const ctx = await manager.acquireContext("claude-sonnet-4.5");
			expect(ctx.id).toBe("static");
			expect(ctx.token).toBe("ksk-static");
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("reports api_key in the snapshot", () => {
		const { manager } = createManagerFixture([
			makeApiKeyCredential("static"),
			{ id: "oauth", refreshToken: "rt".padEnd(120, "x"), accessToken: "at", authMethod: "social" },
		]);
		const entries = manager.snapshot().entries;
		const apiKeyEntry = entries.find((e) => e.id === "static");
		const oauthEntry = entries.find((e) => e.id === "oauth");
		expect(apiKeyEntry?.isApiKey).toBe(true);
		expect(apiKeyEntry?.authMethod).toBe("api_key");
		expect(oauthEntry?.isApiKey).toBe(false);
		expect(oauthEntry?.authMethod).toBe("social");
	});

	test("keeps legacy authMethod values in the snapshot for UI tier classification", () => {
		const { manager } = createManagerFixture([
			{
				id: "legacy",
				authMethod: "builder-id",
				refreshToken: "rt".padEnd(120, "x"),
				accessToken: "at",
				subscriptionTitle: "Free",
			},
		]);
		const entry = manager.snapshot().entries[0];
		expect(entry.authMethod).toBe("builder-id");
		expect(entry.isApiKey).toBe(false);
	});

	test("derives a machineId from the api key when no refresh token exists", () => {
		const machineId = generateMachineId(makeApiKeyCredential("static"), BASE_CONFIG);
		expect(machineId).toMatch(/^[0-9a-f]{64}$/);
	});

	test("rejects a manual refresh instead of rewriting the credentials file", async () => {
		const { manager, credentialsPath } = createManagerFixture([makeApiKeyCredential("static")]);
		const before = readFileSync(credentialsPath, "utf-8");

		// The UI hides the refresh button for api_key credentials, but the route is
		// callable directly — a refresh here would stamp a fresh far-future expiry and
		// persist it for no reason.
		await expect(manager.manualRefresh("static")).rejects.toThrow(/never refreshed/);
		expect(readFileSync(credentialsPath, "utf-8")).toBe(before);
	});

	test("still refreshes a refresh-token credential manually", async () => {
		const { manager } = createManagerFixture([
			{
				id: "oauth",
				refreshToken: "rt".padEnd(120, "x"),
				accessToken: "at",
				authMethod: "social",
			},
		]);
		const originalFetch = globalThis.fetch;
		globalThis.fetch = (async () =>
			new Response(
				JSON.stringify({
					accessToken: "at-new",
					refreshToken: "rt2".padEnd(120, "y"),
					expiresAt: new Date(Date.now() + 3_600_000).toISOString(),
				}),
				{ status: 200 },
			)) as unknown as typeof fetch;

		try {
			await manager.manualRefresh("oauth");
			expect(manager.snapshot().entries[0]?.isApiKey).toBe(false);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("Kiro api_key requests", () => {
	test("sends TokenType: API_KEY on chat requests", async () => {
		const { manager } = createManagerFixture([makeApiKeyCredential("static")]);
		const client = new KiroApiClient(manager);
		const originalFetch = globalThis.fetch;
		const seen: Array<Record<string, string | null>> = [];
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			const headers = new Headers(init?.headers);
			seen.push({
				auth: headers.get("Authorization"),
				tokenType: headers.get("TokenType"),
			});
			return new Response("", { status: 200 });
		}) as typeof fetch;

		try {
			const response = await client.sendChatRequest({
				conversationState: {
					conversationId: "conv-api-key",
					currentMessage: {
						userInputMessage: { content: "hi", modelId: "claude-sonnet-4.5" },
					},
				},
			});
			response.release();
			expect(seen).toEqual([{ auth: "Bearer ksk-static", tokenType: "API_KEY" }]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("sends TokenType: API_KEY on MCP requests", async () => {
		const { manager } = createManagerFixture([makeApiKeyCredential("static")]);
		const client = new KiroMcpClient(manager);
		const originalFetch = globalThis.fetch;
		let tokenType: string | null | undefined;
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			tokenType = new Headers(init?.headers).get("TokenType");
			return new Response(JSON.stringify({ id: "1", jsonrpc: "2.0" }), { status: 200 });
		}) as typeof fetch;

		try {
			await client.listTools();
			expect(tokenType).toBe("API_KEY");
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	test("omits TokenType for refresh-token credentials", async () => {
		const { manager } = createManagerFixture([
			{
				id: "oauth",
				refreshToken: "rt".padEnd(120, "x"),
				accessToken: "at",
				authMethod: "social",
				expiresAt: new Date(Date.now() + 60 * 60_000).toISOString(),
			},
		]);
		const client = new KiroApiClient(manager);
		const originalFetch = globalThis.fetch;
		let tokenType: string | null = "unset";
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			tokenType = new Headers(init?.headers).get("TokenType");
			return new Response("", { status: 200 });
		}) as typeof fetch;

		try {
			const response = await client.sendChatRequest({
				conversationState: {
					conversationId: "conv-oauth",
					currentMessage: {
						userInputMessage: { content: "hi", modelId: "claude-sonnet-4.5" },
					},
				},
			});
			response.release();
			expect(tokenType).toBeNull();
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("Kiro api_key import", () => {
	test("accepts the kiro.rs kiroApiKey file shape", () => {
		const [cred] = normalizeImportCredentials([
			{ kiroApiKey: "ksk_your_api_key_here", authMethod: "api_key" },
		]);
		expect(cred.authMethod).toBe("api_key");
		expect(cred.apiKey).toBe("ksk_your_api_key_here");
		expect(cred.accessToken).toBe("ksk_your_api_key_here");
		expect(hasImportableSecret(cred)).toBe(true);
	});

	test("accepts snake_case aliases", () => {
		const [cred] = normalizeImportCredentials([
			{ kiro_api_key: "ksk-snake", auth_method: "api_key" },
		]);
		expect(cred.authMethod).toBe("api_key");
		expect(cred.apiKey).toBe("ksk-snake");
	});

	test("accepts the accounts wrapper", () => {
		const creds = normalizeImportCredentials([
			{
				accounts: [
					{
						credentials: { authMethod: "api_key", apiKey: "ksk-123" },
						email: "a@example.com",
						subscription: { title: "Kiro Pro" },
					},
				],
			},
		]);
		expect(creds).toHaveLength(1);
		expect(creds[0].apiKey).toBe("ksk-123");
		expect(creds[0].email).toBe("a@example.com");
		expect(creds[0].subscriptionTitle).toBe("Kiro Pro");
	});

	test("infers api_key when only a key is supplied", () => {
		const [cred] = normalizeImportCredentials([{ apiKey: "ksk-inferred" }]);
		expect(cred.authMethod).toBe("api_key");
		expect(cred.accessToken).toBe("ksk-inferred");
	});

	test("drops an inherited expiry from api_key payloads", () => {
		const [cred] = normalizeImportCredentials([
			{ apiKey: "ksk-1", authMethod: "api_key", expiresAt: "2020-01-01T00:00:00Z" },
		]);
		expect(cred.expiresAt).toBeUndefined();
	});

	test("leaves refresh-token credentials untouched", () => {
		const [cred] = normalizeImportCredentials([
			{ refreshToken: "rt".padEnd(120, "x"), authMethod: "social", accessToken: "at" },
		]);
		expect(cred.authMethod).toBe("social");
		expect(cred.apiKey).toBeUndefined();
		expect(cred.accessToken).toBe("at");
	});

	test("marks payloads without any secret as unusable", () => {
		const [cred] = normalizeImportCredentials([{ email: "a@example.com" }]);
		expect(hasImportableSecret(cred)).toBe(false);
	});

	test("dedupes api_key credentials on the key value", () => {
		const { manager } = createManagerFixture([
			makeApiKeyCredential("existing", { apiKey: "ksk-x", accessToken: "ksk-x" }),
		]);
		const result = manager.addCredentials([
			// Same key, different casing of the auth method → duplicate
			{ authMethod: "apikey", apiKey: "ksk-x", accessToken: "ksk-x" },
			{ authMethod: "api_key", apiKey: "ksk-new", accessToken: "ksk-new" },
		]);
		expect(result).toEqual({ added: 1, duplicates: 1 });
		expect(manager.snapshot().total).toBe(2);
	});

	test("canonicalizes api_key credentials when loaded from disk", () => {
		const dir = mkdtempSync(join(tmpdir(), "narrafork-kiro-apikey-load-"));
		tempDirs.push(dir);
		const credentialsPath = join(dir, "credentials.json");
		writeFileSync(
			credentialsPath,
			JSON.stringify([{ id: "loaded", authMethod: "APIKEY", apiKey: "ksk-loaded" }]),
		);

		const { credentials } = loadAllCredentials(credentialsPath);

		expect(credentials[0].authMethod).toBe("api_key");
		expect(credentials[0].accessToken).toBe("ksk-loaded");
	});

	test("persists api_key credentials as api_key", () => {
		const { credentialsPath } = createManagerFixture([makeApiKeyCredential("static")]);
		const persisted = JSON.parse(readFileSync(credentialsPath, "utf-8"));
		expect(persisted[0].authMethod).toBe("api_key");
		expect(persisted[0].apiKey).toBe("ksk-static");
	});
});
