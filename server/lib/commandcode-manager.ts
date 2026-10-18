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
	/** 未登录或文件不可用时给出的可操作提示。 */
	reason?: string;
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
			return { reason: "~/.commandcode/auth.json 里没有 apiKey" };
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
		return {
			reason:
				code === "ENOENT"
					? "未配置 Command Code：请在下方填入 Studio 生成的 API key"
					: `读取 ~/.commandcode/auth.json 失败：${error instanceof Error ? error.message : String(error)}`,
		};
	}
}
