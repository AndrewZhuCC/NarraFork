import { afterEach, describe, expect, test } from "bun:test";
import { invalidateCommandCodeModelsCache, listCommandCodeModels } from "../commandcode-models";
import { getSubagentVisibleModels, getVisibleModels, settings } from "../settings";

/**
 * Command Code 的模型来自上游目录（`GET /provider/v1/models`），不是 settings 里的静态列表。
 * 这曾经让枚举路径和调用路径脱节：显式传 `CommandCode:<model>` 能正常跑子代理，但 Agent 工具
 * 说明的 model 名单里没有它，AI 助手据此误判模型不可用。
 *
 * 这些用例钉住那条枚举路径，并确认 commandCode.disabled 与 hiddenModels 仍然生效。
 */

const BASE = "https://models.example/provider/v1";
const MODEL_ID = "deepseek/deepseek-v4.1-flash";
const MODEL = `CommandCode:${MODEL_ID}`;

const originalFetch = globalThis.fetch;
const originalCommandCode = settings.commandCode;
const originalHiddenModels = settings.agent.hiddenModels;

/** 用一次上游响应把目录装进缓存——getVisibleModels 只读缓存，自己不发请求。 */
async function primeCatalog(): Promise<void> {
	globalThis.fetch = (async () =>
		new Response(
			JSON.stringify({ data: [{ id: MODEL_ID, supported_endpoints: ["/chat/completions"] }] }),
			{ status: 200, headers: { "Content-Type": "application/json" } },
		)) as unknown as typeof fetch;
	await listCommandCodeModels("test_key", BASE);
}

function configureCommandCode(apiKey: string, disabled: boolean): void {
	settings.commandCode = { disabled, apiKey, baseUrl: BASE };
}

afterEach(() => {
	globalThis.fetch = originalFetch;
	invalidateCommandCodeModelsCache();
	settings.commandCode = originalCommandCode;
	settings.agent.hiddenModels = originalHiddenModels;
});

describe("Command Code models in the agent model list", () => {
	test("an enabled provider's catalog models are visible to agents and subagents", async () => {
		await primeCatalog();
		configureCommandCode("test_key", false);

		expect(getVisibleModels()).toContain(MODEL);
		expect(getSubagentVisibleModels()).toContain(MODEL);
	});

	test("a disabled provider contributes no models", async () => {
		await primeCatalog();
		configureCommandCode("test_key", true);

		expect(getVisibleModels()).not.toContain(MODEL);
		expect(getSubagentVisibleModels()).not.toContain(MODEL);
	});

	test("hiddenModels still hides a Command Code model", async () => {
		await primeCatalog();
		configureCommandCode("test_key", false);
		settings.agent.hiddenModels = [...(originalHiddenModels ?? []), MODEL];

		expect(getVisibleModels()).not.toContain(MODEL);
	});

	test("a catalog cached for another credential is not reported as visible", async () => {
		await primeCatalog();
		configureCommandCode("other_key", false);

		// 缓存按 token + base 分槽，换了账号不能沿用上一次的目录。
		expect(getVisibleModels()).not.toContain(MODEL);
	});
});
