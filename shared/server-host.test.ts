import { describe, expect, test } from "bun:test";
import { probeHost, urlHost } from "./server-host";

describe("urlHost", () => {
	test("通配地址和 auto-lan 写成回环，裸 IPv6 加方括号", () => {
		expect(urlHost("0.0.0.0")).toBe("127.0.0.1");
		expect(urlHost("::")).toBe("127.0.0.1");
		expect(urlHost("[::]")).toBe("127.0.0.1");
		expect(urlHost("")).toBe("127.0.0.1");
		expect(urlHost("auto-lan")).toBe("127.0.0.1");
		expect(urlHost("::1")).toBe("[::1]");
		expect(urlHost("[::1]")).toBe("[::1]");
		expect(urlHost("localhost")).toBe("localhost");
	});
});

describe("probeHost", () => {
	test("通配地址探测回环，auto-lan 探测第一块局域网地址", () => {
		expect(probeHost("0.0.0.0", ["192.168.1.5"])).toBe("127.0.0.1");
		expect(probeHost("::")).toBe("127.0.0.1");
		expect(probeHost("auto-lan", ["192.168.1.5", "10.0.0.2"])).toBe("192.168.1.5");
		expect(probeHost("auto-lan", ["", "10.0.0.2"])).toBe("10.0.0.2");
		expect(probeHost("auto-lan")).toBe("127.0.0.1");
		expect(probeHost("::1")).toBe("[::1]");
		expect(probeHost("[fe80::1]")).toBe("[fe80::1]");
	});
});
