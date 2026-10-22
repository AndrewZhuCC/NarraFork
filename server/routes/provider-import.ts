import {
	LOCAL_IMPORT_MAX_RESOLVE_IDS,
	type LocalProviderDetectResponse,
	type LocalProviderResolveResponse,
} from "@shared/local-provider-import";
import { Hono } from "hono";
import { z } from "zod";
import { ValidationError } from "../lib/errors";
import { settings } from "../lib/settings";
import { requireAdmin } from "../middleware/auth";
import {
	detectLocalProviderSources,
	providerIdentity,
	scanLocalProviderConfigs,
	summarizeLocalScan,
} from "../services/local-provider-import";

/**
 * Import provider settings from other AI tools on the server machine.
 *
 * Both endpoints are admin-only POSTs: reading another tool's config is an
 * explicit, user-authorized action, never something a page load triggers.
 * `scan` returns masked keys only; `resolve` returns the real key for the
 * candidates the admin picked, so the UI can save them through the normal
 * add-provider flow (same prefix rules and model refresh as manual entry).
 */
export const providerImportRoutes = new Hono();

const resolveSchema = z.object({
	ids: z.array(z.string().min(1).max(128)).min(1).max(LOCAL_IMPORT_MAX_RESOLVE_IDS),
});

function configuredIdentities(): Set<string> {
	return new Set(
		(settings.customApiProviders ?? [])
			.filter((provider) => provider.apiKey && provider.baseUrl)
			.map((provider) => providerIdentity(provider.protocol, provider.baseUrl, provider.apiKey)),
	);
}

// Existence checks only (no file is read): lets the add page decide whether to
// offer the import option at all.
providerImportRoutes.get("/local/detect", requireAdmin, async (c) => {
	const response: LocalProviderDetectResponse = { sources: await detectLocalProviderSources() };
	c.header("Cache-Control", "no-store");
	return c.json(response);
});

providerImportRoutes.post("/local/scan", requireAdmin, async (c) => {
	const scan = await scanLocalProviderConfigs();
	c.header("Cache-Control", "no-store");
	return c.json(summarizeLocalScan(scan, configuredIdentities()));
});

providerImportRoutes.post("/local/resolve", requireAdmin, async (c) => {
	const parsed = resolveSchema.safeParse(await c.req.json().catch(() => null));
	if (!parsed.success) throw new ValidationError(parsed.error.message);
	const wanted = new Set(parsed.data.ids);
	const scan = await scanLocalProviderConfigs();
	const providers = scan.candidates
		.filter((candidate) => wanted.has(candidate.id))
		.map(({ id, name, protocol, baseUrl, apiKey }) => ({ id, name, protocol, baseUrl, apiKey }));
	const found = new Set(providers.map((provider) => provider.id));
	const response: LocalProviderResolveResponse = {
		providers,
		missing: [...wanted].filter((id) => !found.has(id)),
	};
	// Keys must never be cached by the browser or an intermediary.
	c.header("Cache-Control", "no-store");
	return c.json(response);
});
