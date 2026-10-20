import { describe, expect, test } from "bun:test";
import { Hono } from "hono";
import { AppError } from "../lib/errors";
import { storageRoutes } from "./storage";

const app = new Hono();
app.use("*", async (c, next) => {
	const role = c.req.header("x-test-role");
	if (role)
		c.set("user", {
			sub: "authenticated-admin",
			role: role as "admin" | "user",
			iat: 0,
			exp: 9999999999,
		});
	await next();
});
app.onError(
	(error) =>
		new Response(JSON.stringify({ error: error.message }), {
			status: error instanceof AppError ? error.statusCode : 500,
		}),
);
app.route("/storage", storageRoutes);

describe("retired workspace recovery routes", () => {
	test.each([
		["GET", "/workspace-barriers"],
		["POST", "/workspace-barriers/scope-a/observe"],
		["POST", "/workspace-barriers/scope-a/recover"],
		["POST", "/workspace-barriers/scope-a/retry-persistence"],
		["POST", "/workspace-barriers/scope-a/maintenance/begin"],
		["POST", "/workspace-barriers/scope-a/maintenance/observe"],
		["POST", "/workspace-barriers/scope-a/maintenance/commit"],
		["POST", "/workspace-barriers/scope-a/maintenance/cancel"],
	])("%s %s is absent for every role", async (method, path) => {
		for (const role of [null, "user", "admin"]) {
			const response = await app.request(`/storage${path}`, {
				method,
				headers: role ? { "x-test-role": role } : {},
			});
			expect(response.status).toBe(404);
		}
	});

	test("retains ordinary storage authorization and request validation", async () => {
		for (const path of ["/scan/start", "/database/preview", "/database/cleanup"]) {
			for (const [role, status] of [
				[null, 401],
				["user", 403],
				["admin", 400],
			] as const) {
				const response = await app.request(`/storage${path}`, {
					method: "POST",
					headers: {
						"Content-Type": "application/json",
						...(role ? { "x-test-role": role } : {}),
					},
					body: JSON.stringify({ categories: ["invalid"], target: "invalid" }),
				});
				expect(response.status).toBe(status);
			}
		}
	});
});
