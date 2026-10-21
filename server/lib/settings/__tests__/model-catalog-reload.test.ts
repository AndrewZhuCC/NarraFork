import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import {
	getEffectiveModelProfile,
	getModelCardSnapshot,
	getModelCatalogRevision,
	mutateModelCard,
	setModelWindow,
} from "../../model-catalog";
import { narraforkDir, reloadSettings, reloadSettingsIfChanged, settings } from "../index";

/**
 * Regression for "provider context window has no effect" and "Unknown field ...
 * working_context_tokens" after editing settings: every save is followed by a hot
 * reload (the settings-file watcher sees the write), and the reload used to put the
 * stored catalog into memory without the conversion startup performed.
 */
const path = join(narraforkDir, "settings.json");
const model = "anthropic:claude-sonnet-4-5";
let original: string;

beforeEach(() => {
	original = readFileSync(path, "utf8");
});
afterEach(() => {
	writeFileSync(path, original);
	reloadSettings();
});

function saveWindow(value: number | undefined) {
	setModelWindow({
		baseRevision: getModelCatalogRevision(),
		scope: "connection",
		target: model,
		contextWindow: value ?? null,
	});
}
const ownWindow = (local = settings.agent.modelCatalog?.local) =>
	local?.connections?.[model]?.working_context_tokens;

describe("model catalog survives hot reload", () => {
	test("provider-page windows apply, reload and re-apply; catalog edits keep working", () => {
		const preset = getEffectiveModelProfile(model).contextWindow;
		saveWindow(77_777);
		expect(getEffectiveModelProfile(model).contextWindow).toBe(77_777);

		// What the watcher does ~300 ms after every save.
		reloadSettings();
		expect(settings.agent.modelCatalog?.schemaVersion).toBe(2);
		expect(getEffectiveModelProfile(model).contextWindow).toBe(77_777);
		expect(getEffectiveModelProfile(model).contextWindowSource?.layer).toBe("local-connection");
		expect(ownWindow()).toBe(77_777);

		// A second page edit after the reload must land, not be silently dropped.
		saveWindow(55_555);
		reloadSettings();
		expect(getEffectiveModelProfile(model).contextWindow).toBe(55_555);
		const disk = JSON.parse(readFileSync(path, "utf8"));
		expect(disk.agent.modelContextWindows).toBeUndefined();
		expect(ownWindow(disk.agent.modelCatalog.local)).toBe(55_555);

		// The catalog editor validates the same document without tripping on raw keys.
		expect(() =>
			mutateModelCard({
				baseRevision: getModelCardSnapshot().local.revision,
				action: "patch",
				target: "model",
				targetId: "claude-sonnet-4-5",
				patch: { set: { max_output_tokens: 32_000 } },
			}),
		).not.toThrow();
		reloadSettings();
		expect(getEffectiveModelProfile(model).maxOutputTokens).toBe(32_000);
		expect(getEffectiveModelProfile(model).contextWindow).toBe(55_555);

		saveWindow(undefined);
		reloadSettings();
		expect(getEffectiveModelProfile(model).contextWindow).toBe(preset);
	});

	test("the file watcher skips this process's own writes but not external edits", () => {
		saveWindow(44_444);
		expect(reloadSettingsIfChanged()).toBe(false);
		const stored = JSON.parse(readFileSync(path, "utf8"));
		stored.agent.defaultReasoningEffort = "low";
		writeFileSync(path, JSON.stringify(stored, null, 2));
		expect(reloadSettingsIfChanged()).toBe(true);
		expect(settings.agent.defaultReasoningEffort).toBe("low");
		expect(getEffectiveModelProfile(model).contextWindow).toBe(44_444);
		expect(reloadSettingsIfChanged()).toBe(false);
	});

	test("an unreadable file keeps the running configuration instead of half-applying", () => {
		saveWindow(66_666);
		const before = structuredClone(settings.agent.modelCatalog);
		writeFileSync(path, "{ not json");
		expect(() => reloadSettings()).not.toThrow();
		expect(settings.agent.modelCatalog).toEqual(before);
		expect(getEffectiveModelProfile(model).contextWindow).toBe(66_666);
	});

	test("hot reload holds exactly what a cold start holds for the same file", async () => {
		saveWindow(88_888);
		// Leave an old-format window map in the file, as an older build wrote it.
		const stored = JSON.parse(readFileSync(path, "utf8"));
		stored.agent.modelContextWindows = { [model]: 99_999 };
		writeFileSync(path, JSON.stringify(stored, null, 2));
		const fileBefore = readFileSync(path, "utf8");

		const settingsModule = pathToFileURL(join(import.meta.dir, "..", "index.ts")).href;
		const home = process.env.NARRAFORK_HOME;
		// The cold start runs first so both read the same pre-import file.
		writeFileSync(path, fileBefore);
		const child = Bun.spawn(
			[
				process.execPath,
				"--eval",
				`const { settings } = await import(${JSON.stringify(settingsModule)});
				console.log("COLD=" + JSON.stringify({ catalog: settings.agent.modelCatalog, windows: settings.agent.modelContextWindows ?? null }));`,
			],
			{ env: { ...process.env, NARRAFORK_HOME: home }, stdout: "pipe", stderr: "pipe" },
		);
		const [code, stdout] = await Promise.all([child.exited, new Response(child.stdout).text()]);
		expect(code).toBe(0);
		const cold = JSON.parse(
			stdout
				.split("\n")
				.find((line) => line.startsWith("COLD="))!
				.slice(5),
		);

		writeFileSync(path, fileBefore);
		reloadSettings();
		expect({
			catalog: settings.agent.modelCatalog,
			windows: (settings.agent as { modelContextWindows?: unknown }).modelContextWindows ?? null,
		}).toEqual(cold);
		// The old map is imported as the entry's own window and not kept beside it.
		expect(cold.windows).toBeNull();
		expect(ownWindow()).toBe(99_999);
	}, 30_000);
});
