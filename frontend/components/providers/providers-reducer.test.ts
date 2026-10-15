import { describe, expect, test } from "bun:test";
import {
	initialProvidersState,
	providersReducer,
	providersStateFromSettings,
} from "./providers-reducer";

const provider = {
	id: "provider-id",
	name: "Gemini",
	prefix: "gemini-old",
	apiKey: "****abcd",
	baseUrl: "https://example.com/v1beta",
	defaultModel: "gemini-test",
	protocol: "gemini-compatible" as const,
	codexAccountId: "",
};

describe("providers reducer context window merging", () => {
	test("accepts inclusive bounds and preserves valid values outside the visible model list", () => {
		const next = providersReducer(initialProvidersState, {
			type: "MERGE_CONTEXT_WINDOWS",
			windows: { "unlisted:minimum": 256, "unlisted:maximum": 10_000_000 },
		});
		expect(next.modelContextWindows).toEqual({
			"unlisted:minimum": 256,
			"unlisted:maximum": 10_000_000,
		});
		expect(initialProvidersState.modelContextWindows).toEqual({});
	});

	test.each([
		0,
		-1,
		255,
		256.5,
		10_000_001,
		Number.NaN,
		Infinity,
		-Infinity,
	])("ignores invalid automatic context window %s without changing state identity", (value) => {
		const next = providersReducer(initialProvidersState, {
			type: "MERGE_CONTEXT_WINDOWS",
			windows: { "unlisted:model": value },
		});
		expect(next).toBe(initialProvidersState);
	});

	test("merges valid entries while ignoring invalid entries in the same response", () => {
		const next = providersReducer(initialProvidersState, {
			type: "MERGE_CONTEXT_WINDOWS",
			windows: { "unlisted:valid": 128_000, "unlisted:invalid": 100 },
		});
		expect(next.modelContextWindows).toEqual({ "unlisted:valid": 128_000 });
	});

	test.each([
		100, 256.5, 128_000, 10_000_001,
	])("preserves manual value %s until save-time normalization rather than overwriting it", (size) => {
		const edited = providersReducer(initialProvidersState, {
			type: "SET_CONTEXT_WINDOW",
			modelVal: "unlisted:model",
			size,
		});
		expect(edited.modelContextWindows).toEqual({ "unlisted:model": size });
		const merged = providersReducer(edited, {
			type: "MERGE_CONTEXT_WINDOWS",
			windows: { "unlisted:model": 256_000, "unlisted:other": 64_000 },
		});
		expect(merged.modelContextWindows).toEqual({
			"unlisted:model": size,
			"unlisted:other": 64_000,
		});
		expect(edited.modelContextWindows).toEqual({ "unlisted:model": size });
	});
});

describe("providers reducer server synchronization", () => {
	test("SYNC_FROM_SETTINGS replaces local state with the server-final normalized response", () => {
		const local = providersStateFromSettings({
			customApiProviders: [provider],
			agent: { modelContextWindows: { "gemini-old:gemini-test": 100 } },
		});
		const serverResponse = {
			customApiProviders: [{ ...provider, prefix: "gemini-new", apiKey: "****wxyz" }],
			agent: {
				modelContextWindows: { "gemini-new:gemini-test": 1_000_000 },
				providerOrder: ["gemini-new"],
				disabledProviders: [],
			},
		};

		const synced = providersReducer(local, {
			type: "SYNC_FROM_SETTINGS",
			settings: serverResponse,
		});
		expect(synced.customApiProviders[0]?.prefix).toBe("gemini-new");
		expect(synced.customApiProviders[0]?.apiKey).toBe("****wxyz");
		expect(synced.modelContextWindows).toEqual({ "gemini-new:gemini-test": 1_000_000 });
		expect(synced.providerOrder).toEqual(["gemini-new"]);
	});

	test("normalizes missing Gemini transport and preserves explicit legacy split transport", () => {
		const unified = providersStateFromSettings({ customApiProviders: [provider], agent: {} });
		expect(unified.customApiProviders[0]?.geminiTransport).toBe("generate-content");

		const legacy = providersStateFromSettings({
			geminiProviders: [{ ...provider, protocol: undefined, geminiTransport: "interactions" }],
			agent: {},
		});
		expect(legacy.customApiProviders[0]).toMatchObject({
			protocol: "gemini-compatible",
			geminiTransport: "interactions",
		});
	});

	test("provider disable toggles keep detail state and overview state aligned", () => {
		const initialized = providersStateFromSettings({ customApiProviders: [provider], agent: {} });
		const disabled = providersReducer(initialized, {
			type: "TOGGLE_PROVIDER_DISABLED",
			prefix: "gemini-old",
		});
		expect(disabled.customApiProviders[0]?.disabled).toBe(true);
		expect(disabled.disabledProviders.has("gemini-old")).toBe(true);

		const enabled = providersReducer(disabled, {
			type: "TOGGLE_PROVIDER_DISABLED",
			prefix: "gemini-old",
		});
		expect(enabled.customApiProviders[0]?.disabled).toBe(false);
		expect(enabled.disabledProviders.has("gemini-old")).toBe(false);
	});

	test("ignores empty-prefix global toggles so unsaved providers stay local", () => {
		const initialized = providersStateFromSettings({
			customApiProviders: [{ ...provider, prefix: "" }],
			agent: {},
		});
		const next = providersReducer(initialized, {
			type: "TOGGLE_PROVIDER_DISABLED",
			prefix: "",
		});
		expect(next).toBe(initialized);
		expect(next.disabledProviders.has("")).toBe(false);
	});

	test("empty settings still produce an initialized state", () => {
		const state = providersReducer(initialProvidersState, {
			type: "SYNC_FROM_SETTINGS",
			settings: {},
		});
		expect(state.initialized).toBe(true);
	});
});
