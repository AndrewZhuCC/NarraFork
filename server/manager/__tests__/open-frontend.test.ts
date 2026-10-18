import { describe, expect, test } from "bun:test";
import { resolveFrontendOpen } from "../open-frontend";

describe("resolveFrontendOpen", () => {
	test("启动时尊重不打开，明确点击回落到浏览器", () => {
		expect(resolveFrontendOpen({ openBrowser: "off" })).toBe("skip");
		expect(resolveFrontendOpen({ openBrowser: "off", explicit: true })).toBe("browser");
		expect(resolveFrontendOpen({ openBrowser: "off", forceNewWindow: true })).toBe("browser");
	});

	test("浏览器和网页应用按设置打开", () => {
		expect(resolveFrontendOpen({ openBrowser: "browser", explicit: true })).toBe("browser");
		expect(resolveFrontendOpen({ openBrowser: "app" })).toBe("app");
		expect(resolveFrontendOpen({ openBrowser: "app", explicit: true })).toBe("app");
		expect(resolveFrontendOpen({ openBrowser: "something-else" })).toBe("browser");
	});
});
