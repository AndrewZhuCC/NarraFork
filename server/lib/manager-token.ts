/**
 * 管理器面板调用本机服务的凭据。
 *
 * 面板没有用户 JWT。放行条件是两条同时成立：TCP 对端是回环地址，并且
 * `Authorization: Bearer` 等于启动时从环境变量收进内存的令牌。解析后的 clientIp 不能用，
 * 它会采纳受信代理上的 X-Forwarded-For，反向代理会把外部请求伪装成 127.0.0.1。
 */

import { timingSafeEqual } from "node:crypto";
import { isTrustedProxyAddress } from "./client-ip";

export const MANAGER_TOKEN_ENV = "NARRAFORK_MANAGER_TOKEN";

const LOOPBACK_CIDRS = ["127.0.0.0/8", "::1/128"];

/**
 * 启动时从环境变量收走的令牌。
 *
 * 令牌只该留在这个进程的内存里。终端和 Bash 会复制 `process.env`，继续留在环境里
 * 就能让非管理员调用关闭和更新接口。用赋值而不是 delete：Bun 的 delete 到不了
 * 子进程继承的那份原生环境。
 */
let capturedToken: string | undefined;
let tokenCaptured = false;

export function captureManagerTokenFromEnv(): void {
	if (tokenCaptured) return;
	tokenCaptured = true;
	const value = process.env[MANAGER_TOKEN_ENV]?.trim();
	capturedToken = value ? value : undefined;
	if (process.env[MANAGER_TOKEN_ENV] !== undefined) process.env[MANAGER_TOKEN_ENV] = "";
}

export function readManagerToken(): string | undefined {
	return capturedToken;
}

/** 更新拉起的替换进程需要这份令牌，其它子进程不要。 */
export function managerChildTokenEnv(): Record<string, string> {
	return capturedToken ? { [MANAGER_TOKEN_ENV]: capturedToken } : {};
}

/** 测试用：清掉已捕获的令牌，让下一次捕获重新读环境变量。 */
export function resetManagerTokenForTests(): void {
	capturedToken = undefined;
	tokenCaptured = false;
}

export function isManagerTokenAuthorized(input: {
	peerIp: string | null | undefined;
	authorization: string | null | undefined;
	expectedToken: string | null | undefined;
}): boolean {
	const expected = input.expectedToken?.trim();
	if (!expected) return false;
	if (!isTrustedProxyAddress(input.peerIp, LOOPBACK_CIDRS)) return false;
	const presented = bearerToken(input.authorization);
	if (!presented) return false;
	const actual = Buffer.from(presented);
	const wanted = Buffer.from(expected);
	return actual.length === wanted.length && timingSafeEqual(actual, wanted);
}

function bearerToken(header: string | null | undefined): string | undefined {
	if (!header) return undefined;
	const match = /^Bearer\s+(\S+)\s*$/i.exec(header);
	const token = match?.[1];
	return token && token.length > 0 ? token : undefined;
}
