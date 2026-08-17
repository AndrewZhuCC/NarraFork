import { describe, expect, test } from "bun:test";
import { initialProvidersState, providersReducer } from "./providers-reducer";

/**
 * Round-trip of the NUG per-provider switches through the reducer.
 *
 * The reducer maps NUG providers field by field rather than spreading them, so a
 * field it does not list is dropped when settings are loaded back. The control
 * then renders from its default and the user sees the toggle snap back — looking
 * like the save failed, while the value was in fact saved and then discarded on
 * read. That is exactly what happened to `kiroAnthropicProtocol`, so the field is
 * pinned here.
 */

function hydrate(settings: Record<string, unknown>) {
	return providersReducer(initialProvidersState, { type: "INIT_FROM_SETTINGS", settings });
}

describe("nug provider switch round-trip", () => {
	test("kiroAnthropicProtocol survives loading settings", () => {
		const state = hydrate({
			nugProviders: [
				{
					id: "n1",
					name: "GW",
					prefix: "nug",
					apiKey: "k",
					baseUrl: "https://gw.example",
					defaultModel: "",
					kiroAnthropicProtocol: true,
				},
			],
		});

		expect(state.nugProviders[0].kiroAnthropicProtocol).toBe(true);
	});

	test("an absent switch stays absent rather than becoming true", () => {
		// Default-off matters on upgrade: an existing config has no such field, and
		// hydrating it as enabled would silently switch protocols for everyone.
		const state = hydrate({
			nugProviders: [
				{ id: "n1", name: "GW", prefix: "nug", apiKey: "k", baseUrl: "u", defaultModel: "" },
			],
		});

		expect(state.nugProviders[0].kiroAnthropicProtocol).toBeFalsy();
	});

	test("an explicit false survives too", () => {
		const state = hydrate({
			nugProviders: [
				{
					id: "n1",
					name: "GW",
					prefix: "nug",
					apiKey: "k",
					baseUrl: "u",
					defaultModel: "",
					kiroAnthropicProtocol: false,
				},
			],
		});

		expect(state.nugProviders[0].kiroAnthropicProtocol).toBe(false);
	});

	test("SYNC_FROM_SETTINGS also preserves it", () => {
		// Both hydration paths go through the same mapping; asserting only INIT would
		// let a divergence between the two through.
		const state = providersReducer(initialProvidersState, {
			type: "SYNC_FROM_SETTINGS",
			settings: {
				nugProviders: [
					{
						id: "n1",
						name: "GW",
						prefix: "nug",
						apiKey: "k",
						baseUrl: "u",
						defaultModel: "",
						kiroAnthropicProtocol: true,
					},
				],
			},
		});

		expect(state.nugProviders[0].kiroAnthropicProtocol).toBe(true);
	});

	test("toggling then re-hydrating keeps the new value", () => {
		// The full user-visible cycle: flip the switch, persist, load back.
		let state = hydrate({
			nugProviders: [
				{ id: "n1", name: "GW", prefix: "nug", apiKey: "k", baseUrl: "u", defaultModel: "" },
			],
		});

		state = providersReducer(state, {
			type: "SET_NUG_PROVIDERS",
			providers: (prev) => prev.map((p) => ({ ...p, kiroAnthropicProtocol: true })),
		});
		expect(state.nugProviders[0].kiroAnthropicProtocol).toBe(true);

		const reloaded = hydrate({ nugProviders: state.nugProviders });
		expect(reloaded.nugProviders[0].kiroAnthropicProtocol).toBe(true);
	});
});
