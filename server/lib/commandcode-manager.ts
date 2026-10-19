/**
 * Command Code 凭据读取。
 *
 * Command Code CLI（`cmd`）登录后把 OAuth 凭据写入 `~/.commandcode/auth.json`；NarraFork 不
 * 重复实现 OAuth 流程，直接复用该文件 —— 与 codex 复用 CodexManager 凭据池同一个思路，
 * 区别是 CLI 只维护单账号，这里也先按单账号实现。
 */

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { logger } from "./logger";

/** 给前端翻译的稳定原因码，不把本机路径或文件系统错误原文回给浏览器。 */
export type CommandCodeAuthReason = "not_configured" | "missing_api_key" | "read_failed";

export interface CommandCodeCredential {
	/** 访问令牌，用作 `Authorization: Bearer`。 */
	token: string;
	/** 令牌来自哪里：设置页手填，还是 CLI 落盘的 auth.json。 */
	source: "settings" | "cli";
	/** CLI 登录时附带的账号信息，仅用于展示。 */
	userName?: string;
	userId?: string;
}

export interface CommandCodeAuthState {
	credential?: CommandCodeCredential;
	/** 未登录或文件不可用时的原因码，由前端按界面语言显示。 */
	reason?: CommandCodeAuthReason;
}

/** CLI 固定把凭据写在这里，与 NARRAFORK_HOME 无关。 */
export function commandCodeAuthPath(): string {
	return join(homedir(), ".commandcode", "auth.json");
}

/**
 * 取用凭据：设置页手填的 API key 优先，其次才是 CLI 落盘的 auth.json。
 *
 * 这样用户可以完全不用安装 CLI —— 从 Studio 生成一把 key 填进设置即可；
 * 已经用 CLI 登录过的用户也无需重配。
 */
export function resolveCommandCodeToken(configuredApiKey?: string): CommandCodeAuthState {
	const apiKey = configuredApiKey?.trim();
	if (apiKey) return { credential: { token: apiKey, source: "settings" } };
	return readCommandCodeAuth();
}

/** 读取并校验凭据；任何失败都返回可读原因而不是抛错，调用方直接展示即可。 */
export function readCommandCodeAuth(): CommandCodeAuthState {
	try {
		const raw = JSON.parse(readFileSync(commandCodeAuthPath(), "utf8")) as {
			apiKey?: unknown;
			userName?: unknown;
			userId?: unknown;
		};
		const token = typeof raw.apiKey === "string" ? raw.apiKey.trim() : "";
		if (!token) {
			return { reason: "missing_api_key" };
		}
		return {
			credential: {
				token,
				source: "cli",
				userName: typeof raw.userName === "string" ? raw.userName : undefined,
				userId: typeof raw.userId === "string" ? raw.userId : undefined,
			},
		};
	} catch (error) {
		const code = (error as NodeJS.ErrnoException)?.code;
		if (code !== "ENOENT") {
			logger.warn("Failed to read Command Code CLI auth file", {
				error: error instanceof Error ? error.message : String(error),
			});
		}
		return { reason: code === "ENOENT" ? "not_configured" : "read_failed" };
	}
}
