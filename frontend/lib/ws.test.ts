import { describe, expect, test } from "bun:test";
import { resetAppBaseForTest } from "./base-path";
import { buildWsUrl } from "./ws";

interface GlobalPatch {
	key: string;
	had: boolean;
	previous: unknown;
}

function patchGlobal(key: string, value: unknown): GlobalPatch {
	const globals = globalThis as Record<string, unknown>;
	const patch = { key, had: Object.hasOwn(globals, key), previous: globals[key] };
	globals[key] = value;
	return patch;
}

function restoreGlobal(patch: GlobalPatch): void {
	const globals = globalThis as Record<string, unknown>;
	if (patch.had) globals[patch.key] = patch.previous;
	else delete globals[patch.key];
}

function withBrowserLocation(
	location: { protocol: string; hostname: string; host: string; port: string },
	baseURI: string,
	fn: () => void,
	viteHost = "0.0.0.0",
): void {
	const patches = [
		patchGlobal("window", { location }),
		patchGlobal("location", { ...location, href: `${location.protocol}//${location.host}/` }),
		patchGlobal("document", { baseURI }),
		patchGlobal("__DEV_VITE_PORT__", "41221"),
		patchGlobal("__DEV_BACKEND_PORT__", "7778"),
		patchGlobal("__DEV_VITE_HOST__", viteHost),
	];
	resetAppBaseForTest();
	try {
		fn();
	} finally {
		resetAppBaseForTest();
		for (const patch of patches.reverse()) restoreGlobal(patch);
	}
}

describe("buildWsUrl", () => {
	test("keeps a remote host, secure protocol, and mount prefix even in serve mode", () => {
		withBrowserLocation(
			{
				protocol: "https:",
				hostname: "nf.example.com",
				host: "nf.example.com",
				port: "",
			},
			"https://nf.example.com/nf/",
			() => {
				expect(buildWsUrl("/ws/narrator", "token=test")).toBe(
					"wss://nf.example.com/nf/ws/narrator?token=test",
				);
			},
		);
	});

	test("still bypasses the broken proxy for direct local Vite access", () => {
		withBrowserLocation(
			{
				protocol: "http:",
				hostname: "localhost",
				host: "localhost:41221",
				port: "41221",
			},
			"http://localhost:41221/",
			() => {
				expect(buildWsUrl("/ws/terminal")).toBe("ws://localhost:7778/ws/terminal");
			},
		);
	});

	test("uses the backend port for the explicitly configured LAN Vite host", () => {
		withBrowserLocation(
			{
				protocol: "http:",
				hostname: "192.168.119.105",
				host: "192.168.119.105:41221",
				port: "41221",
			},
			"http://192.168.119.105:41221/",
			() =>
				expect(buildWsUrl("/ws/narrator", "token=test")).toBe(
					"ws://192.168.119.105:7778/ws/narrator?token=test",
				),
			"192.168.119.105",
		);
	});

	test("does not turn arbitrary LAN hosts into direct-backend connections", () => {
		withBrowserLocation(
			{
				protocol: "http:",
				hostname: "192.168.119.104",
				host: "192.168.119.104:41221",
				port: "41221",
			},
			"http://192.168.119.104:41221/nf/",
			() => expect(buildWsUrl("/ws/narrator")).toBe("ws://192.168.119.104:41221/nf/ws/narrator"),
			"192.168.119.105",
		);
	});
});
