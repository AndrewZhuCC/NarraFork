import { describe, expect, test } from "bun:test";
import { MAX_COMMAND_SECRET_VALUE_BYTES } from "@server/lib/plugins/protocol";
import {
	InvalidBundleError,
	MissingCredentialError,
	parseBundle,
	readConfig,
	serializeBundle,
	toCredentials,
} from "../../../../examples/plugins/kiro-external/src/credentials";

/**
 * Credential parsing for the plugin.
 *
 * Two properties carry real risk:
 *
 *  1. **Backward compatibility.** The single-credential field layout was already verified
 *     working end to end against a live account. Introducing the bundle must not strand a
 *     provider that is configured the old way.
 *  2. **Never silently losing credentials.** A malformed bundle must fail loudly. Degrading
 *     to "no credentials" would present a configured provider as unconfigured and send the
 *     user to re-enter credentials that are, in fact, still stored.
 */

const SINGLE = {
	authMethod: "social",
	refreshToken: "rt-single",
	region: "us-west-2",
	profileArn: "arn:aws:codewhisperer:::profile/ABC",
};

describe("kiro-external credentials: bundle", () => {
	test("parses a multi-credential bundle in order", () => {
		const credentials = toCredentials(
			readConfig({
				credentialsBundle: JSON.stringify([
					{ id: "a", authMethod: "social", refreshToken: "rt-a", priority: 0 },
					{ id: "b", authMethod: "social", refreshToken: "rt-b", priority: 1 },
				]),
			}),
		);
		expect(credentials.map((item) => item.id)).toEqual(["a", "b"]);
		expect(credentials[1].priority).toBe(1);
	});

	test("accepts a bare object as a one-entry bundle", () => {
		const credentials = parseBundle(JSON.stringify({ authMethod: "social", refreshToken: "rt" }));
		expect(credentials).toHaveLength(1);
	});

	test("carries per-credential state through", () => {
		const [credential] = parseBundle(
			JSON.stringify([
				{
					authMethod: "social",
					refreshToken: "rt",
					disabled: true,
					disabledReason: "quota_exhausted",
					meterUsage: 12.5,
					availableModels: ["claude-sonnet-4.5", 42],
					email: "user@example.com",
				},
			]),
		);
		expect(credential.disabled).toBe(true);
		expect(credential.disabledReason).toBe("quota_exhausted");
		expect(credential.meterUsage).toBe(12.5);
		// Non-string entries are dropped rather than trusted into a typed array.
		expect(credential.availableModels).toEqual(["claude-sonnet-4.5"]);
		expect(credential.email).toBe("user@example.com");
	});

	test("drops unknown fields instead of forwarding them upstream", () => {
		const [credential] = parseBundle(
			JSON.stringify([{ authMethod: "social", refreshToken: "rt", injected: "evil" }]),
		);
		expect("injected" in credential).toBe(false);
	});

	test("skips entries with no usable secret but keeps the rest", () => {
		const credentials = parseBundle(
			JSON.stringify([
				{ id: "empty", authMethod: "social" },
				{ id: "good", authMethod: "social", refreshToken: "rt" },
			]),
		);
		expect(credentials.map((item) => item.id)).toEqual(["good"]);
	});

	test("requires the client pair for idc entries", () => {
		const credentials = parseBundle(
			JSON.stringify([
				{ id: "partial", authMethod: "idc", refreshToken: "rt" },
				{ id: "full", authMethod: "idc", refreshToken: "rt", clientId: "c", clientSecret: "s" },
			]),
		);
		expect(credentials.map((item) => item.id)).toEqual(["full"]);
	});

	test("accepts an api_key entry with no refresh token", () => {
		const credentials = parseBundle(JSON.stringify([{ authMethod: "api_key", apiKey: "ksk_x" }]));
		expect(credentials).toHaveLength(1);
	});

	test("round-trips through serializeBundle", () => {
		const original = parseBundle(
			JSON.stringify([{ id: "a", authMethod: "social", refreshToken: "rt-a" }]),
		);
		expect(parseBundle(serializeBundle(original))).toEqual(original);
	});
});

describe("kiro-external credentials: failure reporting", () => {
	test("invalid JSON is reported, not swallowed", () => {
		// Silently treating this as "unconfigured" would tell the user their credentials are
		// gone when they are merely unreadable.
		expect(() => parseBundle("{not json")).toThrow(InvalidBundleError);
	});

	test("a bundle with no usable entry is reported", () => {
		expect(() => parseBundle(JSON.stringify([{ authMethod: "social" }]))).toThrow(
			MissingCredentialError,
		);
	});

	test("an empty array is reported rather than treated as configured", () => {
		expect(() => parseBundle("[]")).toThrow(MissingCredentialError);
	});

	test("a bundle at the size ceiling still parses", () => {
		// The ceiling is a real constraint of storing the list as one secret value, so the
		// boundary is asserted rather than assumed.
		const filler = "x".repeat(1_000);
		const entries = Array.from({ length: 50 }, (_, index) => ({
			id: `c${index}`,
			authMethod: "social",
			refreshToken: `${filler}-${index}`,
		}));
		const bundle = JSON.stringify(entries);
		expect(Buffer.byteLength(bundle, "utf8")).toBeLessThan(MAX_COMMAND_SECRET_VALUE_BYTES);
		expect(parseBundle(bundle)).toHaveLength(50);
	});
});

describe("kiro-external credentials: legacy compatibility", () => {
	test("still accepts the single-credential field layout", () => {
		// This shape was verified working against a live account before the bundle existed;
		// breaking it would strand an already-configured provider.
		const credentials = toCredentials(readConfig(SINGLE));
		expect(credentials).toHaveLength(1);
		expect(credentials[0].refreshToken).toBe("rt-single");
		expect(credentials[0].region).toBe("us-west-2");
		expect(credentials[0].profileArn).toBe(SINGLE.profileArn);
	});

	test("the bundle takes precedence when both are present", () => {
		const credentials = toCredentials(
			readConfig({
				...SINGLE,
				credentialsBundle: JSON.stringify([
					{ id: "from-bundle", authMethod: "social", refreshToken: "rt-bundle" },
				]),
			}),
		);
		expect(credentials.map((item) => item.id)).toEqual(["from-bundle"]);
	});

	test("reports what is missing for the legacy layout", () => {
		expect(() => toCredentials(readConfig({ authMethod: "idc", refreshToken: "rt" }))).toThrow(
			/clientId, clientSecret/,
		);
	});

	test("an absent config reports a missing refresh token", () => {
		expect(() => toCredentials(readConfig({}))).toThrow(MissingCredentialError);
	});

	test("ignores blank strings so a cleared field is genuinely absent", () => {
		const config = readConfig({ refreshToken: "   ", credentialsBundle: "" });
		expect(config.refreshToken).toBeUndefined();
		expect(config.credentialsBundle).toBeUndefined();
	});
});
