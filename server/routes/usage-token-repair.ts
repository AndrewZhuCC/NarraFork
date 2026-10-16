import { NotFoundError } from "@server/lib/errors";
import {
	tokenRepairApplySchema,
	tokenRepairIdSchema,
	tokenRepairPageSchema,
	tokenRepairStartSchema,
} from "@server/lib/validators/usage-token-repair";
import { requireAdmin, requireAuth } from "@server/middleware/auth";
import { usageTokenRepairService } from "@server/services/usage-token-repair-service";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";

export function createUsageTokenRepairRoutes(
	options: {
		service?: typeof usageTokenRepairService;
		requireAuth?: typeof requireAuth;
		requireAdmin?: typeof requireAdmin;
	} = {},
) {
	const routes = new Hono();
	const service = options.service ?? usageTokenRepairService;
	routes.use("*", options.requireAuth ?? requireAuth, options.requireAdmin ?? requireAdmin);
	routes.use("*", bodyLimit({ maxSize: 32 * 1024 }));
	const jobId = (id: string) => tokenRepairIdSchema.parse(id);
	routes.get("/probe", async (c) => c.json(await service.probe()));
	routes.get("/jobs", async (c) => c.json({ job: await service.latestJob() }));
	routes.post("/jobs", async (c) => {
		const input = tokenRepairStartSchema.parse(await c.req.json());
		return c.json(
			{
				job: await service.start(
					c.get("user").sub,
					input.includeReviewed,
					input.protocolOverrides,
					input.sourceJobId,
				),
			},
			202,
		);
	});
	routes.get("/jobs/:jobId", async (c) => {
		const job = await service.getJob(jobId(c.req.param("jobId")));
		if (!job) throw new NotFoundError("Token repair job", c.req.param("jobId"));
		return c.json({ job });
	});
	routes.get("/jobs/:jobId/candidates", async (c) => {
		const id = jobId(c.req.param("jobId"));
		const { cursor } = tokenRepairPageSchema.parse(c.req.query());
		if (!(await service.getJob(id)))
			throw new NotFoundError("Token repair job", c.req.param("jobId"));
		return c.json(await service.listCandidates(id, cursor));
	});
	routes.post("/jobs/:jobId/cancel", async (c) => {
		const job = await service.cancel(jobId(c.req.param("jobId")));
		if (!job) throw new NotFoundError("Token repair job", c.req.param("jobId"));
		return c.json({ job });
	});
	routes.post("/jobs/:jobId/apply", async (c) => {
		const id = jobId(c.req.param("jobId"));
		const input = tokenRepairApplySchema.parse(await c.req.json());
		return c.json(await service.apply(id, input.candidateIds, c.get("user").sub));
	});
	return routes;
}
