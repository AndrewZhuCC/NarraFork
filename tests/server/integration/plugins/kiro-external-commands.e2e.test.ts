import { afterAll, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseManifest } from "@server/lib/plugins/manifest";
import { PluginCommandRegistry } from "@server/services/plugin-command-registry";
import { applyCommandSecretWrites } from "@server/services/plugin-command-secret-writes";
import { providerRegistrationsFromManifest } from "@server/services/plugin-provider-manifest";
import { PluginProviderRegistry } from "@server/services/plugin-provider-registry";
import { LocalProcessRunner, PluginRuntime } from "@server/services/plugin-runtime";
import { PluginSecretVault } from "@server/services/plugin-secret-vault";

/**
 * The credential page's action surface, driven through a real plugin subprocess.
 *
 * This is the path that did not exist before: `commands.execute` only ever resolved
 * host-registered handlers, so a plugin command was dead code. The e2e here proves the whole
 * chain — host registry → `commands.invoke` RPC → plugin handler → `secretWrites` → vault —
 * rather than each half in isolation.
 *
 * Credentials are synthetic. These commands manipulate credential *state* (disable, priority,
 * removal) and never contact upstream, so no live account is needed; the two commands that do
 * reach the network have their own coverage in the connectivity script.
 */

const PLUGIN_ROOT = resolve(import.meta.dir, "../../../../examples/plugins/kiro-external");
const PLUGIN_ID = "com.narrafork.kiro-external";

/** Fixed bookkeeping key. Each credential lives under its own `provider.kiro.cred.<id>`. */
const INDEX_KEY = "provider.kiro.credentialsIndex";

function credKey(id: string): string {
	return `provider.kiro.cred.${id}`;
}

const roots: string[] = [];

afterAll(async () => {
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function loadManifest() {
	return parseManifest(JSON.parse(await readFile(join(PLUGIN_ROOT, "manifest.json"), "utf8")));
}

/**
 * Serves the plugin's own `secrets.*` calls out of a real vault.
 *
 * Needed because credentials no longer arrive inside `config`: the plugin reads them itself, so
 * a subprocess with no `secrets.*` channel would find none and every command would report an
 * unconfigured provider. Only the four secret methods are served — anything else the plugin
 * asked for would be a change worth noticing rather than silently satisfying.
 */
function secretDispatcher(vault: PluginSecretVault) {
	return {
		dispatch: async (request: { method: string; params?: unknown }) => {
			const { key, value } = (request.params ?? {}) as { key?: string; value?: string };
			switch (request.method) {
				case "secrets.get":
					return {
						value: key ? await vault.getSecret({ pluginId: PLUGIN_ID, key }) : undefined,
					};
				case "secrets.set":
					if (key && value !== undefined) {
						await vault.setSecret({ pluginId: PLUGIN_ID, key, value });
					}
					return { stored: true };
				case "secrets.delete":
					if (key) await vault.deleteSecret({ pluginId: PLUGIN_ID, key });
					return { deleted: true };
				case "secrets.list":
					return {
						secrets: (await vault.listKeys(PLUGIN_ID)).map((name) => ({ key: name })),
					};
				default:
					throw new Error(`unexpected plugin->host method: ${request.method}`);
			}
		},
	};
}

function createRuntime(
	manifest: Awaited<ReturnType<typeof loadManifest>>,
	vault: PluginSecretVault,
): PluginRuntime {
	if (!manifest.server) throw new Error("plugin must declare a server entry");
	return new PluginRuntime({
		dispatcher: secretDispatcher(vault) as never,
		pluginId: manifest.pluginId,
		pluginVersion: manifest.version,
		command: [process.execPath, join(PLUGIN_ROOT, manifest.server.entry)],
		cwd: PLUGIN_ROOT,
		rpcProtocol: manifest.server.protocol,
		hostApiVersion: "1.0",
		grantedCapabilities: manifest.permissions.host,
		activationReason: "commands-e2e",
		runner: new LocalProcessRunner({
			allowedCwds: [PLUGIN_ROOT],
			maxHeaderBytes: 8 * 1024,
			maxBodyBytes: 4 * 1024 * 1024,
			maxStdoutBytes: 4 * 1024 * 1024,
			stderrRingBytes: 16 * 1024,
			maxStderrBytes: 32 * 1024,
			maxStderrBytesPerSecond: 32 * 1024,
			spawnTimeoutMs: 10_000,
			idleTimeoutMs: 30_000,
			totalTimeoutMs: 60_000,
			killProcessTree: true,
			resourceLimits: { cpuTimeSeconds: 30, memoryBytes: 1024 * 1024 * 1024 },
			allowUnboundedResourceUsage: process.platform === "win32",
		}),
		timeouts: {
			handshakeMs: 10_000,
			activationMs: 10_000,
			rpcMs: 30_000,
			drainMs: 200,
			shutdownMs: 1_500,
			cancelGraceMs: 1_000,
		},
		idleTimeoutMs: 30_000,
		totalTimeoutMs: 60_000,
		maxInFlight: 4,
	});
}

/** Write credentials into the vault the way the plugin itself stores them. */
async function seedCredentials(
	vault: PluginSecretVault,
	entries: Array<{ id: string; priority?: number; disabled?: boolean }>,
): Promise<void> {
	for (const entry of entries) {
		await vault.setSecret({
			pluginId: PLUGIN_ID,
			key: credKey(entry.id),
			value: JSON.stringify({
				id: entry.id,
				authMethod: "social",
				refreshToken: `rt-${entry.id}`,
				...(entry.priority === undefined ? {} : { priority: entry.priority }),
				...(entry.disabled === undefined ? {} : { disabled: entry.disabled }),
			}),
		});
	}
	await vault.setSecret({
		pluginId: PLUGIN_ID,
		key: INDEX_KEY,
		value: JSON.stringify({ revision: 1, count: entries.length }),
	});
}

/** Every stored credential, read back out of the vault. */
async function storedCredentials(
	vault: PluginSecretVault,
): Promise<Array<Record<string, unknown>>> {
	const keys = (await vault.listKeys(PLUGIN_ID)).filter((key) =>
		key.startsWith("provider.kiro.cred."),
	);
	const credentials: Array<Record<string, unknown>> = [];
	for (const key of keys.sort()) {
		const raw = await vault.getSecret({ pluginId: PLUGIN_ID, key });
		if (raw) credentials.push(JSON.parse(raw) as Record<string, unknown>);
	}
	return credentials;
}

/** Full host wiring: registry, vault, and the secret-write validator. */
async function withPlugin<T>(
	run: (harness: {
		invoke: (
			contributionId: string,
			input: Record<string, unknown>,
		) => Promise<{ output: unknown; applied: string[]; deleted: string[] }>;
		vault: PluginSecretVault;
		config: Record<string, unknown>;
	}) => Promise<T>,
): Promise<T> {
	const root = await mkdtemp(join(tmpdir(), "nf-kiro-cmd-e2e-"));
	roots.push(root);
	const manifest = await loadManifest();
	const vault = new PluginSecretVault({ root });
	const runtime = createRuntime(manifest, vault);
	const providerRegistry = new PluginProviderRegistry();
	for (const registration of providerRegistrationsFromManifest({
		manifest,
		generation: `${manifest.version}:cmd-e2e`,
	})) {
		providerRegistry.register(registration);
	}
	const commandRegistry = new PluginCommandRegistry({ resolveRuntime: () => runtime });
	commandRegistry.registerManifest(manifest);

	try {
		// Seed the vault, not the config: this is where credentials live now, and seeding them
		// the old way would leave the plugin reading an empty store.
		await seedCredentials(vault, [
			{ id: "a", priority: 0 },
			{ id: "b", priority: 1 },
		]);
		await runtime.start();
		await runtime.request("provider.describe", { protocolVersions: ["1.0"] });
		const config: Record<string, unknown> = { region: "us-east-1" };
		let requestSeq = 0;
		const invoke = async (contributionId: string, input: Record<string, unknown>) => {
			requestSeq += 1;
			const result = await commandRegistry.invoke(
				contributionId,
				manifest.pluginId,
				{ config, ...input } as never,
				{ requestId: `req-${requestSeq}` },
			);
			// Apply through the real validator, so an escape would fail here rather than in
			// production.
			// Deletions are reported separately from writes, and both matter now: removing a
			// credential is a deletion of its own key rather than a rewrite of a shared value.
			const outcome =
				result.secretWrites.length > 0
					? await applyCommandSecretWrites({
							pluginId: manifest.pluginId,
							writes: result.secretWrites,
							registry: providerRegistry,
							sink: vault,
						})
					: { written: [], deleted: [] };
			return { output: result.output, applied: outcome.written, deleted: outcome.deleted };
		};
		return await run({ invoke, vault, config });
	} finally {
		await runtime.shutdown().catch(() => undefined);
	}
}

describe("kiro-external commands: read", () => {
	test("status reports every credential and the balancing mode", async () => {
		await withPlugin(async ({ invoke }) => {
			const { output } = await invoke("status", {});
			expect(output).toMatchObject({
				credentialCount: 2,
				availableCount: 2,
				loadBalancingMode: "priority",
			});
		});
	}, 60_000);

	test("status never returns credential material", async () => {
		await withPlugin(async ({ invoke }) => {
			const { output } = await invoke("status", {});
			// `snapshot()` is metadata-only by design; this pins that the command does not
			// enrich it with anything sensitive on the way out.
			expect(JSON.stringify(output)).not.toContain("rt-a");
			expect(JSON.stringify(output)).not.toContain("refreshToken");
		});
	}, 60_000);
});

describe("kiro-external commands: state changes persist through the vault", () => {
	test("disabling a credential writes the bundle to the vault", async () => {
		await withPlugin(async ({ invoke, vault }) => {
			const { output, applied } = await invoke("credentials.setState", {
				credentialId: "a",
				state: "disabled",
			});
			expect(output).toMatchObject({ availableCount: 1 });
			// The decisive assertion: the change reached the vault, under the changed
			// credential's own key, with the sentinel written last.
			expect(applied).toEqual([credKey("a"), credKey("b"), INDEX_KEY]);
			const parsed = await storedCredentials(vault);
			expect(parsed.find((item) => item.id === "a")?.disabled).toBe(true);
			// The untouched sibling is still there: per-key storage means one credential's
			// change cannot drop another.
			expect(parsed.find((item) => item.id === "b")).toBeDefined();
		});
	}, 60_000);

	test("enable and reset are distinct states", async () => {
		await withPlugin(async ({ invoke }) => {
			await invoke("credentials.setState", { credentialId: "a", state: "disabled" });
			const enabled = await invoke("credentials.setState", {
				credentialId: "a",
				state: "enabled",
			});
			expect(enabled.output).toMatchObject({ availableCount: 2 });
			// `reset` additionally clears counters, so it must be accepted as its own state.
			const reset = await invoke("credentials.setState", { credentialId: "a", state: "reset" });
			expect(reset.output).toMatchObject({ availableCount: 2 });
		});
	}, 60_000);

	test("priority changes are persisted", async () => {
		await withPlugin(async ({ invoke, vault }) => {
			const { applied } = await invoke("credentials.setPriority", {
				credentialId: "b",
				priority: 7,
			});
			expect(applied).toEqual([credKey("a"), credKey("b"), INDEX_KEY]);
			const parsed = await storedCredentials(vault);
			expect(parsed.find((item) => item.id === "b")?.priority).toBe(7);
		});
	}, 60_000);

	test("removing a credential deletes its key and updates the count", async () => {
		await withPlugin(async ({ invoke, vault }) => {
			const { output, deleted } = await invoke("credentials.remove", { credentialId: "a" });
			expect(output).toMatchObject({ credentialCount: 1 });

			// Removal is a deletion of the removed credential's own key, not a rewrite of a
			// shared value, so the remaining one is untouched.
			expect(await storedCredentials(vault)).toHaveLength(1);
			expect(deleted).toEqual([credKey("a")]);
			const index = await vault.getSecret({ pluginId: PLUGIN_ID, key: INDEX_KEY });
			expect(JSON.parse(index ?? "{}")).toMatchObject({ count: 1 });
		});
	}, 60_000);

	test("removing the last credential deletes the sentinel entirely", async () => {
		// A sentinel of `count: 0` would still read as a non-empty value, so `isUsable()` and
		// `secretsSet` would both report a configured provider with nothing behind it. The
		// failure would only surface when a request was finally made.
		await withPlugin(async ({ invoke, vault }) => {
			await invoke("credentials.remove", { credentialId: "a" });
			await invoke("credentials.remove", { credentialId: "b" });

			expect(await storedCredentials(vault)).toHaveLength(0);
			expect(await vault.getSecret({ pluginId: PLUGIN_ID, key: INDEX_KEY })).toBeUndefined();
		});
	}, 60_000);

	test("importing adds credentials and reports duplicates", async () => {
		await withPlugin(async ({ invoke }) => {
			const { output } = await invoke("credentials.import", {
				credentials: [
					{ authMethod: "social", refreshToken: "rt-c" },
					// Same secret as the existing `a`, so it must be counted as a duplicate.
					{ authMethod: "social", refreshToken: "rt-a" },
				],
			});
			expect(output).toMatchObject({ added: 1, duplicates: 1 });
		});
	}, 60_000);

	test("updating non-secret fields is persisted", async () => {
		await withPlugin(async ({ invoke, vault }) => {
			await invoke("credentials.update", {
				credentialId: "a",
				fields: { region: "eu-west-1", displayName: "Team account" },
			});
			const target = (await storedCredentials(vault)).find((item) => item.id === "a");
			expect(target?.region).toBe("eu-west-1");
			expect(target?.displayName).toBe("Team account");
		});
	}, 60_000);

	test("load balancing mode changes without touching the vault", async () => {
		await withPlugin(async ({ invoke }) => {
			const { output, applied } = await invoke("config.setLoadBalancing", { mode: "balanced" });
			expect(output).toMatchObject({ loadBalancingMode: "balanced" });
			// Not a secret, so the host's config form owns persistence for this one.
			expect(applied).toEqual([]);
		});
	}, 60_000);
});

describe("kiro-external commands: input validation", () => {
	test("rejects a non-numeric priority", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(
				invoke("credentials.setPriority", { credentialId: "b", priority: "high" }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects an unknown state", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(
				invoke("credentials.setState", { credentialId: "a", state: "paused" }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects a missing credentialId", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(invoke("credentials.remove", {})).rejects.toThrow();
		});
	}, 60_000);

	test("rejects an import with no usable credential", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(
				invoke("credentials.import", { credentials: [{ authMethod: "social" }] }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects an update with no editable field", async () => {
		await withPlugin(async ({ invoke }) => {
			// Secrets are editable now (the built-in credential editor allows it too), so the
			// rejection this asserts is the empty-patch case: a field set the command does not
			// recognise must fail loudly rather than report a successful no-op edit.
			await expect(
				invoke("credentials.update", { credentialId: "a", fields: { notAField: "x" } }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects a blank secret instead of clearing the stored one", async () => {
		await withPlugin(async ({ invoke }) => {
			// The credential page never echoes stored secrets, so its inputs start blank. An
			// untouched blank field must mean "keep", never "erase" — otherwise saving an
			// unrelated field would wipe the refresh token. With only a blank secret in the
			// patch there is nothing left to apply, so the command rejects.
			await expect(
				invoke("credentials.update", { credentialId: "a", fields: { refreshToken: "   " } }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects an unknown command", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(invoke("does-not-exist", {})).rejects.toThrow();
		});
	}, 60_000);
});
