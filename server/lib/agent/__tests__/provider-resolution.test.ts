import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { settings } from "../../settings";

class MockProvider {}

mock.module("../anthropic-provider", () => ({ AnthropicProvider: MockProvider }));
mock.module("../cline-provider", () => ({ ClineProvider: MockProvider }));
mock.module("../codex-provider", () => ({ CodexProvider: MockProvider }));
mock.module("../kiro-provider", () => ({ KiroProvider: MockProvider }));
mock.module("../nkp-provider", () => ({ NkpProvider: MockProvider }));
mock.module("../nug-provider", () => ({ NugProvider: MockProvider }));
mock.module("../openai-provider", () => ({ OpenAIProvider: MockProvider }));

const { resolveProviderAndModel } = await import("../provider");

describe("resolveProviderAndModel", () => {
	let originalDisabledProviders: string[] | undefined;
	let originalDisableKiroEnv: string | undefined;

	beforeEach(() => {
		originalDisabledProviders = settings.agent?.disabledProviders;
		originalDisableKiroEnv = process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		if (settings.agent) settings.agent.disabledProviders = [];
		delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
	});

	afterEach(() => {
		if (settings.agent) settings.agent.disabledProviders = originalDisabledProviders;
		if (originalDisableKiroEnv === undefined) delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		else process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = originalDisableKiroEnv;
	});

	test("rejects kiro models when the provider is disabled in settings", () => {
		if (settings.agent) settings.agent.disabledProviders = ["kiro"];

		expect(() => resolveProviderAndModel("kiro:claude-sonnet-4.6")).toThrow(
			/Kiro provider is disabled/,
		);
	});
});
