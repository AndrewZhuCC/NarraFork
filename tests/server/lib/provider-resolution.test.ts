import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { getProvider, resolveProviderAndModel } from "../../../server/lib/agent/provider";
import { __setCodexManagerForTests, CodexManager } from "../../../server/lib/codex-manager";
import { resolveProvider, settings } from "../../../server/lib/settings";

const originalEnvDisableKiroProvider = process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
const settingsKeys = Object.keys(settings) as Array<keyof typeof settings>;
const tempDirs: string[] = [];

function cloneSettingsSnapshot() {
	return structuredClone(settings);
}

function restoreFromSnapshot(snapshot: ReturnType<typeof cloneSettingsSnapshot>): void {
	for (const key of settingsKeys) {
		// biome-ignore lint/suspicious/noExplicitAny: generic key/value restoration in test helper
		(settings as any)[key] = snapshot[key];
	}
}

function resetProviders(): void {
	settings.openaiProviders = [];
	settings.anthropicProviders = [];
	settings.nkpProviders = [];
	settings.codex = undefined;
	settings.kiro = undefined;
	settings.agent.customModels = [];
}

function createTempCodexManagerWithCredential(): CodexManager {
	const tempHome = mkdtempSync(join(tmpdir(), "narrafork-provider-resolution-"));
	tempDirs.push(tempHome);
	const credsDir = join(tempHome, ".narrafork");
	mkdirSync(credsDir, { recursive: true });
	writeFileSync(
		join(credsDir, "codex-credentials.json"),
		JSON.stringify(
			[
				{
					id: "cred-test",
					refreshToken: "test-refresh-token",
					priority: 0,
					disabled: false,
				},
			],
			null,
			2,
		),
	);
	return new CodexManager({ homeDir: tempHome, registerProcessHooks: false });
}

function addOpenaiProvider(prefix: string): void {
	settings.openaiProviders = [
		{
			id: `${prefix}-id`,
			name: `${prefix}-name`,
			prefix,
			apiKey: "test-key",
			baseUrl: "https://api.openai.com/v1",
			defaultModel: "gpt-4o",
		},
	];
}

function addAnthropicProvider(prefix: string): void {
	settings.anthropicProviders = [
		{
			id: `${prefix}-id`,
			name: `${prefix}-name`,
			prefix,
			apiKey: "anthropic-key",
			baseUrl: "https://api.anthropic.com/v1",
			defaultModel: "claude-sonnet-4-20250514",
		},
	];
}

describe("resolveProvider fallback order", () => {
	let snapshot: ReturnType<typeof cloneSettingsSnapshot>;

	beforeEach(() => {
		snapshot = cloneSettingsSnapshot();
		resetProviders();
		delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		__setCodexManagerForTests(undefined);
	});
	afterEach(() => {
		restoreFromSnapshot(snapshot);
		if (originalEnvDisableKiroProvider === undefined) {
			delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		} else {
			process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = originalEnvDisableKiroProvider;
		}
		__setCodexManagerForTests(undefined);
		for (const dir of tempDirs.splice(0)) {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	test("unknown model 优先 fallback 到已配置 openai provider", () => {
		addOpenaiProvider("deepseek");
		const provider = resolveProvider("unknown-model");
		expect(provider).toBe("deepseek");
	});

	test("unknown model 无 openai 时 fallback 到 anthropic provider", () => {
		addAnthropicProvider("anthropic-cn");
		const provider = resolveProvider("unknown-model");
		expect(provider).toBe("anthropic-cn");
	});

	test("unknown model 无 openai/anthropic 但有 codex 可用凭据时 fallback 到 codex", () => {
		settings.codex = {};
		const manager = createTempCodexManagerWithCredential();
		__setCodexManagerForTests(manager);
		const provider = resolveProvider("unknown-model");
		expect(provider).toBe("codex");
	});

	test("unknown model 无其他 provider 但有 kiro 配置时 fallback 到 kiro", () => {
		settings.kiro = {
			credentialsPath: "/tmp/kiro-creds.json",
			configPath: "/tmp/kiro-config.json",
		};
		const provider = resolveProvider("unknown-model");
		expect(provider).toBe("kiro");
	});

	test("无任何 provider 配置时保持 legacy fallback 到 kiro", () => {
		const provider = resolveProvider("unknown-model");
		expect(provider).toBe("kiro");
	});

	test("explicit provider prefix 保持优先", () => {
		addOpenaiProvider("deepseek");
		const provider = resolveProvider("anthropic:gpt-4o");
		expect(provider).toBe("anthropic");
	});

	test("builtin codex model 仍解析为 codex", () => {
		const provider = resolveProvider("gpt-5.1-codex");
		expect(provider).toBe("codex");
	});

	test("builtin kiro model 仍解析为 kiro", () => {
		const provider = resolveProvider("claude-sonnet");
		expect(provider).toBe("kiro");
	});
});

describe("getProvider fallback behavior", () => {
	let snapshot: ReturnType<typeof cloneSettingsSnapshot>;

	beforeEach(() => {
		snapshot = cloneSettingsSnapshot();
		resetProviders();
		delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		__setCodexManagerForTests(undefined);
	});
	afterEach(() => {
		restoreFromSnapshot(snapshot);
		if (originalEnvDisableKiroProvider === undefined) {
			delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		} else {
			process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = originalEnvDisableKiroProvider;
		}
		__setCodexManagerForTests(undefined);
		for (const dir of tempDirs.splice(0)) {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	test("禁用 kiro provider 时直接报错", () => {
		addOpenaiProvider("deepseek");
		process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = "1";
		expect(() => getProvider("kiro")).toThrow(/Kiro provider is disabled/);
	});

	test("禁用 kiro provider 且无其他 provider 时抛错", () => {
		process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = "1";
		expect(() => getProvider("kiro")).toThrow(/Kiro provider is disabled/);
	});

	test("unknown provider 直接报错而非静默回退", () => {
		addOpenaiProvider("deepseek");
		expect(() => getProvider("unknown-provider")).toThrow(
			/Provider "unknown-provider" is not configured/,
		);
	});

	test("unknown provider 且 kiro 被禁用时抛错", () => {
		process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = "1";
		expect(() => getProvider("unknown-provider")).toThrow(
			/Provider "unknown-provider" is not configured/,
		);
	});

	test("显式 openai provider 返回 OpenAIProvider", () => {
		addOpenaiProvider("deepseek");
		const provider = getProvider("deepseek");
		expect(provider.constructor.name).toBe("OpenAIProvider");
	});
});

describe("resolveProviderAndModel behavior", () => {
	let snapshot: ReturnType<typeof cloneSettingsSnapshot>;

	beforeEach(() => {
		snapshot = cloneSettingsSnapshot();
		resetProviders();
		delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		__setCodexManagerForTests(undefined);
	});
	afterEach(() => {
		restoreFromSnapshot(snapshot);
		if (originalEnvDisableKiroProvider === undefined) {
			delete process.env.NARRAFORK_DISABLE_KIRO_PROVIDER;
		} else {
			process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = originalEnvDisableKiroProvider;
		}
		__setCodexManagerForTests(undefined);
		for (const dir of tempDirs.splice(0)) {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	test("禁用 kiro provider 时直接报错而非静默回退", () => {
		addOpenaiProvider("deepseek");
		process.env.NARRAFORK_DISABLE_KIRO_PROVIDER = "1";
		expect(() => resolveProviderAndModel("kiro:claude-sonnet-4.5")).toThrow(
			/Kiro provider is disabled/,
		);
	});

	test("provider 未变更时保留原模型", () => {
		addOpenaiProvider("deepseek");
		const resolved = resolveProviderAndModel("deepseek:deepseek-chat");
		expect(resolved.provider).toBe("deepseek");
		expect(resolved.model).toBe("deepseek:deepseek-chat");
	});
});
