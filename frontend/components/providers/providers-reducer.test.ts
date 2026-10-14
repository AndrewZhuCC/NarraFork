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

	test("Composer defaults off and survives unified, legacy and edited provider projection", () => {
		const defaults = providersStateFromSettings({ customApiProviders: [provider] });
		expect(defaults.customApiProviders[0]?.codexComposerEnabled).toBe(false);
		const configured = {
			...provider,
			protocol: "openai-responses" as const,
			codexComposerEnabled: true,
			codexComposerPrompt: "predict the next request",
		};
		const unified = providersStateFromSettings({ customApiProviders: [configured] });
		expect(unified.customApiProviders[0]).toMatchObject(configured);
		expect(unified.openaiProviders[0]).toMatchObject({
			codexComposerEnabled: true,
			codexComposerPrompt: configured.codexComposerPrompt,
		});
		const legacy = providersStateFromSettings({
			openaiProviders: [{ ...configured, apiMode: "codex" }],
		});
		expect(legacy.customApiProviders[0]).toMatchObject(configured);
		const updated = providersReducer(unified, {
			type: "SET_CUSTOM_API_PROVIDERS",
			providers: [{ ...configured, codexComposerPrompt: "new instructions" }],
		});
		expect(updated.customApiProviders[0]?.codexComposerPrompt).toBe("new instructions");
		expect(updated.openaiProviders[0]?.codexComposerPrompt).toBe("new instructions");
	});

	test("empty settings still produce an initialized state", () => {
		const state = providersReducer(initialProvidersState, {
			type: "SYNC_FROM_SETTINGS",
			settings: {},
		});
		expect(state.initialized).toBe(true);
	});
});
