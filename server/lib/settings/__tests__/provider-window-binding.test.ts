import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { Hono } from "hono";
import { settingsRoutes } from "../../../routes/settings";
import {
	getEffectiveModelCard,
	getEffectiveModelProfile,
	getModelCardSnapshot,
	mutateModelCard,
} from "../../model-catalog";
import { deleteNugCachedModels, setNugCachedModels } from "../../nug-model-cache";
import {
	getModelContextWindow,
	narraforkDir,
	reloadSettings,
	saveSettings,
	settings,
} from "../index";

/**
 * Regression for "provider page set gpt-6-astra to 272000 but the narrator still used
 * 1.1M": a provider-page window must apply to the model value it was entered for,
 * whatever else the catalog holds for that connection and however the value resolves.
 */
const path = join(narraforkDir, "settings.json");
const original = readFileSync(path, "utf8");
afterEach(() => {
	deleteNugCachedModels("window-nug");
	writeFileSync(path, original);
	reloadSettings();
});
const app = new Hono<{ Variables: { user: { sub: string; role: string } } }>();
app.use("*", async (c, next) => {
	c.set("user", { sub: "window-test", role: "admin" });
	await next();
});
app.onError((error, c) => c.json({ error: error.message }, 400));
app.route("/settings", settingsRoutes);
function saveFromProviderPage(model: string, window: number) {
	return app.request("/settings", {
		method: "PATCH",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			agent: { modelContextWindows: { ...settings.agent.modelContextWindows, [model]: window } },
		}),
	});
}

describe("provider-page context windows", () => {
	test("compose with a catalog-editor binding for the same connection", async () => {
		const model = "codex:gpt-6-astra";
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(1_050_000);
		// What the price modal's "edit binding" saves: the request's own selector.
		const query = getEffectiveModelCard(model).resolvedQuery;
		mutateModelCard({
			baseRevision: getModelCardSnapshot().local.revision,
			action: "upsert-binding",
			binding: {
				id: "astra-editor",
				upstreamModelId: query.upstreamModelId,
				providerId: query.providerId,
				overrides: { max_output_tokens: 64_000 },
			},
		});
		const response = await saveFromProviderPage(model, 272_000);
		expect(response.status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(272_000);
		const profile = getEffectiveModelProfile(model);
		expect(profile.maxOutputTokens).toBe(64_000);
		expect(profile.contextWindowSource?.id).toBe("legacy-window:codex:gpt-6-astra");
		// Editors keep opening the binding they own.
		expect(getEffectiveModelCard(model).bindingId).toBe("astra-editor");
		expect(settings.agent.modelContextWindows[model]).toBe(272_000);
	});

	test("follow the model value when its gateway route resolves differently later", async () => {
		const nug = {
			id: "window-nug",
			name: "Gateway",
			prefix: "gw",
			apiKey: "unused",
			baseUrl: "https://gateway.invalid",
			defaultModel: "",
		};
		const draft = structuredClone(settings);
		draft.nugProviders = [nug];
		saveSettings(draft);
		const model = "gw:chan:gpt-6-astra";
		// Saved before the gateway's model list is cached: the binding's selector is the
		// opaque id, not the channel route the runtime will see later.
		expect((await saveFromProviderPage(model, 272_000)).status).toBe(200);
		setNugCachedModels(nug.id, [
			{
				id: "chan:gpt-6-astra",
				model: "gpt-6-astra",
				channel: "chan",
				channelType: "codex",
				contextLength: 1_050_000,
			},
		]);
		expect(getEffectiveModelCard(model).resolvedQuery.channelId).toBe("chan");
		expect(getModelContextWindow("chan:gpt-6-astra", "gw")).toBe(272_000);
		expect(getEffectiveModelProfile(model).contextWindowSource?.layer).toBe("local-binding");
	});
});
