import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { PLUGIN_UI_BACKEND_METHODS } from "@frontend/components/plugins/protocol";
import { parseManifest } from "@server/lib/plugins/manifest";

/**
 * The credential page, checked against what the host actually offers an iframe.
 *
 * A view runs in a sandboxed document, so a call to a method the host does not implement
 * fails at runtime with nothing to catch it at manifest time. These tests are the
 * manifest-time check: every SDK method the built artifact calls must exist in the host's
 * iframe inventory, and the page must not attempt to read secret values or reach the network.
 */

const PLUGIN_ROOT = resolve(import.meta.dir, "../../../../examples/plugins/kiro-external");

async function viewSource(): Promise<string> {
	return readFile(join(PLUGIN_ROOT, "ui/provider-settings.iife.js"), "utf8");
}

async function manifest() {
	return parseManifest(JSON.parse(await readFile(join(PLUGIN_ROOT, "manifest.json"), "utf8")));
}

describe("kiro-external settings view: host contract", () => {
	test("every SDK method it calls is one the host exposes to an iframe", async () => {
		const source = await viewSource();
		const called = [...source.matchAll(/request\(\s*"([a-z][a-zA-Z.]*)"/g)].map(
			(match) => match[1],
		);
		expect(called.length).toBeGreaterThan(0);
		const allowed = new Set<string>([
			...PLUGIN_UI_BACKEND_METHODS,
			// Host-local methods are handled in the frontend bridge rather than the backend.
			"context.get",
			"panel.setTitle",
			"notifications.show",
		]);
		for (const method of new Set(called)) {
			expect(allowed.has(method), `view calls unsupported SDK method: ${method}`).toBe(true);
		}
	});

	test("drives the backend through commands.execute", async () => {
		// This is the whole point of the `commands.invoke` work: the page's actions reach the
		// plugin backend rather than an HTTP endpoint the iframe cannot call.
		expect(await viewSource()).toContain('request("commands.execute"');
	});

	test("never asks for a secret value", async () => {
		const source = await viewSource();
		// Only presence is available to a UI; there is no host method that returns a value.
		expect(source).not.toContain('"secrets.get"');
		expect(source).not.toContain('"config.getSecret"');
	});

	test("does not reach for the network from inside the iframe", async () => {
		const source = await viewSource();
		// CSP is `connect-src 'none'`, so any of these would be a silent runtime failure.
		expect(source).not.toMatch(/\bfetch\(/);
		expect(source).not.toContain("XMLHttpRequest");
		expect(source).not.toContain("EventSource");
		expect(source).not.toContain("WebSocket");
	});

	test("clears the import box so a pasted secret does not linger in the document", async () => {
		// The textarea is the only place a credential exists in this document; it must not
		// outlive the request that consumes it. The page is a controlled React input, so the
		// clear is a state reset rather than an assignment to `.value`.
		expect(await viewSource()).toContain('setImportText("")');
	});

	test("does not rely on modal APIs the sandbox ignores", async () => {
		const source = await viewSource();
		// The iframe is sandboxed with `allow-scripts` only. Without `allow-modals` the browser
		// ignores `confirm`/`alert` and returns `undefined`, so `if (!confirm(...)) return;`
		// lets a destructive action through unconditionally — which is exactly the bug the
		// hand-written version of this page shipped on credential deletion.
		expect(source).not.toMatch(/\bwindow\.(confirm|alert|prompt)\b/);
		expect(source).not.toMatch(/[^.\w](confirm|alert|prompt)\(/);
	});

	test("does not bundle its own React or Mantine", async () => {
		const source = await viewSource();
		// The panel renders with the host's shared runtime. A second copy of React here would
		// mean components created by one React and rendered by another, and would undo the
		// size argument for having a shared runtime at all.
		expect(source).toContain("__nfPluginRuntime");
		expect(source).not.toContain("react-dom");
		expect(source).not.toContain("@mantine/core");
	});
});

describe("kiro-external settings view: manifest wiring", () => {
	test("the view is bound to this plugin's provider and surface", async () => {
		const parsed = await manifest();
		const view = parsed.contributes.views.find((item) => item.id === "kiro-external-settings");
		expect(view).toBeDefined();
		expect(view?.surfaces).toContain("provider-settings");
		expect(view?.providerId).toBe("kiro");
	});

	test("opts into the host UI runtime the page depends on", async () => {
		// Without this the shell never injects React/Mantine and the page throws on a missing
		// global. The declaration and the page's use of `__nfPluginRuntime` must agree.
		const parsed = await manifest();
		const view = parsed.contributes.views.find((item) => item.id === "kiro-external-settings");
		expect(view?.runtime).toBe("host-react");
	});

	test("every command the view invokes is declared with a server handler", async () => {
		const parsed = await manifest();
		const source = await viewSource();
		const declared = new Map(
			parsed.contributes.commands.map((command) => [command.id, command.handler]),
		);
		// Extract the command ids the page actually calls, so a renamed command cannot leave
		// the UI pointing at nothing — the exact failure that shipped last round.
		const invoked = [...source.matchAll(/commandId:\s*"([a-zA-Z.-]+)"|command\("([a-zA-Z.-]+)"/g)]
			.map((match) => match[1] ?? match[2])
			.filter((value): value is string => Boolean(value));
		expect(invoked.length).toBeGreaterThan(0);
		for (const commandId of new Set(invoked)) {
			expect(declared.has(commandId), `view calls undeclared command: ${commandId}`).toBe(true);
			expect(declared.get(commandId), `${commandId} must be server-handled`).toBe("server");
		}
	});

	test("declares the capabilities the page needs", async () => {
		const parsed = await manifest();
		expect(parsed.permissions.host).toContain("ui.panel");
		expect(parsed.permissions.host).toContain("config.read_self");
	});
});
