/**
 * The plugin's adoption of the host-provided proxy (`hostHints.outbound.proxyUrl`).
 *
 * Asserts against the real `host-hints.ts` implementation rather than a copy of its logic
 * re-declared here. That distinction matters: an inlined copy keeps passing after the
 * original changes, which is how a proxy-clearing bug survived the first implementation of
 * these tests.
 *
 * The logic lives in its own module precisely so it can be imported — `server.ts` calls
 * `listen()` and writes a `hello` frame at import time and would take over stdio.
 *
 * What this proves end to end: the host sends `hostHints` → the plugin applies it →
 * `getProxy()` (which `pfetch` consults on every upstream request inside the bundled Kiro
 * client modules) returns that value.
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { getProxy, registerProxyResolver } from "@server/lib/kiro/proxy";
import {
	activeProxyUrl,
	applyHostHints,
	installProxyResolver,
	resetHostHints,
} from "../../../../examples/plugins/kiro-external/src/host-hints";

/** A provider request as the host sends it, with only the fields these tests care about. */
function chatParams(hostHints?: unknown): Record<string, unknown> {
	return {
		config: { credentialsBundle: "[]" },
		modelId: "claude-sonnet-4.5",
		operationId: "op-1",
		...(hostHints !== undefined ? { hostHints } : {}),
	};
}

beforeEach(() => {
	resetHostHints();
	installProxyResolver();
});

afterEach(() => {
	resetHostHints();
	// Leave the shared kiro proxy singleton inert so unrelated suites are unaffected.
	registerProxyResolver(() => undefined);
});

describe("kiro-external proxy adoption", () => {
	test("a host-sent proxy reaches the kiro client layer", () => {
		expect(getProxy()).toBeUndefined();
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://squid.corp:3128" } }));
		expect(getProxy()).toBe("http://squid.corp:3128");
	});

	test("a changed proxy takes effect on the next call", () => {
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://old.corp:8080" } }));
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://new.corp:3128" } }));
		expect(getProxy()).toBe("http://new.corp:3128");
	});

	test("a removed proxy is cleared rather than left in effect", () => {
		// The regression this guards: assigning only on a non-empty string leaves a deleted
		// proxy applied for the rest of the process lifetime, so the plugin keeps routing
		// upstream traffic through a proxy the user already removed from settings.
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://squid.corp:3128" } }));
		expect(getProxy()).toBe("http://squid.corp:3128");

		applyHostHints(chatParams({ outbound: {} }));
		expect(getProxy()).toBeUndefined();
	});

	test("hostHints with no outbound block also clears the proxy", () => {
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://squid.corp:3128" } }));
		applyHostHints(chatParams({ concurrency: { maxConcurrentUpstream: 4 } }));
		expect(getProxy()).toBeUndefined();
	});

	test("an empty proxy string is treated as no proxy", () => {
		// The host documents that it never sends an empty string, because absent and empty
		// mean different things. Should one arrive anyway, "no proxy" is the safe reading.
		applyHostHints(chatParams({ outbound: { proxyUrl: "" } }));
		expect(getProxy()).toBeUndefined();
	});

	test("absent hostHints leaves the current proxy untouched", () => {
		// Silence is not the same as "no proxy": a call that carries no hints says nothing
		// about policy, so the last known value must survive.
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://squid.corp:3128" } }));
		applyHostHints(chatParams());
		expect(getProxy()).toBe("http://squid.corp:3128");
	});

	test("a proxy URL carrying credentials survives verbatim", () => {
		// Userinfo must not be normalised or stripped; the upstream connection needs it.
		const withCredentials = "http://user:p%40ss@proxy.internal:8080";
		applyHostHints(chatParams({ outbound: { proxyUrl: withCredentials } }));
		expect(getProxy()).toBe(withCredentials);
	});

	test("malformed params are ignored instead of throwing", () => {
		// These arrive from a remote process, so the plugin must not fault the request over
		// a shape it did not expect.
		applyHostHints(chatParams({ outbound: { proxyUrl: "http://squid.corp:3128" } }));
		for (const bad of [null, undefined, "string", 42, [], { hostHints: "nope" }]) {
			expect(() => applyHostHints(bad)).not.toThrow();
		}
		expect(getProxy()).toBe("http://squid.corp:3128");
	});

	test("a non-string proxyUrl is rejected rather than coerced", () => {
		applyHostHints(chatParams({ outbound: { proxyUrl: 8080 } }));
		expect(activeProxyUrl()).toBeUndefined();
	});
});
