import { describe, expect, test } from "bun:test";
import {
	connectionForProtocolSwitch,
	openAIFamilyProtocol,
	protocolMatchesConnection,
} from "../protocol-override";

describe("protocol override keeps the connection", () => {
	test("copies proxy, headers and fingerprint onto the switched adapter config", () => {
		const headers = () => ({ "X-Channel": "live" });
		const shared = connectionForProtocolSwitch(
			{
				id: "p1",
				name: "Demo",
				prefix: "demo",
				apiKey: "secret",
				baseUrl: "https://upstream.example/v1",
				proxy: { mode: "custom", url: "http://127.0.0.1:9" },
				extraHeaders: { "X-Test": "1" },
				dynamicHeaders: headers,
				userAgentMode: "custom",
				customUserAgent: "demo-agent",
				officialApi: true,
			},
			"demo:claude-sonnet",
		);
		expect(shared.proxy).toEqual({ mode: "custom", url: "http://127.0.0.1:9" });
		expect(shared.extraHeaders).toEqual({ "X-Test": "1" });
		expect(shared.dynamicHeaders).toBe(headers);
		expect(shared.customUserAgent).toBe("demo-agent");
		expect(shared.officialApi).toBe(true);
		expect(shared.defaultModel).toBe("claude-sonnet");
	});

	test("a matching protocol stays on the original adapter", () => {
		expect(protocolMatchesConnection("anthropic", {}, "anthropic-messages")).toBe(true);
		expect(protocolMatchesConnection("openai", { apiMode: "codex" }, "openai-responses")).toBe(
			true,
		);
		expect(
			protocolMatchesConnection("openai", { apiMode: "responses" }, "completions-compatible"),
		).toBe(false);
		expect(protocolMatchesConnection("nug", {}, "anthropic-messages")).toBe(false);
		expect(openAIFamilyProtocol({ responsesApi: false })).toBe("completions-compatible");
	});
});
