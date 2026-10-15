import { describe, expect, mock, test } from "bun:test";
import { AppError } from "@server/lib/errors";
import type { TokenRepairJob, TokenRepairProtocolOverride } from "@shared/usage-token-repair";
import type { Context, Next } from "hono";
import { Hono } from "hono";
import { HTTPException } from "hono/http-exception";
import { createUsageTokenRepairRoutes } from "../usage-token-repair";

const job: TokenRepairJob = {
	id: "job",
	status: "ready",
	createdBy: "admin",
	createdAt: "2026-01-01",
	updatedAt: "2026-01-01",
	scanned: 1,
	repaired: 0,
	conflicts: 0,
	upperCreatedAt: null,
	upperId: null,
	cursorCreatedAt: null,
	cursorId: null,
	includeReviewed: false,
	error: null,
};
function fixture() {
	const service = {
		probe: mock(async () => ({ hasPending: true, job: null })),
		latestJob: mock(async () => job),
		getJob: mock(async (id: string) => (id === "missing" ? null : job)),
		listCandidates: mock(async (_id: string, _cursor?: string) => ({
			records: [],
			nextCursor: null,
		})),
		start: mock(
			async (
				_user: string,
				_reviewed: boolean,
				_overrides?: TokenRepairProtocolOverride[],
				_sourceJobId?: string,
			) => job,
		),
		cancel: mock(async (_id: string) => job),
		apply: mock(async (_id: string, _ids: string[], _user: string) => ({
			applied: 1,
			conflicts: 0,
		})),
	};
	const auth = async (c: Context, next: Next) => {
		const role = c.req.header("x-test-role");
		if (role === "admin" || role === "user")
			c.set("user", { sub: "actual-user", role, iat: 1, exp: 9999999999 });
		if (c.req.header("x-test-oauth"))
			c.set("auth", {
				type: "oauth",
				user: c.get("user"),
				oauth: {
					tokenId: "t",
					clientId: "c",
					oauthClientId: "c",
					grantId: null,
					refreshFamilyId: null,
					expiresAt: "2099-01-01",
					scopes: ["admin"],
				},
			});
		await next();
	};
	const app = new Hono();
	app.onError((error, c) =>
		error instanceof HTTPException
			? error.getResponse()
			: c.json(
					{ error: "Request rejected" },
					error instanceof AppError ? (error.statusCode as 400) : 400,
				),
	);
	app.route("/", createUsageTokenRepairRoutes({ service, requireAuth: auth }));
	return { app, service };
}
const admin = { "x-test-role": "admin", "content-type": "application/json" };

describe("historical token repair administration boundary", () => {
	test.each([
		undefined,
		"user",
	])("probe rejects non-admin %s without touching storage", async (role) => {
		const { app, service } = fixture();
		expect(
			(await app.request("/probe", { headers: role ? { "x-test-role": role } : {} })).status,
		).toBe(role ? 403 : 401);
		expect(service.probe).not.toHaveBeenCalled();
	});
	test("OAuth admin credentials cannot perform maintenance", async () => {
		const { app, service } = fixture();
		expect(
			(await app.request("/probe", { headers: { ...admin, "x-test-oauth": "yes" } })).status,
		).toBe(403);
		expect(service.probe).not.toHaveBeenCalled();
	});
	test("probe is read-only and start records the authenticated actor", async () => {
		const { app, service } = fixture();
		expect(await (await app.request("/probe", { headers: admin })).json()).toEqual({
			hasPending: true,
			job: null,
		});
		expect(service.start).not.toHaveBeenCalled();
		expect(
			(
				await app.request("/jobs", {
					method: "POST",
					headers: admin,
					body: JSON.stringify({ includeReviewed: true }),
				})
			).status,
		).toBe(202);
		expect(service.start).toHaveBeenCalledWith("actual-user", true, [], undefined);
	});
	test.each([
		{ candidateIds: ["c"] },
		{ candidateIds: ["c", "c"], confirm: true },
		{ candidateIds: ["c"], confirm: true, inputTokens: 0 },
		{ candidateIds: ["c"], confirm: true, syncTotals: true },
		{ candidateIds: ["c"], confirm: true, filePath: "/private/dump" },
		{
			candidateIds: Array.from({ length: 51 }, (_, i) => `c${i}`),
			confirm: true,
		},
	])("rejects unsafe write parameters %#", async (body) => {
		const { app, service } = fixture();
		expect(
			(
				await app.request("/jobs/job/apply", {
					method: "POST",
					headers: admin,
					body: JSON.stringify(body),
				})
			).status,
		).toBe(400);
		expect(service.apply).not.toHaveBeenCalled();
	});
	test("explicit confirmation only sends candidate IDs and actor", async () => {
		const { app, service } = fixture();
		expect(
			(
				await app.request("/jobs/job/apply", {
					method: "POST",
					headers: admin,
					body: JSON.stringify({ candidateIds: ["c"], confirm: true }),
				})
			).status,
		).toBe(200);
		expect(service.apply).toHaveBeenCalledWith("job", ["c"], "actual-user");
	});
	test("unknown jobs are not empty successful previews", async () => {
		const { app, service } = fixture();
		expect((await app.request("/jobs/missing/candidates", { headers: admin })).status).toBe(404);
		expect(service.listCandidates).not.toHaveBeenCalled();
	});
	test("start freezes administrator protocol choices with exact provider and model identities", async () => {
		const { app, service } = fixture();
		const protocolOverrides = [
			{ provider: "nug", model: "same-model", protocol: "openai" },
			{ provider: "other", model: "same-model", protocol: "anthropic" },
		];
		expect(
			(
				await app.request("/jobs", {
					method: "POST",
					headers: admin,
					body: JSON.stringify({ includeReviewed: true, protocolOverrides }),
				})
			).status,
		).toBe(202);
		expect(service.start).toHaveBeenCalledWith("actual-user", true, protocolOverrides, undefined);
	});
	test.each(
		[
			[{ provider: "nug", model: "m", protocol: "unknown" }],
			[{ provider: "nug", model: null, protocol: "openai" }],
			[{ provider: "nug", model: " ", protocol: "openai" }],
			[{ provider: "nug", model: "m", protocol: "openai", inputTokens: 0 }],
			[
				{ provider: "nug", model: "m", protocol: "openai" },
				{ provider: "nug", model: "m", protocol: "anthropic" },
			],
			Array.from({ length: 33 }, (_, i) => ({
				provider: "nug",
				model: `m${i}`,
				protocol: "openai",
			})),
			Array.from({ length: 32 }, (_, i) => ({
				provider: "中".repeat(128),
				model: `m${i}${"文".repeat(250)}`,
				protocol: "openai",
			})),
		].map((protocolOverrides) => ({ protocolOverrides })),
	)("invalid or excessive protocol choices are rejected before starting %#", async ({
		protocolOverrides,
	}) => {
		const { app, service } = fixture();
		const response = await app.request("/jobs", {
			method: "POST",
			headers: admin,
			body: JSON.stringify({ protocolOverrides }),
		});
		expect([400, 413]).toContain(response.status);
		expect(service.start).not.toHaveBeenCalled();
	});

	test("reanalysis forwards a fixed source batch and requires reviewed records", async () => {
		const { app, service } = fixture();
		expect(
			(
				await app.request("/jobs", {
					method: "POST",
					headers: admin,
					body: JSON.stringify({
						includeReviewed: true,
						sourceJobId: "previous-job",
						protocolOverrides: [],
					}),
				})
			).status,
		).toBe(202);
		expect(service.start).toHaveBeenCalledWith("actual-user", true, [], "previous-job");
		const invalid = fixture();
		expect(
			(
				await invalid.app.request("/jobs", {
					method: "POST",
					headers: admin,
					body: JSON.stringify({ sourceJobId: "previous-job" }),
				})
			).status,
		).toBe(400);
		expect(invalid.service.start).not.toHaveBeenCalled();
	});

	test("maintenance request bodies are bounded before parsing", async () => {
		const { app, service } = fixture();
		expect(
			(await app.request("/jobs", { method: "POST", headers: admin, body: "x".repeat(33000) }))
				.status,
		).toBe(413);
		expect(service.start).not.toHaveBeenCalled();
	});
});
