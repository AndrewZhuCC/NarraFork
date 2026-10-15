/**
 * 管理器的配置读写。
 *
 * 关键是「写入不能破坏其它配置」：settings.json 由 NarraFork 自己维护，面板只被允许改
 * server.host / server.port 两个字段。关闭偏好存在独立的 manager.json，不进 settings。
 */
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	readPreferences,
	readServerAddress,
	settingsMtime,
	writePreferences,
	writeServerAddress,
} from "../manager-config";

let home = "";

beforeEach(() => {
	home = mkdtempSync(join(tmpdir(), "nf-manager-config-"));
	process.env.NARRAFORK_HOME = home;
});

afterEach(() => {
	delete process.env.NARRAFORK_HOME;
	rmSync(home, { recursive: true, force: true });
});

function writeSettings(value: unknown): void {
	writeFileSync(join(home, "settings.json"), JSON.stringify(value, null, 2), "utf8");
}

describe("监听地址读写", () => {
	test("缺文件时回落到默认值", () => {
		expect(readServerAddress()).toEqual({ host: "127.0.0.1", port: 7779 });
	});

	test("写入只改 server.host/port，其它配置原样保留", () => {
		writeSettings({
			server: { host: "127.0.0.1", port: 7779, trustProxy: true },
			branding: { iconColor: "#123456" },
			agent: { defaultModel: "x" },
		});

		expect(writeServerAddress({ host: "0.0.0.0", port: 8080 })).toBe(true);

		const saved = JSON.parse(readFileSync(join(home, "settings.json"), "utf8"));
		expect(saved.server).toEqual({ host: "0.0.0.0", port: 8080, trustProxy: true });
		expect(saved.branding).toEqual({ iconColor: "#123456" });
		expect(saved.agent).toEqual({ defaultModel: "x" });
		expect(readServerAddress()).toEqual({ host: "0.0.0.0", port: 8080 });
	});

	test("写入后 mtime 前进，回显轮询据此发现外部修改", async () => {
		writeSettings({ server: { host: "127.0.0.1", port: 7779 } });
		const before = settingsMtime();
		await Bun.sleep(10);
		writeServerAddress({ host: "127.0.0.1", port: 9000 });
		expect(settingsMtime()).toBeGreaterThan(before);
	});
});

describe("关闭偏好", () => {
	test("缺文件时默认每次询问、默认直接退出", () => {
		expect(readPreferences()).toEqual({ askOnClose: true, closeAction: "exit" });
	});

	test("往返写入后读回一致", () => {
		expect(writePreferences({ askOnClose: false, closeAction: "minimize" })).toBe(true);
		expect(readPreferences()).toEqual({ askOnClose: false, closeAction: "minimize" });
	});
});
