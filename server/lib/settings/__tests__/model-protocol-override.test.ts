/**
 * 逐模型协议的两层优先级。
 *
 * `agent.modelProtocolOverrides` 是「编辑此连接覆盖」里单独保存的那层，优先于设置页模型行
 * 写入的 `agent.modelProtocols`（provider 层）；删掉覆盖键就恢复继承 provider 的值。
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import {
	DEFAULTS,
	narraforkDir,
	reloadSettings,
	resolveModelProtocol,
	saveSettings,
	settings,
} from "../index";

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

describe("model protocol override precedence", () => {
	test("prefers the local override and inherits the provider protocol once it is removed", () => {
		const withBoth = structuredClone(settings);
		withBoth.agent.modelProtocols = { "CommandCode:demo": "openai-responses" };
		withBoth.agent.modelProtocolOverrides = { "CommandCode:demo": "completions-compatible" };
		saveSettings(withBoth);
		reloadSettings();
		expect(resolveModelProtocol("CommandCode", "demo")).toBe("completions-compatible");

		const inherited = structuredClone(settings);
		delete inherited.agent.modelProtocolOverrides?.["CommandCode:demo"];
		saveSettings(inherited);
		reloadSettings();
		expect(resolveModelProtocol("CommandCode", "demo")).toBe("openai-responses");

		// 没有模型（或两层都没有该键）时不返回任何协议，交给 provider 的默认规则。
		expect(resolveModelProtocol("CommandCode", undefined)).toBeUndefined();
		expect(resolveModelProtocol("CommandCode", "other-model")).toBeUndefined();
	});
});
