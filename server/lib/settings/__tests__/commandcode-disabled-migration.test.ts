/**
 * 老版本把 Command Code 当成 `agent.disabledProviders` 里的普通前缀；现在它由
 * `settings.commandCode.disabled` 决定（后端据此把 provider 从可用候选里剔除）。
 *
 * 迁移必须同时做两件事：保留用户的停用意图，并把该前缀从 `disabledProviders` 移除 ——
 * 后者仍被客户端用来过滤模型列表，残留会让模型全部消失而 provider 却是启用状态。
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { DEFAULTS, narraforkDir, reloadSettings, saveSettings, settings } from "../index";

const path = join(narraforkDir, "settings.json");
// 这些用例读写真实 settings.json（afterEach 会还原）；全新环境里它可能还不存在。
if (!existsSync(path)) saveSettings(structuredClone(DEFAULTS));

let original: string;

beforeEach(() => {
	original = readFileSync(path, "utf8");
});
afterEach(() => {
	writeFileSync(path, original);
	reloadSettings();
});

describe("Command Code disabled flag migration", () => {
	test("moves a legacy disabledProviders entry onto commandCode.disabled", () => {
		const draft = structuredClone(settings);
		draft.agent.disabledProviders = ["openai", "CommandCode"];
		draft.commandCode = { apiKey: "user_demo" };
		saveSettings(draft);

		const loaded = reloadSettings();
		expect(loaded.agent.disabledProviders).toEqual(["openai"]);
		expect(loaded.commandCode?.disabled).toBe(true);

		// 迁移结果必须落盘，否则每次启动都会重新迁移。
		const persisted = JSON.parse(readFileSync(path, "utf8"));
		expect(persisted.agent.disabledProviders).not.toContain("CommandCode");
		expect(persisted.commandCode.disabled).toBe(true);
	});

	test("keeps an explicit flag and leaves other prefixes untouched", () => {
		const draft = structuredClone(settings);
		draft.agent.disabledProviders = ["nug"];
		draft.commandCode = { apiKey: "user_demo", disabled: false };
		saveSettings(draft);

		const loaded = reloadSettings();
		expect(loaded.agent.disabledProviders).toEqual(["nug"]);
		expect(loaded.commandCode?.disabled).toBe(false);
	});
});
