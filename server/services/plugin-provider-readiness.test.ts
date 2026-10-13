import { describe, expect, spyOn, test } from "bun:test";
import { ProviderSourceRegistry } from "@server/lib/agent/provider-source-registry";
import {
	PluginProviderConfigService,
	type ProviderSecretStore,
	providerSecretKey,
} from "./plugin-provider-config-service";
import { listPluginProviderReadiness } from "./plugin-provider-readiness";
import {
	PluginProviderRegistry,
	type ProviderRegistryRegistration,
} from "./plugin-provider-registry";

const pluginId = "com.example.readiness";
const instanceId = `${pluginId}/demo`;
const signal = () => new AbortController().signal;
const noSideEffects = () => {
	throw new Error("Readiness must not write state, create adapters or activate plugins");
};

function register(
	registry: PluginProviderRegistry,
	overrides: Partial<ProviderRegistryRegistration> = {},
) {
	return registry.register({
		kind: "executable-plugin",
		pluginId,
		localId: "demo",
		providerInstanceId: instanceId,
		providerPrefix: "demo",
		displayName: "Demo Provider",
		createAdapter: noSideEffects,
		...overrides,
	});
}

function configService(
	registry: PluginProviderRegistry,
	hasSecret?: ProviderSecretStore["hasSecret"],
) {
	return new PluginProviderConfigService({
		registry,
		stateStore: {
			setProviderConfig: noSideEffects,
			setProviderPrefix: noSideEffects,
			setProviderProxy: noSideEffects,
			getCachedState: noSideEffects,
		},
		...(hasSecret
			? {
					secretStore: {
						hasSecret,
						setSecret: noSideEffects,
						deleteSecret: noSideEffects,
					},
				}
			: {}),
	});
}

function registerWithSecret(
	registry: PluginProviderRegistry,
	overrides: Partial<ProviderRegistryRegistration> = {},
) {
	return register(registry, {
		configSchema: {
			type: "object",
			properties: {
				endpoint: { type: "string" },
				apiKey: { type: "string", format: "password" },
			},
			required: ["endpoint", "apiKey"],
		},
		// Registration validates required keys. A legacy value must NOT satisfy vault presence.
		config: { endpoint: "https://example.invalid", apiKey: "registration-only-secret" },
		...overrides,
	});
}

async function readiness(
	registry: PluginProviderRegistry,
	hasSecret?: ProviderSecretStore["hasSecret"],
) {
	return listPluginProviderReadiness(registry, configService(registry, hasSecret), signal());
}

describe("plugin provider readiness", () => {
	test("an empty registry is empty and non-plugin registrations are excluded", async () => {
		const registry = new PluginProviderRegistry();
		expect(await readiness(registry)).toEqual([]);
		register(registry, { kind: "builtin", pluginId: undefined });
		register(registry, {
			kind: "compatible-api",
			pluginId: undefined,
			localId: "compatible",
			providerInstanceId: "compatible/other",
			providerPrefix: "compatible",
		});
		expect(await readiness(registry)).toEqual([]);
	});

	test("an empty/stale model catalog does not prevent local readiness or construct adapters", async () => {
		const registry = new PluginProviderRegistry();
		register(registry);
		expect(await readiness(registry)).toEqual([
			{ id: instanceId, prefix: "demo", name: "Demo Provider", ready: true },
		]);
	});

	test("an available registration without a factory is not ready", async () => {
		const registry = new PluginProviderRegistry();
		register(registry, { createAdapter: undefined });
		expect((await readiness(registry))[0].ready).toBe(false);
		registry.setRemoteProviderAdapterFactory(noSideEffects);
		expect((await readiness(registry))[0].ready).toBe(true);
	});

	test("disabled, incompatible and explicitly unavailable providers are not ready", async () => {
		for (const overrides of [
			{ disabled: true },
			{ compatible: false },
			{ unavailableReason: "runtime-unavailable" },
		]) {
			const registry = new PluginProviderRegistry();
			registerWithSecret(registry, overrides);
			expect((await readiness(registry, noSideEffects))[0].ready).toBe(false);
		}
	});

	test("live disable, enable and unregister changes are visible without cached readiness", async () => {
		const registry = new PluginProviderRegistry();
		register(registry);
		expect((await readiness(registry))[0].ready).toBe(true);
		registry.disablePlugin(pluginId);
		expect((await readiness(registry))[0].ready).toBe(false);
		registry.setPluginState(pluginId, { desiredState: "enabled", compatibility: "compatible" });
		expect((await readiness(registry))[0].ready).toBe(true);
		registry.unregister(instanceId);
		expect(await readiness(registry)).toEqual([]);
	});

	test("missing, empty or whitespace-only required plain config is not ready", async () => {
		const registry = new PluginProviderRegistry();
		register(registry, {
			configSchema: {
				type: "object",
				properties: { endpoint: { type: "string" } },
				required: ["endpoint"],
			},
			config: { endpoint: "" },
		});
		expect((await readiness(registry))[0].ready).toBe(false);
		registry.updateConfig(instanceId, { endpoint: "  " });
		expect((await readiness(registry))[0].ready).toBe(false);
		const getConfig = spyOn(registry, "getConfig").mockReturnValue({});
		try {
			expect((await readiness(registry))[0].ready).toBe(false);
		} finally {
			getConfig.mockRestore();
		}
		registry.updateConfig(instanceId, { endpoint: "configured" });
		expect((await readiness(registry))[0].ready).toBe(true);
	});

	test("required secrets use vault presence only, and never cross the API projection", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry);
		expect((await readiness(registry))[0].ready).toBe(false);
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
		const lookups: unknown[] = [];
		const result = await readiness(registry, (input) => {
			lookups.push(input);
			return true;
		});
		expect(result).toEqual([
			{ id: instanceId, prefix: "demo", name: "Demo Provider", ready: true },
		]);
		expect(lookups).toEqual([{ pluginId, key: providerSecretKey("demo", "apiKey") }]);
		expect(JSON.stringify(result)).not.toContain("apiKey");
		expect(JSON.stringify(result)).not.toContain("registration-only-secret");
	});

	test("all supported secret markers require vault presence", async () => {
		const markers: Record<string, string | boolean>[] = [
			{ format: "password" },
			{ writeOnly: true },
			{ "x-narrafork-secret": true },
		];
		for (const marker of markers) {
			const registry = new PluginProviderRegistry();
			registerWithSecret(registry, {
				configSchema: {
					type: "object",
					properties: { apiKey: { type: "string", ...marker } },
					required: ["apiKey"],
				},
				config: { apiKey: "legacy-value" },
			});
			expect((await readiness(registry, () => false))[0].ready).toBe(false);
			expect((await readiness(registry, () => true))[0].ready).toBe(true);
		}
	});

	test("secret patterns and lengths do not validate synthetic presence markers", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: {
					apiKey: { type: "string", format: "password", pattern: "^sk-", minLength: 64 },
					mode: { type: "string", enum: ["valid"] },
				},
				required: ["apiKey", "mode"],
			},
			config: { apiKey: `sk-${"a".repeat(80)}`, mode: "valid" },
		});
		const before = registry.get(instanceId)?.configSchema;
		expect((await readiness(registry, () => true))[0].ready).toBe(true);
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
		expect((await readiness(registry))[0].ready).toBe(false);
		expect(registry.get(instanceId)?.configSchema).toEqual(before);
		// The ordinary write-validation path still rejects an invalid real secret.
		expect(registry.validateConfig(instanceId, { apiKey: "short", mode: "valid" }).valid).toBe(
			false,
		);
		const secrets = new Set(["apiKey"]);
		expect(registry.validateConfigPresence(instanceId, { mode: "valid" }, secrets).valid).toBe(
			false,
		);
		expect(
			registry.validateConfigPresence(instanceId, { apiKey: 42, mode: "valid" }, secrets).valid,
		).toBe(false);
		expect(
			registry.validateConfigPresence(instanceId, { apiKey: "present", mode: "invalid" }, secrets)
				.valid,
		).toBe(false);
	});

	test("allOf and referenced secret constraints are projected without relaxing ordinary fields", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: { apiKey: { type: "string", format: "password" } },
				required: ["apiKey", "mode", "nested"],
				$defs: {
					credentials: {
						properties: { apiKey: { type: "string", minLength: 64, pattern: "^sk-" } },
					},
				},
				allOf: [
					{ properties: { apiKey: { minLength: 64 } } },
					{ $ref: "#/$defs/credentials" },
					{
						properties: {
							mode: { type: "string", pattern: "^valid$" },
							nested: {
								type: "object",
								properties: { apiKey: { type: "string", minLength: 64 } },
							},
						},
					},
				],
			},
			config: {
				apiKey: `sk-${"a".repeat(80)}`,
				mode: "valid",
				nested: { apiKey: "a".repeat(80) },
			},
		});
		const before = registry.get(instanceId)?.configSchema;
		expect((await readiness(registry, () => true))[0].ready).toBe(true);
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
		expect(registry.get(instanceId)?.configSchema).toEqual(before);
		const stored = registry.getConfig(instanceId);
		const getConfig = spyOn(registry, "getConfig");
		try {
			getConfig.mockReturnValue({ ...stored, mode: "invalid" });
			expect((await readiness(registry, () => true))[0].ready).toBe(false);
			getConfig.mockReturnValue({ ...stored, nested: { apiKey: "short" } });
			expect((await readiness(registry, () => true))[0].ready).toBe(false);
		} finally {
			getConfig.mockRestore();
		}
		expect(registry.validateConfig(instanceId, { ...stored, apiKey: "short" }).valid).toBe(false);
	});

	test("allOf and refs contribute required vault secrets without reading nested optional keys", async () => {
		const branches: Array<Record<string, string | string[]>> = [
			{ required: ["apiKey"] },
			{ $ref: "#/$defs/credentials" },
		];
		for (const branch of branches) {
			const registry = new PluginProviderRegistry();
			registerWithSecret(registry, {
				configSchema: {
					type: "object",
					properties: {
						apiKey: { type: "string", format: "password" },
						optionalKey: { type: "string", writeOnly: true },
						nested: { type: "object", required: ["optionalKey"] },
					},
					$defs: { credentials: { required: ["apiKey"] } },
					allOf: [branch],
				},
				config: { apiKey: "legacy-value", optionalKey: "legacy-optional" },
			});
			const lookups: string[] = [];
			const service = configService(registry, ({ key }) => {
				lookups.push(key);
				return true;
			});
			const before = registry.get(instanceId)?.configSchema;
			expect(await service.isConfigured(instanceId, signal())).toBe(true);
			expect(lookups).toEqual([providerSecretKey("demo", "apiKey")]);
			expect(await configService(registry, () => false).isConfigured(instanceId, signal())).toBe(
				false,
			);
			expect(registry.get(instanceId)?.configSchema).toEqual(before);
		}
	});

	test("anyOf vault requirements allow either secret and do not fail on a missing alternative", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: {
					apiKey: { type: "string", format: "password" },
					secondKey: { type: "string", writeOnly: true },
					optionalKey: { type: "string", writeOnly: true },
				},
				anyOf: [{ required: ["apiKey"] }, { required: ["secondKey"] }],
			},
			config: { apiKey: "legacy-value" },
		});
		for (const present of ["apiKey", "secondKey"]) {
			const lookups: string[] = [];
			expect(
				(
					await readiness(registry, ({ key }) => {
						lookups.push(key);
						return key === providerSecretKey("demo", present);
					})
				)[0].ready,
			).toBe(true);
			expect(lookups).toEqual([
				providerSecretKey("demo", "apiKey"),
				providerSecretKey("demo", "secondKey"),
			]);
		}
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
		expect((await readiness(registry))[0].ready).toBe(false);
	});

	test("oneOf secret content branches become presence alternatives and retain existing anyOf", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: { apiKey: { type: "string", format: "password" }, mode: { type: "string" } },
				$defs: {
					credential: { required: ["apiKey"], properties: { apiKey: { enum: ["alpha"] } } },
				},
				oneOf: [
					{ $ref: "#/$defs/credential" },
					{ required: ["apiKey"], properties: { apiKey: { enum: ["beta"] } } },
				],
				anyOf: [{ properties: { mode: { const: "valid" } } }],
			},
			config: { apiKey: "alpha", mode: "valid" },
		});
		const before = registry.get(instanceId)?.configSchema;
		expect((await readiness(registry, () => true))[0].ready).toBe(true);
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
		expect(
			registry.validateConfigPresence(
				instanceId,
				{ apiKey: "present", mode: "invalid" },
				new Set(["apiKey"]),
			).valid,
		).toBe(false);
		expect(registry.validateConfig(instanceId, { apiKey: "other", mode: "valid" }).valid).toBe(
			false,
		);
		expect(registry.get(instanceId)?.configSchema).toEqual(before);
	});

	test("oneOf without existing anyOf accepts presence for secret enums", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: { apiKey: { type: "string", format: "password" } },
				oneOf: [
					{ required: ["apiKey"], properties: { apiKey: { enum: ["alpha"] } } },
					{ required: ["apiKey"], properties: { apiKey: { enum: ["beta"] } } },
				],
			},
			config: { apiKey: "alpha" },
		});
		expect((await readiness(registry, () => true))[0].ready).toBe(true);
		expect((await readiness(registry, () => false))[0].ready).toBe(false);
	});

	test("ordinary oneOf remains exclusive even when secrets elsewhere are projected", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: {
					apiKey: { type: "string", format: "password", enum: ["alpha"] },
					mode: { type: "string" },
				},
				required: ["apiKey", "mode"],
				oneOf: [
					{ properties: { mode: { enum: ["unique", "both"] }, apiKey: { type: "string" } } },
					{ properties: { mode: { enum: ["both"] }, apiKey: { type: "string" } } },
				],
			},
			config: { apiKey: "alpha", mode: "unique" },
		});
		expect((await readiness(registry, () => true))[0].ready).toBe(true);
		expect(
			registry.validateConfigPresence(
				instanceId,
				{ apiKey: "present", mode: "both" },
				new Set(["apiKey"]),
			).valid,
		).toBe(false);
	});

	test("optional secrets do not require vault access", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: { apiKey: { type: "string", writeOnly: true } },
			},
			config: {},
		});
		expect((await readiness(registry, noSideEffects))[0].ready).toBe(true);
	});

	test("registry schema validation is reused for plain configuration", async () => {
		const registry = new PluginProviderRegistry();
		register(registry, {
			configSchema: { type: "object", properties: { mode: { enum: ["valid"] } } },
		});
		const getConfig = spyOn(registry, "getConfig").mockReturnValue({ mode: "invalid" });
		try {
			expect((await readiness(registry))[0].ready).toBe(false);
		} finally {
			getConfig.mockRestore();
		}
	});

	test("vault errors mark only the affected provider unready", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry);
		register(registry, {
			localId: "other",
			providerInstanceId: `${pluginId}/other`,
			providerPrefix: "other",
		});
		const entries = await readiness(registry, () => {
			throw new Error("unreadable vault");
		});
		expect(entries.map((entry) => entry.ready)).toEqual([false, true]);
	});

	test("a hung vault lookup preserves healthy providers before the outer source deadline", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry, {
			configSchema: {
				type: "object",
				properties: {
					apiKey: { type: "string", format: "password" },
					secondKey: { type: "string", format: "password" },
				},
				required: ["apiKey", "secondKey"],
			},
			config: { apiKey: "legacy-key", secondKey: "legacy-second" },
		});
		register(registry, {
			localId: "healthy",
			providerInstanceId: `${pluginId}/healthy`,
			providerPrefix: "healthy",
		});
		const pending = Promise.withResolvers<boolean>();
		let calls = 0;
		const service = configService(registry, () => {
			calls++;
			return pending.promise;
		});
		const sources = new ProviderSourceRegistry();
		sources.register("plugins", {
			resolve: () => null,
			listReadiness: (sourceSignal) => listPluginProviderReadiness(registry, service, sourceSignal),
		});
		const result = await sources.listReadiness(new Set());
		expect(result).toHaveLength(2);
		expect(result.find((entry) => entry.prefix === "demo")?.ready).toBe(false);
		expect(result.find((entry) => entry.prefix === "healthy")?.ready).toBe(true);
		expect(calls).toBe(1);
		pending.resolve(true);
		await Promise.resolve();
		expect(calls).toBe(1);
		expect(result.find((entry) => entry.prefix === "demo")?.ready).toBe(false);
	});

	test("revocation during a pending vault read prevents readiness", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry);
		const reached = Promise.withResolvers<void>();
		const pending = Promise.withResolvers<boolean>();
		const result = readiness(registry, () => {
			reached.resolve();
			return pending.promise;
		});
		await reached.promise;
		registry.disablePlugin(pluginId);
		pending.resolve(true);
		expect((await result)[0].ready).toBe(false);
	});

	test("pending vault reads respect prefix changes, unregistration and cleared plain config", async () => {
		for (const change of ["prefix", "unregister", "config"]) {
			const registry = new PluginProviderRegistry();
			registerWithSecret(registry);
			const reached = Promise.withResolvers<void>();
			const pending = Promise.withResolvers<boolean>();
			const result = readiness(registry, () => {
				reached.resolve();
				return pending.promise;
			});
			await reached.promise;
			if (change === "prefix") registry.setProviderPrefix(instanceId, "renamed");
			else if (change === "unregister") registry.unregister(instanceId);
			else registry.updateConfig(instanceId, { endpoint: "", apiKey: "legacy-value" });
			pending.resolve(true);
			const [entry] = await result;
			expect(entry.ready).toBe(change === "prefix");
			expect(entry.prefix).toBe(change === "prefix" ? "renamed" : "demo");
		}
	});

	test("secret I/O has bounded concurrency and cancellation stops subsequent work", async () => {
		const registry = new PluginProviderRegistry();
		for (let i = 0; i < 12; i++) {
			registerWithSecret(registry, {
				localId: `provider-${i}`,
				providerInstanceId: `${pluginId}/provider-${i}`,
				providerPrefix: `provider-${i}`,
			});
		}
		const reached = Promise.withResolvers<void>();
		const pending = Promise.withResolvers<boolean>();
		const controller = new AbortController();
		let calls = 0;
		const service = configService(registry, () => {
			if (++calls === 4) reached.resolve();
			return pending.promise;
		});
		const result = listPluginProviderReadiness(registry, service, controller.signal);
		await reached.promise;
		expect(calls).toBe(4);
		controller.abort(new Error("cancel readiness"));
		await expect(result).rejects.toThrow("cancel readiness");
		pending.resolve(true);
		expect(calls).toBe(4);
	});

	test("bounded workers eventually inspect every provider", async () => {
		const registry = new PluginProviderRegistry();
		for (let i = 0; i < 12; i++) {
			registerWithSecret(registry, {
				localId: `provider-${i}`,
				providerInstanceId: `${pluginId}/provider-${i}`,
				providerPrefix: `provider-${i}`,
			});
		}
		const reached = Promise.withResolvers<void>();
		const pending = Promise.withResolvers<void>();
		let active = 0;
		let maximum = 0;
		let calls = 0;
		const result = readiness(registry, async () => {
			calls++;
			maximum = Math.max(maximum, ++active);
			if (active === 4) reached.resolve();
			await pending.promise;
			active--;
			return true;
		});
		await reached.promise;
		expect(calls).toBe(4);
		pending.resolve();
		expect((await result).every((entry) => entry.ready)).toBe(true);
		expect(calls).toBe(12);
		expect(maximum).toBe(4);
	});

	test("an already cancelled request performs no local lookup", async () => {
		const registry = new PluginProviderRegistry();
		registerWithSecret(registry);
		const controller = new AbortController();
		controller.abort(new Error("already cancelled"));
		await expect(
			listPluginProviderReadiness(
				registry,
				configService(registry, noSideEffects),
				controller.signal,
			),
		).rejects.toThrow("already cancelled");
	});
});
