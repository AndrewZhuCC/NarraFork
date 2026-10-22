import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import {
	anthropicBaseWithVersion,
	collectHarnessProviders,
	type LocalScanContext,
	parseJsonc,
	providerIdentity,
	scanClaude,
	scanCodex,
	scanDeepseekHarness,
	scanLocalProviderConfigs,
	scanOpencode,
	summarizeLocalScan,
} from "../local-provider-import";

const HOME = "/home/tester";

function memoryContext(
	files: Record<string, string>,
	env: Record<string, string> = {},
): LocalScanContext {
	const paths = Object.keys(files);
	const isDir = (path: string) => paths.some((file) => file.startsWith(`${path}/`));
	return {
		home: HOME,
		env,
		async readText(path) {
			return files[path] ?? null;
		},
		async listDirs(path) {
			const names = new Set<string>();
			for (const file of paths) {
				if (!file.startsWith(`${path}/`)) continue;
				const rest = file.slice(path.length + 1);
				const slash = rest.indexOf("/");
				if (slash > 0) names.add(rest.slice(0, slash));
			}
			return [...names];
		},
		async exists(path) {
			return path in files || isDir(path);
		},
	};
}

const h = (...parts: string[]) => join(HOME, ...parts);

describe("helpers", () => {
	test("anthropic bases gain /v1 once", () => {
		expect(anthropicBaseWithVersion("https://api.deepseek.com/anthropic")).toBe(
			"https://api.deepseek.com/anthropic/v1",
		);
		expect(anthropicBaseWithVersion("https://api.anthropic.com/v1/")).toBe(
			"https://api.anthropic.com/v1",
		);
	});

	test("parseJsonc keeps URLs inside strings and drops comments/trailing commas", () => {
		expect(
			parseJsonc(`{
				// comment
				"url": "https://example.com/v1", /* block */
				"list": [1, 2,],
			}`),
		).toEqual({ url: "https://example.com/v1", list: [1, 2] });
	});

	test("collectHarnessProviders finds providers in plugin lists and legacy maps", () => {
		const found = collectHarnessProviders({
			plugins: [
				{ id: "llm-pi-ai", config: { providers: { a: { baseURL: "https://a/v1" } } } },
				{ id: "agent-default-model", config: { provider: "a", model: "m" } },
			],
			"llm-pi-ai": { providers: { b: { baseURL: "https://b/v1" }, broken: { api: "x" } } },
		});
		expect(found.map(([id]) => id).sort()).toEqual(["a", "b"]);
	});
});

describe("Claude Code", () => {
	test("imports a third-party Anthropic gateway from settings env", async () => {
		const ctx = memoryContext({
			[h(".claude", "settings.json")]: JSON.stringify({
				env: {
					ANTHROPIC_BASE_URL: "https://open.bigmodel.cn/api/anthropic",
					ANTHROPIC_AUTH_TOKEN: "zhipu-key-1234",
					ANTHROPIC_MODEL: "glm-5",
				},
			}),
		});
		const scan = await scanClaude(ctx);
		expect(scan.found).toBe(true);
		expect(scan.candidates).toHaveLength(1);
		expect(scan.candidates[0]).toMatchObject({
			source: "claude",
			name: "Claude Code (open.bigmodel.cn)",
			protocol: "anthropic-messages",
			baseUrl: "https://open.bigmodel.cn/api/anthropic/v1",
			apiKey: "zhipu-key-1234",
			model: "glm-5",
		});
	});

	test("settings.local.json overrides settings.json", async () => {
		const ctx = memoryContext({
			[h(".claude", "settings.json")]: JSON.stringify({ env: { ANTHROPIC_API_KEY: "old" } }),
			[h(".claude", "settings.local.json")]: JSON.stringify({ env: { ANTHROPIC_API_KEY: "new" } }),
		});
		const scan = await scanClaude(ctx);
		expect(scan.candidates.map((c) => c.apiKey)).toEqual(["new"]);
		expect(scan.candidates[0]?.baseUrl).toBe("https://api.anthropic.com/v1");
		expect(scan.candidates[0]?.name).toBe("Anthropic");
	});

	test("subscription login is reported, never imported", async () => {
		const ctx = memoryContext({
			[h(".claude", ".credentials.json")]: JSON.stringify({ claudeAiOauth: { accessToken: "x" } }),
		});
		const scan = await scanClaude(ctx);
		expect(scan.candidates).toEqual([]);
		expect(scan.notes).toEqual([{ source: "claude", code: "subscription-login-skipped" }]);
	});

	test("reads primaryApiKey from .claude.json and honors CLAUDE_CONFIG_DIR", async () => {
		const ctx = memoryContext(
			{ "/cfg/claude/.claude.json": JSON.stringify({ primaryApiKey: "sk-ant-primary" }) },
			{ CLAUDE_CONFIG_DIR: "/cfg/claude" },
		);
		const scan = await scanClaude(ctx);
		expect(scan.candidates.map((c) => c.apiKey)).toEqual(["sk-ant-primary"]);
	});
});

describe("Codex", () => {
	test("custom providers resolve env_key and wire_api; ChatGPT tokens are skipped", async () => {
		const ctx = memoryContext(
			{
				[h(".codex", "config.toml")]: `
model = "deepseek-v4"
model_provider = "deepseek"

[model_providers.deepseek]
name = "DeepSeek"
base_url = "https://api.deepseek.com/v1"
env_key = "DEEPSEEK_API_KEY"
wire_api = "chat"

[model_providers.nokey]
base_url = "https://nokey.example/v1"
env_key = "UNSET_KEY"
`,
				[h(".codex", "auth.json")]: JSON.stringify({
					OPENAI_API_KEY: null,
					tokens: { id_token: "x" },
				}),
			},
			{ DEEPSEEK_API_KEY: "ds-key-abcd" },
		);
		const scan = await scanCodex(ctx);
		expect(scan.candidates).toHaveLength(1);
		expect(scan.candidates[0]).toMatchObject({
			name: "DeepSeek",
			protocol: "completions-compatible",
			baseUrl: "https://api.deepseek.com/v1",
			apiKey: "ds-key-abcd",
			model: "deepseek-v4",
		});
		expect(scan.notes).toEqual([{ source: "codex", code: "subscription-login-skipped" }]);
	});

	test("auth.json key is OpenAI unless a relay borrows it via requires_openai_auth", async () => {
		const plain = memoryContext({
			[h(".codex", "auth.json")]: JSON.stringify({ OPENAI_API_KEY: "sk-openai" }),
		});
		expect((await scanCodex(plain)).candidates).toMatchObject([
			{ name: "OpenAI", protocol: "openai-responses", baseUrl: "https://api.openai.com/v1" },
		]);

		const relay = memoryContext({
			[h(".codex", "auth.json")]: JSON.stringify({ OPENAI_API_KEY: "sk-relay" }),
			[h(".codex", "config.toml")]: `
model_provider = "relay"
[model_providers.relay]
name = "Relay"
base_url = "https://relay.example/v1"
requires_openai_auth = true
`,
		});
		const scan = await scanCodex(relay);
		expect(scan.candidates).toHaveLength(1);
		expect(scan.candidates[0]).toMatchObject({
			name: "Relay",
			protocol: "openai-responses",
			baseUrl: "https://relay.example/v1",
			apiKey: "sk-relay",
		});
	});
});

describe("OpenCode", () => {
	test("custom providers, env substitution, auth.json keys and OAuth notes", async () => {
		const ctx = memoryContext(
			{
				[h(".config", "opencode", "opencode.jsonc")]: `{
					// my providers
					"provider": {
						"myrelay": {
							"npm": "@ai-sdk/openai-compatible",
							"name": "My Relay",
							"options": { "baseURL": "https://relay.example/v1", "apiKey": "{env:RELAY_KEY}" },
						},
						"nobase": { "options": { "apiKey": "k" } },
					},
				}`,
				[h(".local", "share", "opencode", "auth.json")]: JSON.stringify({
					deepseek: { type: "api", key: "ds-from-auth" },
					anthropic: { type: "oauth", refresh: "r", access: "a", expires: 0 },
				}),
			},
			{ RELAY_KEY: "relay-secret" },
		);
		const scan = await scanOpencode(ctx);
		expect(scan.found).toBe(true);
		expect(
			scan.candidates.map(({ name, protocol, baseUrl, apiKey }) => ({
				name,
				protocol,
				baseUrl,
				apiKey,
			})),
		).toEqual([
			{
				name: "My Relay",
				protocol: "completions-compatible",
				baseUrl: "https://relay.example/v1",
				apiKey: "relay-secret",
			},
			{
				name: "DeepSeek",
				protocol: "completions-compatible",
				baseUrl: "https://api.deepseek.com/v1",
				apiKey: "ds-from-auth",
			},
		]);
		expect(scan.notes).toEqual([{ source: "opencode", code: "subscription-login-skipped" }]);
	});
});

describe("DeepSeek Harness", () => {
	test("profile cordis patches use apiKeyEnv from .credentials.yaml; env wins", async () => {
		const ctx = memoryContext(
			{
				[h(".dsh", ".credentials.yaml")]:
					"REQUESTY_API_KEY: stored-key\nDEEPSEEK_API_KEY: ds-key\n",
				[h(".dsh", "profiles", "default", "cordis.patch.yml")]: `
plugins:
  - id: agent-default-model
    config:
      provider: requesty
      model: deepseek/deepseek-v4-pro
  - id: llm-pi-ai
    config:
      providers:
        requesty:
          api: openai-completions
          baseURL: https://router.requesty.ai/v1
          apiKeyEnv: REQUESTY_API_KEY
          models:
            - id: deepseek/deepseek-v4-pro
        claude-like:
          api: anthropic-messages
          baseURL: https://gateway.example
          apiKeyEnv: GATEWAY_KEY
`,
			},
			{ GATEWAY_KEY: "gw-env-key" },
		);
		const scan = await scanDeepseekHarness(ctx);
		expect(
			scan.candidates.map(({ name, protocol, baseUrl, apiKey }) => ({
				name,
				protocol,
				baseUrl,
				apiKey,
			})),
		).toEqual([
			{
				name: "requesty",
				protocol: "completions-compatible",
				baseUrl: "https://router.requesty.ai/v1",
				apiKey: "stored-key",
			},
			{
				name: "claude-like",
				protocol: "anthropic-messages",
				baseUrl: "https://gateway.example/v1",
				apiKey: "gw-env-key",
			},
			{
				name: "DeepSeek",
				protocol: "completions-compatible",
				baseUrl: "https://api.deepseek.com/v1",
				apiKey: "ds-key",
			},
		]);
	});

	test("legacy settings.yaml top-level llm-pi-ai block is read; missing home is not found", async () => {
		const ctx = memoryContext(
			{
				[h(".dsh", "settings.yaml")]: `
agent-default-model:
  provider: bailian
  model: auto
llm-pi-ai:
  providers:
    bailian:
      api: openai-completions
      baseURL: https://dashscope.aliyuncs.com/compatible-mode/v1
      apiKeyEnv: BAILIAN_API_KEY
`,
			},
			{ BAILIAN_API_KEY: "bl-key" },
		);
		expect((await scanDeepseekHarness(ctx)).candidates.map((c) => c.apiKey)).toEqual(["bl-key"]);
		expect((await scanDeepseekHarness(memoryContext({}))).found).toBe(false);
	});
});

describe("scanLocalProviderConfigs", () => {
	test("reports every source, dedupes the same key across tools, and keeps ids stable", async () => {
		const files = {
			[h(".codex", "config.toml")]: `
[model_providers.deepseek]
base_url = "https://api.deepseek.com/v1/"
experimental_bearer_token = "shared-key"
wire_api = "chat"
`,
			[h(".dsh", ".credentials.yaml")]: "DEEPSEEK_API_KEY: shared-key\n",
		};
		const first = await scanLocalProviderConfigs(memoryContext(files));
		expect(first.sources).toEqual([
			{ source: "claude", found: false },
			{ source: "codex", found: true },
			{ source: "opencode", found: false },
			{ source: "deepseek-harness", found: true },
		]);
		expect(first.candidates).toHaveLength(1);
		expect(first.candidates[0]?.source).toBe("codex");
		const second = await scanLocalProviderConfigs(memoryContext(files));
		expect(second.candidates[0]?.id).toBe(first.candidates[0]?.id ?? "");
	});

	test("invalid files and non-http base URLs are ignored rather than thrown", async () => {
		const scan = await scanLocalProviderConfigs(
			memoryContext({
				[h(".claude", "settings.json")]: "{not json",
				[h(".codex", "config.toml")]: "[[[",
				[h(".config", "opencode", "opencode.json")]: JSON.stringify({
					provider: { x: { options: { baseURL: "file:///etc/passwd", apiKey: "k" } } },
				}),
			}),
		);
		expect(scan.candidates).toEqual([]);
	});
});

describe("summarizeLocalScan", () => {
	test("never exposes the key and flags providers that already exist", async () => {
		const scan = await scanLocalProviderConfigs(
			memoryContext({
				[h(".codex", "auth.json")]: JSON.stringify({ OPENAI_API_KEY: "sk-very-secret-9876" }),
			}),
		);
		const configured = new Set([
			providerIdentity("openai-responses", "https://api.openai.com/v1/", "sk-very-secret-9876"),
		]);
		const summary = summarizeLocalScan(scan, configured);
		expect(JSON.stringify(summary)).not.toContain("sk-very-secret");
		expect(summary.candidates[0]).toMatchObject({
			apiKeyPreview: "********9876",
			hasKey: true,
			alreadyConfigured: true,
		});
		expect(summarizeLocalScan(scan, new Set()).candidates[0]?.alreadyConfigured).toBe(false);
	});
});
