import { afterAll, afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import { settings } from "../../settings";

class MockProvider {}

// Snapshot the real provider modules before mocking. Bun's mock.module is
// process-wide and mock.restore() does NOT undo it, so afterAll re-points each
// specifier back — otherwise these MockProvider stubs leak into later suites
// that need the real provider classes (e.g. anthropic-v1-fallback).
const realProviderModules: Record<string, () => unknown> = {
	"../anthropic-provider": () => realAnthropic,
	"../cline-provider": () => realCline,
	"../codex-provider": () => realCodex,
	"../kiro-provider": () => realKiro,
	"../nug-provider": () => realNug,
	"../openai-provider": () => realOpenai,
};
const realAnthropic = { ...(await import("../anthropic-provider")) };
const realCline = { ...(await import("../cline-provider")) };
const realCodex = { ...(await import("../codex-provider")) };
const realKiro = { ...(await import("../kiro-provider")) };
const realNug = { ...(await import("../nug-provider")) };
const realOpenai = { ...(await import("../openai-provider")) };

mock.module("../anthropic-provider", () => ({ AnthropicProvider: MockProvider }));
mock.module("../cline-provider", () => ({ ClineProvider: MockProvider }));
mock.module("../codex-provider", () => ({ CodexProvider: MockProvider }));
mock.module("../kiro-provider", () => ({ KiroProvider: MockProvider }));
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

	afterAll(() => {
		for (const [specifier, factory] of Object.entries(realProviderModules)) {
			mock.module(specifier, factory);
		}
		mock.restore();
	});

	test("rejects kiro models when the provider is disabled in settings", () => {
		if (settings.agent) settings.agent.disabledProviders = ["kiro"];

		expect(() => resolveProviderAndModel("kiro:claude-sonnet-4.6")).toThrow(
			/Kiro provider is disabled/,
		);
	});
});
