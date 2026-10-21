import type { ModelCardMutation } from "@shared/model-catalog/card-local";
import type { ModelQuery } from "@shared/model-catalog/schema/catalog";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { buildAppErrorResponse } from "../lib/app-error-response";
import { logger } from "../lib/logger";
import {
	applyModelCatalogUpdate,
	CatalogRevisionConflict,
	checkModelCatalogUpdate,
	exportModelCatalogUserLayer,
	getEffectiveModelCard,
	getModelCardSnapshot,
	mutateModelCard,
	resolveEffectiveModelCard,
	rollbackModelCatalog,
	setModelCatalogUpdateSettings,
	setModelWindow,
} from "../lib/model-catalog";
import {
	CATALOG_IO_ERROR_BODY,
	isHostIoError,
	sanitizeCatalogErrorText,
} from "../lib/model-catalog/error-text";
import { describeModelWindows, MAX_WINDOW_MODELS } from "../lib/model-catalog/windows";
import { requireAdmin } from "../middleware/auth";

const resolveBodyLimit = bodyLimit({
	maxSize: 1024 * 1024,
	onError: (c) => c.json({ error: "Model card request exceeds 1 MiB" }, 413),
});

export const modelCatalogRoutes = new Hono();
modelCatalogRoutes.onError((error, c) => {
	const appErrorResponse = buildAppErrorResponse(error, c);
	if (appErrorResponse) return appErrorResponse;
	if (error instanceof CatalogRevisionConflict) {
		return c.json(
			{
				error: error.message,
				code: "CATALOG_REVISION_CONFLICT",
				snapshot: getModelCardSnapshot(),
			},
			409,
		);
	}
	// Unexpected errors may embed host absolute paths (`~/.narrafork/...`). Log the
	// original message; the wire body must stay free of OS usernames and host paths.
	logger.error("model-catalog request failed", {
		message: error instanceof Error ? error.message : String(error),
	});
	if (isHostIoError(error)) return c.json(CATALOG_IO_ERROR_BODY, 400);
	return c.json(
		{
			error: sanitizeCatalogErrorText(error) || CATALOG_IO_ERROR_BODY.error,
			code: "CATALOG_ERROR",
		},
		400,
	);
});
modelCatalogRoutes.get("/v2", (c) => c.json(getModelCardSnapshot()));
modelCatalogRoutes.get("/v2/resolve", (c) => {
	const model = c.req.query("model");
	if (!model) throw new Error("model is required");
	return c.json(getEffectiveModelCard(model));
});
modelCatalogRoutes.post("/v2/resolve", resolveBodyLimit, async (c) => {
	const body = await c.req.json<{ query?: ModelQuery; model?: string }>();
	if (body.model) return c.json(getEffectiveModelCard(body.model));
	if (!body.query || typeof body.query.upstreamModelId !== "string")
		throw new Error("query.upstreamModelId is required");
	return c.json(resolveEffectiveModelCard(body.query));
});
/** Context windows of provider entries, with the scopes the provider page can set. */
modelCatalogRoutes.post("/v2/windows", resolveBodyLimit, async (c) => {
	const body = await c.req.json<{ models?: unknown }>();
	if (
		!Array.isArray(body.models) ||
		body.models.length > MAX_WINDOW_MODELS ||
		body.models.some((model) => typeof model !== "string")
	)
		throw new Error(`models must be an array of at most ${MAX_WINDOW_MODELS} model values`);
	return c.json(await describeModelWindows(body.models as string[]));
});
/** Set or clear a working window for one provider entry (`connection`) or model-wide (`model`). */
modelCatalogRoutes.post("/v2/windows/set", requireAdmin, resolveBodyLimit, async (c) =>
	c.json(setModelWindow(await c.req.json())),
);
modelCatalogRoutes.get("/export", requireAdmin, (c) => c.json(exportModelCatalogUserLayer()));
modelCatalogRoutes.post("/v2/mutate", requireAdmin, resolveBodyLimit, async (c) =>
	c.json(mutateModelCard(await c.req.json<ModelCardMutation>())),
);
modelCatalogRoutes.post("/updates/check", requireAdmin, async (c) =>
	c.json(await checkModelCatalogUpdate()),
);
modelCatalogRoutes.post("/updates/apply", requireAdmin, async (c) => {
	const body = await c.req.json<{ version?: string }>();
	return c.json(applyModelCatalogUpdate(body.version));
});
modelCatalogRoutes.post("/updates/rollback", requireAdmin, async (c) => {
	const body = await c.req.json<{ version: string }>();
	if (typeof body.version !== "string") throw new Error("version is required");
	return c.json(rollbackModelCatalog(body.version));
});
modelCatalogRoutes.patch("/updates/settings", requireAdmin, async (c) =>
	c.json(setModelCatalogUpdateSettings(await c.req.json())),
);
