import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { __resetKiroConcurrencyForTests } from "../concurrency-control";
import { DEFAULT_KIRO_REGION, effectiveApiRegion, effectiveAuthRegion } from "../config";
import { MultiTokenManager } from "../multi-token-manager";
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

function createManagerFixture(
	credentials: KiroCredentials[],
	config: KiroConfig = BASE_CONFIG,
): { manager: MultiTokenManager; credentialsPath: string } {
	const dir = mkdtempSync(join(tmpdir(), "narrafork-kiro-regions-"));
	tempDirs.push(dir);
	const credentialsPath = join(dir, "credentials.json");
	writeFileSync(credentialsPath, JSON.stringify(credentials, null, 2));
	writeFileSync(join(dir, "kiro_usage.json"), "{}");
	return {
		manager: new MultiTokenManager(config, credentials, credentialsPath, true),
		credentialsPath,
	};
}

function readPersisted(credentialsPath: string): KiroCredentials[] {
	return JSON.parse(readFileSync(credentialsPath, "utf-8"));
}

function socialCredential(id: string, overrides: Partial<KiroCredentials> = {}): KiroCredentials {
	return {
		id,
		authMethod: "social",
		refreshToken: `refresh-token-${id}-0123456789`,
		accessToken: `access-${id}`,
		expiresAt: new Date(Date.now() + 60 * 60_000).toISOString(),
		...overrides,
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

describe("region resolution", () => {
	test("auth region prefers credential overrides over config", () => {
		const config: KiroConfig = { ...BASE_CONFIG, region: "us-east-1", authRegion: "eu-west-1" };
		// cred.authRegion wins over everything
		expect(effectiveAuthRegion({ authRegion: "ap-northeast-1", region: "us-west-2" }, config)).toBe(
			"ap-northeast-1",
		);
		// cred.region is the next fallback, ahead of config.authRegion
		expect(effectiveAuthRegion({ region: "us-west-2" }, config)).toBe("us-west-2");
		// then config.authRegion, then config.region
		expect(effectiveAuthRegion({}, config)).toBe("eu-west-1");
		expect(effectiveAuthRegion({}, { ...BASE_CONFIG, region: "us-west-2" })).toBe("us-west-2");
	});

	test("api region honors the credential override", () => {
		const config: KiroConfig = { ...BASE_CONFIG, region: "us-east-1", apiRegion: "eu-west-1" };
		expect(effectiveApiRegion(config, { apiRegion: "ap-northeast-1" })).toBe("ap-northeast-1");
		// Without a credential override, config.apiRegion applies
		expect(effectiveApiRegion(config, {})).toBe("eu-west-1");
		expect(effectiveApiRegion(config)).toBe("eu-west-1");
		expect(effectiveApiRegion({ ...BASE_CONFIG, region: "us-west-2" }, {})).toBe("us-west-2");
	});

	test("credential region does not leak into the data plane", () => {
		// cred.region is the sign-in region; the data plane must not follow it, or a
		// credential registered in one region would stop serving from another.
		const config: KiroConfig = { ...BASE_CONFIG, region: "us-east-1" };
		expect(effectiveApiRegion(config, { region: "eu-west-1" })).toBe("us-east-1");
	});

	test("blank and whitespace-only values fall through to the default", () => {
		const blankConfig: KiroConfig = {
			...BASE_CONFIG,
			region: "  ",
			authRegion: "",
			apiRegion: " ",
		};
		expect(effectiveApiRegion(blankConfig, { apiRegion: "  " })).toBe(DEFAULT_KIRO_REGION);
		expect(effectiveAuthRegion({ authRegion: " ", region: "" }, blankConfig)).toBe(
			DEFAULT_KIRO_REGION,
		);
	});

	test("values are trimmed so they cannot corrupt the endpoint host", () => {
		expect(effectiveApiRegion(BASE_CONFIG, { apiRegion: " eu-west-1 " })).toBe("eu-west-1");
		expect(effectiveAuthRegion({ authRegion: " eu-west-1 " }, BASE_CONFIG)).toBe("eu-west-1");
	});
});

describe("updateCredential", () => {
	test("patches text fields and persists them", () => {
		const { manager, credentialsPath } = createManagerFixture([socialCredential("a")]);
		manager.updateCredential("a", {
			displayName: "Team Account",
			email: "team@example.com",
			region: "us-west-2",
			authRegion: "eu-west-1",
			apiRegion: "ap-northeast-1",
			priority: 5,
		});

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.displayName).toBe("Team Account");
		expect(persisted.email).toBe("team@example.com");
		expect(persisted.region).toBe("us-west-2");
		expect(persisted.authRegion).toBe("eu-west-1");
		expect(persisted.apiRegion).toBe("ap-northeast-1");
		expect(persisted.priority).toBe(5);
	});

	test("an empty string clears an optional field", () => {
		const { manager, credentialsPath } = createManagerFixture([
			socialCredential("a", { apiRegion: "eu-west-1", displayName: "old" }),
		]);
		manager.updateCredential("a", { apiRegion: "", displayName: "" });

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.apiRegion).toBeUndefined();
		expect(persisted.displayName).toBeUndefined();
	});

	test("omitted fields are left untouched", () => {
		const { manager, credentialsPath } = createManagerFixture([
			socialCredential("a", { apiRegion: "eu-west-1", email: "keep@example.com" }),
		]);
		manager.updateCredential("a", { displayName: "renamed" });

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.apiRegion).toBe("eu-west-1");
		expect(persisted.email).toBe("keep@example.com");
		expect(persisted.refreshToken).toBe("refresh-token-a-0123456789");
	});

	test("an empty secret keeps the stored one", () => {
		const { manager, credentialsPath } = createManagerFixture([
			socialCredential("a", { clientId: "cid", clientSecret: "secret" }),
		]);
		manager.updateCredential("a", { refreshToken: "", clientSecret: "", proxyPassword: "" });

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.refreshToken).toBe("refresh-token-a-0123456789");
		expect(persisted.clientSecret).toBe("secret");
	});

	test("replacing the refresh token drops the cached access token", () => {
		const { manager, credentialsPath } = createManagerFixture([socialCredential("a")]);
		manager.updateCredential("a", { refreshToken: "new-refresh-token-0123456789" });

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.refreshToken).toBe("new-refresh-token-0123456789");
		// A stale access token would keep being sent until it expired.
		expect(persisted.accessToken).toBeUndefined();
		expect(persisted.expiresAt).toBeUndefined();
	});

	test("switching to api_key requires a key and mirrors it onto accessToken", () => {
		const { manager, credentialsPath } = createManagerFixture([socialCredential("a")]);
		expect(() => manager.updateCredential("a", { authMethod: "api_key" })).toThrow(
			/apiKey is required/,
		);

		manager.updateCredential("a", { authMethod: "api_key", apiKey: "ksk-new" });
		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.authMethod).toBe("api_key");
		expect(persisted.apiKey).toBe("ksk-new");
		expect(persisted.accessToken).toBe("ksk-new");
		// Static keys never expire.
		expect(persisted.expiresAt).toBeUndefined();
	});

	test("supplying only an api key implies api_key auth", () => {
		const { manager, credentialsPath } = createManagerFixture([socialCredential("a")]);
		manager.updateCredential("a", { apiKey: "ksk-inferred" });

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.authMethod).toBe("api_key");
		expect(persisted.apiKey).toBe("ksk-inferred");
	});

	test("leaving api_key requires a refresh token and clears the stale key", () => {
		const { manager, credentialsPath } = createManagerFixture([
			{ id: "a", authMethod: "api_key", apiKey: "ksk-a", accessToken: "ksk-a" },
		]);
		expect(() => manager.updateCredential("a", { authMethod: "social" })).toThrow(
			/refreshToken is required/,
		);

		manager.updateCredential("a", {
			authMethod: "social",
			refreshToken: "refresh-token-new-0123456789",
		});
		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.authMethod).toBe("social");
		expect(persisted.refreshToken).toBe("refresh-token-new-0123456789");
		expect(persisted.apiKey).toBeUndefined();
	});

	test("rejects a secret already used by another credential", () => {
		const { manager } = createManagerFixture([socialCredential("a"), socialCredential("b")]);
		expect(() =>
			manager.updateCredential("a", { refreshToken: "refresh-token-b-0123456789" }),
		).toThrow(/already used by b/);
	});

	test("a rejected patch leaves the credential unchanged", () => {
		const { manager, credentialsPath } = createManagerFixture([
			socialCredential("a"),
			socialCredential("b"),
		]);
		expect(() =>
			manager.updateCredential("a", {
				displayName: "should not stick",
				refreshToken: "refresh-token-b-0123456789",
			}),
		).toThrow(/already used by b/);

		const [persisted] = readPersisted(credentialsPath);
		expect(persisted.displayName).toBeUndefined();
		expect(persisted.refreshToken).toBe("refresh-token-a-0123456789");
		expect(manager.snapshot().entries[0].displayName).toBeUndefined();
	});

	test("a new secret re-derives a machineId that came from the old one", () => {
		const { manager, credentialsPath } = createManagerFixture([socialCredential("a")]);
		const derived = readPersisted(credentialsPath)[0].machineId;
		// Constructed credentials carry no machineId; the manager derives one lazily,
		// so seed it the way addCredentials would before checking the re-derivation.
		manager.updateCredential("a", {});
		const seeded = readPersisted(credentialsPath)[0].machineId;
		expect(derived ?? seeded).toBeTruthy();

		manager.updateCredential("a", { refreshToken: "refresh-token-rotated-0123456789" });
		const rotated = readPersisted(credentialsPath)[0].machineId;
		expect(rotated).toBeTruthy();
		expect(rotated).not.toBe(seeded);
	});

	test("an explicit machineId survives a secret rotation", () => {
		const explicitId = "a".repeat(64);
		const { manager, credentialsPath } = createManagerFixture([
			socialCredential("a", { machineId: explicitId }),
		]);
		manager.updateCredential("a", {
			machineId: explicitId,
			refreshToken: "refresh-token-rotated-0123456789",
		});
		expect(readPersisted(credentialsPath)[0].machineId).toBe(explicitId);
	});

	test("throws for an unknown credential id", () => {
		const { manager } = createManagerFixture([socialCredential("a")]);
		expect(() => manager.updateCredential("missing", { displayName: "x" })).toThrow(
			/Credential not found/,
		);
	});

	test("snapshot exposes region overrides and secret presence without values", () => {
		const { manager } = createManagerFixture([
			socialCredential("a", {
				region: "us-west-2",
				authRegion: "eu-west-1",
				apiRegion: "ap-northeast-1",
				clientId: "cid",
				clientSecret: "secret",
				proxyUrl: "http://127.0.0.1:7890",
				proxyUsername: "user",
				proxyPassword: "pass",
			}),
		]);
		const [entry] = manager.snapshot().entries;
		expect(entry.region).toBe("us-west-2");
		expect(entry.authRegion).toBe("eu-west-1");
		expect(entry.apiRegion).toBe("ap-northeast-1");
		expect(entry.hasClientId).toBe(true);
		expect(entry.hasClientSecret).toBe(true);
		expect(entry.hasProxyPassword).toBe(true);
		expect(entry.proxyUsername).toBe("user");
		// Secret values must never reach the client.
		expect(JSON.stringify(entry)).not.toContain("secret");
		expect(JSON.stringify(entry)).not.toContain("pass");
	});
});
