import { afterEach, describe, expect, it } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { normalizeModelContextWindowOverrides } from "@shared/model-context-window";
import { Hono } from "hono";
import { legacyWindowProjection } from "../../../server/lib/model-catalog";
import { getNarraforkHome } from "../../../server/lib/narrafork-home";
import { getDefaults, reloadSettings, saveSettings, settings } from "../../../server/lib/settings";
import { normalizeModelContextWindows } from "../../../server/lib/settings/model-context-windows";
import { settingsRoutes, updateSettingsSchema } from "../../../server/routes/settings";

const snapshot = structuredClone(settings);
const settingsPath = resolve(getNarraforkHome(), "settings.json");
const original = readFileSync(settingsPath, "utf8");
afterEach(() => {
	saveSettings(structuredClone(snapshot));
	writeFileSync(settingsPath, original);
});

const app = new Hono<{ Variables: { user: { sub: string; role: string } } }>();
app.use("*", async (c, next) => {
	c.set("user", { sub: "context-window-test-admin", role: "admin" });
	await next();
});
app.onError((error, c) => c.json({ error: error.message }, 400));
app.route("/settings", settingsRoutes);
/** The windows settings.json holds: in the catalog's bindings, never as a separate map. */
function diskWindows() {
	const disk = JSON.parse(readFileSync(settingsPath, "utf8"));
	expect(disk.agent.modelContextWindows).toBeUndefined();
	return legacyWindowProjection(disk.agent.modelCatalog.local);
}
function patch(windows: unknown) {
	return app.request("/settings", {
		method: "PATCH",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ agent: { modelContextWindows: windows, providerOrder: ["test"] } }),
	});
}

async function coldStartContextWindows(home: string, models: string[]) {
	const settingsModule = pathToFileURL(resolve("server/lib/settings/index.ts")).href;
	const budgetsModule = pathToFileURL(resolve("server/lib/agent/provider-model-metadata.ts")).href;
	const child = Bun.spawn(
		[
			process.execPath,
			"--eval",
			`const { settings } = await import(${JSON.stringify(settingsModule)});
			const { modelTokenBudgets } = await import(${JSON.stringify(budgetsModule)});
			console.log("COLD_START_RESULT=" + JSON.stringify({
				windows: settings.agent.modelContextWindows,
				budgets: Object.fromEntries(${JSON.stringify(models)}.map(model => [model, modelTokenBudgets(model)])),
			}));`,
		],
		{
			cwd: resolve("."),
			env: { ...process.env, NARRAFORK_HOME: home },
			stdin: "ignore",
			stdout: "pipe",
			stderr: "pipe",
		},
	);
	let timedOut = false;
	const timer = setTimeout(() => {
		timedOut = true;
		child.kill("SIGKILL");
	}, 15_000);
	// Drain both streams concurrently and enforce a budget before retaining bytes.
	const readBounded = async (stream: ReadableStream<Uint8Array>) => {
		const reader = stream.getReader();
		const chunks: Uint8Array[] = [];
		let bytes = 0;
		try {
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				bytes += value.byteLength;
				if (bytes > 64 * 1024) {
					child.kill("SIGKILL");
					throw new Error("Cold-start output exceeded 64 KiB");
				}
				chunks.push(value);
			}
			return Buffer.concat(chunks).toString("utf8");
		} finally {
			reader.releaseLock();
		}
	};
	try {
		const [code, stdout, stderr] = await Promise.all([
			child.exited,
			readBounded(child.stdout),
			readBounded(child.stderr),
		]);
		if (timedOut || code !== 0)
			throw new Error(
				`Cold start failed (timeout=${timedOut}, exit=${code}): ${stderr}\n${stdout}`,
			);
		const result = stdout.split("\n").find((line) => line.startsWith("COLD_START_RESULT="));
		if (!result) throw new Error(`Missing cold-start result: ${stdout}\n${stderr}`);
		return JSON.parse(result.slice("COLD_START_RESULT=".length));
	} finally {
		clearTimeout(timer);
		if (child.exitCode === null) child.kill("SIGKILL");
		await child.exited;
	}
}

describe("context-window cold-start import", () => {
	for (const schemaVersion of [1, 2]) {
		it(`imports a stored v${schemaVersion} page map once and stops storing it`, async () => {
			const home = mkdtempSync(resolve(tmpdir(), "narrafork-context-cold-start-"));
			const path = resolve(home, "settings.json");
			const windows = {
				"openai:cold-large": 20_000_000,
				"openai:cold-small": 12,
				"openai:cold-invalid": 0,
				"openai:cold-edited": 128_000,
				"openai:cold-reset": 128_000,
			};
			const bindingWindows = {
				...windows,
				"openai:cold-invalid": 64_000,
				"openai:cold-edited": 96_000,
			};
			const entries = Object.entries(bindingWindows).filter(
				([model]) => model !== "openai:cold-reset",
			);
			try {
				// An empty JWT secret also exercises a real pre-bind startup save.
				const raw = getDefaults();
				writeFileSync(
					path,
					JSON.stringify({
						...raw,
						agent: {
							...raw.agent,
							modelContextWindows: windows,
							modelCatalog: {
								schemaVersion,
								migrationVersion: schemaVersion,
								autoApply: false,
								pinnedVersion: null,
								local: {
									revision: 7,
									bindings: entries.map(([model, window]) => ({
										id: `legacy-window:${model}`,
										providerId: "openai",
										upstreamModelId: model.slice("openai:".length),
										overrides:
											schemaVersion === 2
												? { working_context_tokens: window }
												: { limits: { contextWindow: window } },
									})),
									legacyFields: Object.fromEntries(
										entries.map(([model]) => [
											`binding:legacy-window:${model}`,
											[schemaVersion === 2 ? "working_context_tokens" : "limits.contextWindow"],
										]),
									),
								},
							},
						},
					}),
				);
				const first = await coldStartContextWindows(home, Object.keys(windows));
				// Where the stored map and a binding disagree, the map is what the provider
				// page showed (older builds dropped page edits after a hot reload), so it is
				// applied once. A key the map lacks is not a deletion: its binding stays and
				// the page now shows it. Afterwards the map is a view of the bindings.
				expect(first.windows).toEqual({
					"openai:cold-large": 10_000_000,
					"openai:cold-small": 256,
					"openai:cold-invalid": 64_000,
					"openai:cold-edited": 128_000,
					"openai:cold-reset": 128_000,
				});
				expect(first.budgets).toEqual({
					"openai:cold-large": { workingContextTokens: 10_000_000 },
					"openai:cold-small": { workingContextTokens: 256 },
					"openai:cold-invalid": { workingContextTokens: 64_000 },
					"openai:cold-edited": { workingContextTokens: 128_000 },
					"openai:cold-reset": { workingContextTokens: 128_000 },
				});
				const persisted = readFileSync(path, "utf8");
				const disk = JSON.parse(persisted);
				expect(disk.agent.modelContextWindows).toBeUndefined();
				expect(legacyWindowProjection(disk.agent.modelCatalog.local)).toEqual(first.windows);
				expect(disk.agent.modelCatalog.schemaVersion).toBe(2);
				expect(JSON.stringify(disk.agent.modelCatalog)).not.toContain("limits.contextWindow");
				const second = await coldStartContextWindows(home, Object.keys(windows));
				expect(second).toEqual(first);
				expect(readFileSync(path, "utf8")).toBe(persisted);
			} finally {
				rmSync(home, { recursive: true, force: true });
			}
		}, 40_000);
	}
});

describe("context-window automatic normalization", () => {
	const input = {
		"missing:valid": 128_000,
		min: 256,
		max: 10_000_000,
		tooBig: 20_000_000,
		tooSmall: 255,
		fraction: 256.9,
		numericString: " 64000 ",
		zero: 0,
		negative: -1,
		invalid: "unknown",
		empty: " ",
		nullValue: null,
		boolean: true,
		array: [1000],
		object: { value: 1000 },
	};
	const expected = {
		"missing:valid": 128_000,
		min: 256,
		max: 10_000_000,
		tooBig: 10_000_000,
		tooSmall: 256,
		fraction: 256,
		numericString: 64000,
	};

	it("clamps and rounds positive numbers, parses numeric strings and drops unusable overrides", () => {
		expect(
			normalizeModelContextWindowOverrides({ ...input, nan: NaN, infinity: Infinity }),
		).toEqual(expected);
		expect(normalizeModelContextWindowOverrides(expected)).toEqual(expected);
		expect(input.tooBig).toBe(20_000_000);
	});

	it("normalizes all request values rather than rejecting new or edited overrides", () => {
		const parsed = updateSettingsSchema.parse({ agent: { modelContextWindows: input } });
		expect(parsed.agent?.modelContextWindows).toEqual(expected);
		for (const value of [null, [], "invalid", 100]) {
			expect(
				updateSettingsSchema.parse({ agent: { modelContextWindows: value } }).agent
					?.modelContextWindows,
			).toEqual({});
		}
		// Unrelated patches must not clear an omitted override map.
		expect(
			updateSettingsSchema.parse({ agent: { providerOrder: ["test"] } }).agent,
		).not.toHaveProperty("modelContextWindows");
	});

	it("migrates stored values and preserves models absent from the current list", () => {
		const raw = getDefaults();
		raw.agent.modelContextWindows = { "retired:large": 20_000_000, "offline:valid": 128_000 };
		writeFileSync(settingsPath, JSON.stringify(raw));
		const loaded = reloadSettings();
		const result = { "retired:large": 10_000_000, "offline:valid": 128_000 };
		expect(loaded.agent.modelContextWindows).toEqual(result);
		expect(diskWindows()).toEqual(result);
		expect(normalizeModelContextWindows(loaded)).toBe(false);
	});

	it("saves new and edited invalid values and returns the normalized result to the page", async () => {
		settings.agent.modelContextWindows = { tooBig: 128_000 };
		const response = await patch(input);
		expect(response.status).toBe(200);
		const body = await response.json();
		expect(body.agent.modelContextWindows).toEqual(expected);
		expect(settings.agent.modelContextWindows).toEqual(expected);
		expect(settings.agent.providerOrder).toEqual(["test"]);
		expect(diskWindows()).toEqual(expected);
		const again = await patch(expected);
		expect(again.status).toBe(200);
		expect(settings.agent.modelContextWindows).toEqual(expected);
	});

	it("saves an old page replay without requiring historical-value tracking", async () => {
		settings.agent.modelContextWindows = {};
		const response = await patch({ "retired:large": 99_000_000 });
		expect(response.status).toBe(200);
		expect(settings.agent.modelContextWindows).toEqual({ "retired:large": 10_000_000 });
	});

	it("repairs malformed maps without blocking unrelated changes", async () => {
		for (const value of [null, [], "invalid", 100]) {
			const response = await patch(value);
			expect(response.status).toBe(200);
			expect(settings.agent.modelContextWindows).toEqual({});
		}
	});

	it("also normalizes internal writes before persistence", () => {
		const local = getDefaults();
		local.agent.modelContextWindows = {
			"discovered:large": 99_000_000,
			"discovered:good": 4096,
			invalid: -1,
		};
		saveSettings(local);
		const result = { "discovered:large": 10_000_000, "discovered:good": 4096 };
		expect(settings.agent.modelContextWindows).toEqual(result);
		expect(diskWindows()).toEqual(result);
	});
});
