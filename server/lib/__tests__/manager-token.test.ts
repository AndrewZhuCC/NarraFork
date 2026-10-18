import { afterEach, describe, expect, test } from "bun:test";
import {
	captureManagerTokenFromEnv,
	isManagerTokenAuthorized,
	MANAGER_TOKEN_ENV,
	managerChildTokenEnv,
	readManagerToken,
	resetManagerTokenForTests,
} from "../manager-token";
import { envWithAmbientProxy } from "../net/proxy-env";

const TOKEN = "manager-token-value";

describe("isManagerTokenAuthorized", () => {
	test("回环对端加上正确令牌才放行", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "127.0.0.1",
				authorization: `Bearer ${TOKEN}`,
				expectedToken: TOKEN,
			}),
		).toBe(true);
	});

	test("IPv4 映射的回环地址同样放行", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "::ffff:127.0.0.1",
				authorization: `Bearer ${TOKEN}`,
				expectedToken: TOKEN,
			}),
		).toBe(true);
	});

	test("公网对端即使持有令牌也拒绝", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "203.0.113.5",
				authorization: `Bearer ${TOKEN}`,
				expectedToken: TOKEN,
			}),
		).toBe(false);
	});

	test("回环但没有令牌拒绝", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "127.0.0.1",
				authorization: undefined,
				expectedToken: TOKEN,
			}),
		).toBe(false);
	});

	test("服务没有配置令牌时拒绝", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "127.0.0.1",
				authorization: `Bearer ${TOKEN}`,
				expectedToken: undefined,
			}),
		).toBe(false);
	});

	test("令牌不一致拒绝", () => {
		expect(
			isManagerTokenAuthorized({
				peerIp: "::1",
				authorization: "Bearer other-token",
				expectedToken: TOKEN,
			}),
		).toBe(false);
	});
});

describe("captureManagerTokenFromEnv", () => {
	const previous = process.env[MANAGER_TOKEN_ENV];

	afterEach(() => {
		resetManagerTokenForTests();
		if (previous === undefined) delete process.env[MANAGER_TOKEN_ENV];
		else process.env[MANAGER_TOKEN_ENV] = previous;
	});

	test("收进内存后，子进程环境里不再有这份令牌", () => {
		process.env[MANAGER_TOKEN_ENV] = TOKEN;
		captureManagerTokenFromEnv();

		expect(readManagerToken()).toBe(TOKEN);
		expect(envWithAmbientProxy()[MANAGER_TOKEN_ENV]).not.toBe(TOKEN);
		expect(managerChildTokenEnv()).toEqual({ [MANAGER_TOKEN_ENV]: TOKEN });
	});

	test("第二次捕获不会把已经收走的令牌清掉", () => {
		process.env[MANAGER_TOKEN_ENV] = TOKEN;
		captureManagerTokenFromEnv();
		process.env[MANAGER_TOKEN_ENV] = "other-token";
		captureManagerTokenFromEnv();

		expect(readManagerToken()).toBe(TOKEN);
		expect(managerChildTokenEnv()).toEqual({ [MANAGER_TOKEN_ENV]: TOKEN });
	});
});
