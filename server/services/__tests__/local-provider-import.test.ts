import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import {
	anthropicBaseWithVersion,
	detectLocalProviderSources,
	harnessPatchRows,
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

		const repointed = memoryContext({
			[h(".codex", "auth.json")]: JSON.stringify({ OPENAI_API_KEY: "sk-relay" }),
			[h(".codex", "config.toml")]: 'openai_base_url = "https://relay.example/v1"\n',
		});
		expect((await scanCodex(repointed)).candidates).toMatchObject([
			{ name: "Codex (relay.example)", baseUrl: "https://relay.example/v1", apiKey: "sk-relay" },
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
	test("profile patch routes resolve keys: stored refs, then env, then home .env", async () => {
		const ctx = memoryContext(
			{
				[h(".dsh", ".credentials.yaml")]:
					"version: 1\nrefs:\n  REQUESTY_API_KEY: stored-key\n  OPENAI_API_KEY: stored-openai\nrecords: {}\n",
				[h(".dsh", ".env")]: "HOMEENV_KEY='from-dotenv'\n",
				[h(".dsh", "profiles", "default", "cordis.patch.yml")]: `
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      requesty:
        displayName: Requesty
        api: openai-completions
        baseURL: https://router.requesty.ai/v1
        apiKeyEnv: REQUESTY_API_KEY
        models:
          - id: deepseek/deepseek-v4-pro
      openai:
        apiKeyEnv: OPENAI_API_KEY
      gateway:
        api: anthropic-messages
        baseURL: https://gateway.example
        apiKeyEnv: HOMEENV_KEY
      keyless:
        api: openai-completions
        baseURL: https://keyless.example/v1
- id: hmr
  disabled: !!js "!ctx.get('profileContext')"
`,
			},
			{ OPENAI_API_KEY: "env-openai-loses" },
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
				name: "Requesty",
				protocol: "completions-compatible",
				baseUrl: "https://router.requesty.ai/v1",
				apiKey: "stored-key",
			},
			{
				name: "OpenAI",
				protocol: "openai-responses",
				baseUrl: "https://api.openai.com/v1",
				apiKey: "stored-openai",
			},
			{
				name: "gateway",
				protocol: "anthropic-messages",
				baseUrl: "https://gateway.example/v1",
				apiKey: "from-dotenv",
			},
		]);
	});

	test("native DeepSeek adapter: Anthropic Messages root, overridable reference and endpoint", async () => {
		const bare = memoryContext({
			[h(".dsh", ".credentials.yaml")]: "DEEPSEEK_API_KEY: legacy-flat\n",
		});
		expect((await scanDeepseekHarness(bare)).candidates).toMatchObject([
			{
				name: "DeepSeek",
				protocol: "anthropic-messages",
				baseUrl: "https://api.deepseek.com/anthropic/v1",
				apiKey: "legacy-flat",
			},
		]);

		const configured = memoryContext({
			[h(".dsh", ".credentials.yaml")]: "version: 1\nrefs:\n  MY_DS: custom\n",
			[h(".dsh", "profiles", "work", "cordis.patch.yml")]: `
- id: llm-deepseek
  name: '@deepseek-ai/dsh-llm-deepseek-api-key'
  config:
    apiKeyEnv: MY_DS
    baseURL: https://ds-gateway.example/v1
`,
		});
		expect((await scanDeepseekHarness(configured)).candidates).toMatchObject([
			{
				protocol: "anthropic-messages",
				baseUrl: "https://ds-gateway.example/v1",
				apiKey: "custom",
			},
		]);
	});

	test("home patch replaces a profile row's config; legacy settings.yaml sections are read", async () => {
		const ctx = memoryContext(
			{
				[h(".dsh", "profiles", "default", "cordis.patch.yml")]: `
- id: llm-pi-ai
  config:
    providers:
      old:
        api: openai-completions
        baseURL: https://old.example/v1
        apiKeyEnv: OLD_KEY
`,
				[h(".dsh", "cordis.patch.yml")]: `
- id: llm-pi-ai
  config:
    providers:
      new:
        api: openai-completions
        baseURL: https://new.example/v1
        apiKeyEnv: NEW_KEY
`,
				[h(".dsh", "settings.yaml")]: `
llm-pi-ai:
  providers:
    bailian:
      api: openai-completions
      baseURL: https://dashscope.aliyuncs.com/compatible-mode/v1
      apiKeyEnv: BAILIAN_API_KEY
`,
			},
			{ OLD_KEY: "old", NEW_KEY: "new", BAILIAN_API_KEY: "bl-key" },
		);
		expect((await scanDeepseekHarness(ctx)).candidates.map((c) => c.apiKey)).toEqual([
			"new",
			"bl-key",
		]);
		expect((await scanDeepseekHarness(memoryContext({}))).found).toBe(false);
	});

	test("harnessPatchRows flattens insert blocks and per-id overrides", () => {
		const rows = harnessPatchRows([
			{ insert: [{ id: "llm-pi-ai", name: "@deepseek-ai/dsh-llm-pi-ai" }] },
			{ id: "llm-pi-ai", config: { providers: { a: {} } } },
		]);
		expect(rows.get("llm-pi-ai")).toEqual({
			id: "llm-pi-ai",
			name: "@deepseek-ai/dsh-llm-pi-ai",
			config: { providers: { a: {} } },
		});
	});
});

describe("scanLocalProviderConfigs", () => {
	test("reports every source, dedupes the same key across tools, and keeps ids stable", async () => {
		const files = {
			[h(".codex", "auth.json")]: JSON.stringify({ OPENAI_API_KEY: "shared-key" }),
			[h(".local", "share", "opencode", "auth.json")]: JSON.stringify({
				openai: { type: "api", key: "shared-key" },
			}),
		};
		const first = await scanLocalProviderConfigs(memoryContext(files));
		expect(first.sources).toEqual([
			{ source: "claude", found: false },
			{ source: "codex", found: true },
			{ source: "opencode", found: true },
			{ source: "deepseek-harness", found: false },
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

describe("detectLocalProviderSources", () => {
	test("reports tools by config location without reading any file", async () => {
		const ctx = memoryContext({
			[h(".claude.json")]: "{}",
			[h(".dsh", "profiles", "default", "cordis.patch.yml")]: "[]",
		});
		let reads = 0;
		const readText = ctx.readText;
		ctx.readText = async (path) => {
			reads++;
			return readText(path);
		};
		expect(await detectLocalProviderSources(ctx)).toEqual([
			{ source: "claude", found: true },
			{ source: "codex", found: false },
			{ source: "opencode", found: false },
			{ source: "deepseek-harness", found: true },
		]);
		expect(reads).toBe(0);
	});
});
