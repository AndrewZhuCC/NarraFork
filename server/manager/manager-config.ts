/**
 * 管理器的配置读写。
 *
 * 监听地址/端口直接读写 NarraFork 自己的 settings.json —— 前端设置页改的就是同一份配置，
 * 所以两边天然同步：面板保存后前端看得到，前端改了面板也能通过 mtime 轮询回显。
 *
 * 写入采用「读整份 → 改两个字段 → 写回」，其余配置原样保留；并且先写同目录临时文件再原子
 * 改名，避免与 NarraFork 进程同时写时留下半截 JSON。
 */

import { existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { resolveNarraforkHome } from "./backend-process";

export interface ServerAddress {
	host: string;
	port: number;
}

const DEFAULT_ADDRESS: ServerAddress = { host: "127.0.0.1", port: 7779 };

function settingsPath(): string {
	return join(resolveNarraforkHome(), "settings.json");
}

/** 读取监听地址；文件缺失或不可解析时回落到默认值。 */
export function readServerAddress(): ServerAddress {
	try {
		const raw = JSON.parse(readFileSync(settingsPath(), "utf8")) as {
			server?: { host?: string; port?: number };
		};
		return {
			host:
				typeof raw.server?.host === "string" && raw.server.host
					? raw.server.host
					: DEFAULT_ADDRESS.host,
			port: typeof raw.server?.port === "number" ? raw.server.port : DEFAULT_ADDRESS.port,
		};
	} catch {
		return { ...DEFAULT_ADDRESS };
	}
}

/** 只覆盖 server.host / server.port，其余字段原样保留。返回是否写入成功。 */
export function writeServerAddress(address: ServerAddress): boolean {
	try {
		const path = settingsPath();
		const raw = existsSync(path)
			? (JSON.parse(readFileSync(path, "utf8")) as Record<string, unknown>)
			: {};
		const server = (
			typeof raw.server === "object" && raw.server !== null ? raw.server : {}
		) as Record<string, unknown>;
		raw.server = { ...server, host: address.host, port: address.port };
		const temporary = `${path}.manager.tmp`;
		writeFileSync(temporary, `${JSON.stringify(raw, null, 2)}\n`, "utf8");
		renameSync(temporary, path);
		return true;
	} catch {
		return false;
	}
}

/**
 * 管理器自己的偏好（与 NarraFork 的 settings 分开存，避免污染它的 schema 与迁移）。
 * 关闭行为 + 上次的窗口尺寸（下次启动恢复，首次用最小尺寸）。
 */
export interface ManagerPreferences {
	askOnClose: boolean;
	closeAction: "exit" | "minimize";
	/** 最近一次窗口客户区尺寸；未设置时用最小窗口尺寸启动。 */
	windowWidth?: number;
	windowHeight?: number;
}

const DEFAULT_PREFERENCES: ManagerPreferences = { askOnClose: true, closeAction: "exit" };

function preferencesPath(): string {
	return join(resolveNarraforkHome(), "manager.json");
}

export function readPreferences(): ManagerPreferences {
	try {
		const raw = JSON.parse(readFileSync(preferencesPath(), "utf8")) as Partial<ManagerPreferences>;
		const preferences: ManagerPreferences = {
			askOnClose:
				typeof raw.askOnClose === "boolean" ? raw.askOnClose : DEFAULT_PREFERENCES.askOnClose,
			closeAction: raw.closeAction === "minimize" ? "minimize" : "exit",
		};
		if (typeof raw.windowWidth === "number" && raw.windowWidth > 0) {
			preferences.windowWidth = Math.floor(raw.windowWidth);
		}
		if (typeof raw.windowHeight === "number" && raw.windowHeight > 0) {
			preferences.windowHeight = Math.floor(raw.windowHeight);
		}
		return preferences;
	} catch {
		return { ...DEFAULT_PREFERENCES };
	}
}

export function writePreferences(preferences: ManagerPreferences): boolean {
	try {
		const path = preferencesPath();
		mkdirSync(dirname(path), { recursive: true });
		const temporary = `${path}.tmp`;
		writeFileSync(temporary, `${JSON.stringify(preferences, null, 2)}\n`, "utf8");
		renameSync(temporary, path);
		return true;
	} catch {
		return false;
	}
}

/** settings.json 的最近修改时间（毫秒）；文件不存在时返回 0。 */
export function settingsMtime(): number {
	try {
		return statSync(settingsPath()).mtimeMs;
	} catch {
		return 0;
	}
}

/**
 * 轮询 settings.json 的 mtime，变化时回调；返回停止函数。
 *
 * 用 mtime 而不是内容比较：配置文件可能有几十 KB，每几秒全量读一次没有必要。
 */
export function watchSettings(onChange: () => void, intervalMs = 3000): () => void {
	let last = settingsMtime();
	const timer = setInterval(() => {
		const current = settingsMtime();
		if (current !== last) {
			last = current;
			onChange();
		}
	}, intervalMs);
	return () => clearInterval(timer);
}
