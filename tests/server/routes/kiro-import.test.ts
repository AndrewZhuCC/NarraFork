import { afterAll, beforeEach, describe, expect, it, mock } from "bun:test";
import { Hono } from "hono";
import type { ContentfulStatusCode } from "hono/utils/http-status";
import { AppError } from "../../../server/lib/errors";

let importedCredentials: unknown[] = [];

// Snapshot each module before mocking. Bun's mock.module is process-wide and
// mock.restore() does not undo it, so afterAll re-points the specifiers back.
const realModules: Record<string, () => unknown> = {};
const realKiroAdapter = { ...(await import("../../../server/lib/kiro-adapter")) };
const realLogger = { ...(await import("../../../server/lib/logger")) };
realModules["../../../server/lib/kiro-adapter"] = () => realKiroAdapter;
realModules["../../../server/lib/logger"] = () => realLogger;

mock.module("../../../server/lib/kiro-adapter", () => ({
	...realKiroAdapter,
	isKiroAvailable: () => true,
	kiroImportCredentials: (creds: unknown[]) => {
		importedCredentials = creds;
		return { added: creds.length, duplicates: 0 };
	},
	// No new credentials need model backfill in these tests.
	kiroSnapshot: () => ({ entries: [], total: 0, available: 0 }),
	kiroFetchCredentialModels: async () => [],
	kiroGetAllUsageCache: () => ({}),
	kiroUsageQueueSnapshot: () => ({ items: [], isRunning: false }),
}));

mock.module("../../../server/lib/logger", () => ({
	logger: { info: () => {}, warn: () => {}, error: () => {}, debug: () => {} },
}));

const { kiroRoutes } = await import("../../../server/routes/kiro");

const authUser = { sub: "admin-1", role: "admin" as const, iat: 0, exp: Number.MAX_SAFE_INTEGER };
const app = new Hono();
app.use("*", async (c, next) => {
	c.set("auth", { type: "session", user: authUser });
	c.set("user", authUser);
	await next();
});
app.route("/", kiroRoutes);
app.onError((err, c) => {
	if (err instanceof AppError) {
		return c.json({ error: err.message, code: err.code }, err.statusCode as ContentfulStatusCode);
	}
	return c.json({ error: String(err) }, 500);
});

async function post(body: unknown): Promise<Response> {
	return app.request("/credentials/import", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}

beforeEach(() => {
	importedCredentials = [];
});

afterAll(() => {
	for (const [specifier, factory] of Object.entries(realModules)) {
		mock.module(specifier, factory);
	}
	mock.restore();
});

describe("POST /kiro/credentials/import", () => {
	it("imports multi-line plain ksk_ keys via credentials strings", async () => {
		const res = await post({ credentials: ["ksk_line1\nksk_line2"] });

		expect(res.status).toBe(200);
		expect(await res.json()).toMatchObject({ added: 2, duplicates: 0 });
		expect(importedCredentials).toEqual([
			{ apiKey: "ksk_line1", authMethod: "api_key" },
			{ apiKey: "ksk_line2", authMethod: "api_key" },
		]);
	});

	it("imports plain keys from the text field", async () => {
		const res = await post({ text: "ksk_from_text" });

		expect(res.status).toBe(200);
		expect(importedCredentials).toEqual([{ apiKey: "ksk_from_text", authMethod: "api_key" }]);
	});

	it("keeps accepting credential objects", async () => {
		const res = await post({ credentials: [{ refreshToken: "rt-1", authMethod: "social" }] });

		expect(res.status).toBe(200);
		expect(importedCredentials).toEqual([{ refreshToken: "rt-1", authMethod: "social" }]);
	});

	it("treats an empty credentials array as a reload trigger", async () => {
		const res = await post({ credentials: [] });

		expect(res.status).toBe(200);
		expect(importedCredentials).toEqual([]);
	});

	it("rejects a payload with no usable credential material", async () => {
		const res = await post({ text: "no keys here" });

		expect(res.status).toBe(400);
		expect(await res.json()).toMatchObject({ error: "No credentials provided" });
	});

	it("rejects a non-array, non-string credentials field", async () => {
		const res = await post({ credentials: 42 });

		expect(res.status).toBe(400);
		expect(await res.json()).toMatchObject({ error: "credentials must be an array" });
	});

	it("rejects array entries that are neither objects nor strings", async () => {
		const res = await post({ credentials: [["nested"]] });

		expect(res.status).toBe(400);
		expect(await res.json()).toMatchObject({ error: "each credential must be a plain object" });
	});

	// Parsing and key extraction are synchronous main-thread work, so an oversized
	// paste must be refused at the door rather than stalling every other request.
	it("rejects a body over the 1 MiB import cap without parsing it", async () => {
		const oversized = JSON.stringify({ text: `ksk_${"a".repeat(1024 * 1024 + 64)}` });
		const res = await app.request("/credentials/import", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: oversized,
		});

		expect(res.status).toBe(413);
		expect(await res.json()).toMatchObject({ code: "CREDENTIAL_IMPORT_TOO_LARGE" });
		expect(importedCredentials).toEqual([]);
	});

	it("still accepts a body just under the cap", async () => {
		const res = await post({ text: `ksk_${"b".repeat(4096)}` });

		expect(res.status).toBe(200);
		expect(importedCredentials).toHaveLength(1);
	});
});
