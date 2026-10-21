import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { ModelWindowsResponse } from "@shared/model-catalog/schema/api";
import { Hono } from "hono";
import { modelCatalogRoutes } from "../../../routes/model-catalog";
import {
	getEffectiveModelCard,
	getEffectiveModelProfile,
	getModelCardSnapshot,
	getModelCatalogRevision,
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
 * A context window is set either for one provider entry (its full model value) or for
 * the model under every provider. Regression for "provider page set gpt-6-astra to
 * 272000 but the narrator still used 1.1M": an entry's own window applies to the model
 * value it was entered for, whatever else the catalog holds and however it resolves.
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
app.route("/model-catalog", modelCatalogRoutes);

function setWindow(scope: "connection" | "model", target: string, contextWindow: number | null) {
	return app.request("/model-catalog/v2/windows/set", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			baseRevision: getModelCatalogRevision(),
			scope,
			target,
			contextWindow,
		}),
	});
}
async function windows(...models: string[]) {
	const response = await app.request("/model-catalog/v2/windows", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ models }),
	});
	expect(response.status).toBe(200);
	return ((await response.json()) as ModelWindowsResponse).windows;
}

describe("provider-model and model-wide context windows", () => {
	test("a provider entry's own window beats the model-wide one, which beats the preset", async () => {
		const codex = "codex:gpt-6-astra";
		const openai = "openai:gpt-6-astra";
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(1_050_000);

		expect((await setWindow("model", "gpt-6-astra", 400_000)).status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(400_000);
		expect(getModelContextWindow("gpt-6-astra", "openai")).toBe(400_000);

		expect((await setWindow("connection", codex, 272_000)).status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(272_000);
		expect(getModelContextWindow("gpt-6-astra", "openai")).toBe(400_000);

		const [own, other] = await windows(codex, openai);
		expect(own).toMatchObject({
			model: codex,
			contextWindow: 272_000,
			source: "own",
			own: 272_000,
			inherited: 400_000,
			inheritedSource: "model",
			modelId: "gpt-6-astra",
			modelWide: 400_000,
		});
		// The model-wide editor names the entries that keep their own value.
		expect(other).toMatchObject({ contextWindow: 400_000, source: "model" });
		expect(other?.others).toEqual([{ model: codex, contextWindow: 272_000 }]);

		// Clearing the entry falls back to the model-wide value, not to the preset.
		expect((await setWindow("connection", codex, null)).status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(400_000);
		expect((await setWindow("model", "gpt-6-astra", null)).status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(1_050_000);
		expect(settings.agent.modelCatalog?.local.connections).toBeUndefined();
	});

	test("an unknown model can only be set for its provider entry", async () => {
		const [info] = await windows("codex:not-a-catalog-model-xyz");
		expect(info?.modelId).toBeUndefined();
		expect((await setWindow("connection", "codex:not-a-catalog-model-xyz", 64_000)).status).toBe(
			200,
		);
		expect(getModelContextWindow("not-a-catalog-model-xyz", "codex")).toBe(64_000);
		expect((await setWindow("model", "not-a-catalog-model-xyz", 64_000)).status).toBe(400);
	});

	test("rejects stale revisions and out-of-range values", async () => {
		const stale = await app.request("/model-catalog/v2/windows/set", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({
				baseRevision: getModelCatalogRevision() + 1,
				scope: "connection",
				target: "codex:gpt-6-astra",
				contextWindow: 272_000,
			}),
		});
		expect(stale.status).toBe(409);
		expect((await setWindow("connection", "codex:gpt-6-astra", 12)).status).toBe(400);
		expect((await setWindow("connection", " codex:gpt-6-astra", 272_000)).status).toBe(400);
	});

	test("coexists with a catalog-editor binding for the same connection", async () => {
		const model = "codex:gpt-6-astra";
		// What the price modal's "edit binding" saves: the request's own selector.
		const query = getEffectiveModelCard(model).resolvedQuery;
		mutateModelCard({
			baseRevision: getModelCardSnapshot().local.revision,
			action: "upsert-binding",
			binding: {
				id: "astra-editor",
				upstreamModelId: query.upstreamModelId,
				providerId: query.providerId,
				overrides: { max_output_tokens: 64_000, working_context_tokens: 500_000 },
			},
		});
		expect((await setWindow("connection", model, 272_000)).status).toBe(200);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(272_000);
		const profile = getEffectiveModelProfile(model);
		expect(profile.maxOutputTokens).toBe(64_000);
		expect(profile.contextWindowSource).toMatchObject({ layer: "local-connection", id: model });
		expect(getEffectiveModelCard(model).bindingId).toBe("astra-editor");
		const [info] = await windows(model);
		expect(info).toMatchObject({ inherited: 500_000, inheritedSource: "binding" });
	});

	test("follows the model value when its gateway route resolves differently later", async () => {
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
		// Saved before the gateway's model list is cached.
		expect((await setWindow("connection", model, 272_000)).status).toBe(200);
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
		const [info] = await windows(model);
		expect(info).toMatchObject({ source: "own", inheritedSource: "discovered" });
	});

	test("provider-page window bindings from older builds become provider-entry windows", () => {
		const model = "codex:gpt-6-astra";
		const stored = JSON.parse(readFileSync(path, "utf8"));
		const query = getEffectiveModelCard(model).resolvedQuery;
		stored.agent.modelCatalog.local.bindings = [
			...(stored.agent.modelCatalog.local.bindings ?? []),
			{
				id: `legacy-window:${model}`,
				upstreamModelId: query.upstreamModelId,
				providerId: query.providerId,
				modelId: "gpt-6-astra",
				overrides: { working_context_tokens: 272_000 },
			},
		];
		stored.agent.modelCatalog.local.legacyFields = {
			...stored.agent.modelCatalog.local.legacyFields,
			[`binding:legacy-window:${model}`]: ["working_context_tokens"],
		};
		writeFileSync(path, JSON.stringify(stored));
		const loaded = reloadSettings();
		const local = loaded.agent.modelCatalog?.local;
		expect(local?.bindings?.some((b) => b.id.startsWith("legacy-window:"))).toBe(false);
		expect(local?.connections?.[model]).toEqual({ working_context_tokens: 272_000 });
		expect(local?.legacyFields?.[`connection:${model}`]).toEqual(["working_context_tokens"]);
		expect(getModelContextWindow("gpt-6-astra", "codex")).toBe(272_000);
		expect(getEffectiveModelProfile(model).contextWindowSource?.legacy).toBe(true);
		// The migrated document is written back in its new form.
		const disk = JSON.parse(readFileSync(path, "utf8"));
		expect(disk.agent.modelCatalog.local.connections[model]).toEqual({
			working_context_tokens: 272_000,
		});
	});
});
