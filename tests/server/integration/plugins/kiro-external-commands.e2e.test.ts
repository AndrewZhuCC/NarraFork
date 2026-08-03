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
const BUNDLE_KEY = "provider.kiro.credentialsBundle";

const roots: string[] = [];

afterAll(async () => {
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function loadManifest() {
	return parseManifest(JSON.parse(await readFile(join(PLUGIN_ROOT, "manifest.json"), "utf8")));
}

function createRuntime(manifest: Awaited<ReturnType<typeof loadManifest>>): PluginRuntime {
	if (!manifest.server) throw new Error("plugin must declare a server entry");
	return new PluginRuntime({
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

function bundleOf(entries: Array<{ id: string; priority?: number; disabled?: boolean }>): string {
	return JSON.stringify(
		entries.map((entry) => ({
			id: entry.id,
			authMethod: "social",
			refreshToken: `rt-${entry.id}`,
			...(entry.priority === undefined ? {} : { priority: entry.priority }),
			...(entry.disabled === undefined ? {} : { disabled: entry.disabled }),
		})),
	);
}

/** Full host wiring: registry, vault, and the secret-write validator. */
async function withPlugin<T>(
	run: (harness: {
		invoke: (
			contributionId: string,
			input: Record<string, unknown>,
		) => Promise<{ output: unknown; applied: string[] }>;
		vault: PluginSecretVault;
		config: Record<string, unknown>;
	}) => Promise<T>,
): Promise<T> {
	const root = await mkdtemp(join(tmpdir(), "nf-kiro-cmd-e2e-"));
	roots.push(root);
	const manifest = await loadManifest();
	const runtime = createRuntime(manifest);
	const vault = new PluginSecretVault({ root });
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
		await runtime.start();
		await runtime.request("provider.describe", { protocolVersions: ["1.0"] });
		const config: Record<string, unknown> = {
			credentialsBundle: bundleOf([
				{ id: "a", priority: 0 },
				{ id: "b", priority: 1 },
			]),
			region: "us-east-1",
		};
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
			const applied =
				result.secretWrites.length > 0
					? (
							await applyCommandSecretWrites({
								pluginId: manifest.pluginId,
								writes: result.secretWrites,
								registry: providerRegistry,
								sink: vault,
							})
						).written
					: [];
			return { output: result.output, applied };
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
			// The decisive assertion: the change reached the vault under the one key this
			// plugin is allowed to write.
			expect(applied).toEqual([BUNDLE_KEY]);
			const stored = await vault.getSecret({
				pluginId: "com.narrafork.kiro-external",
				key: BUNDLE_KEY,
			});
			const parsed = JSON.parse(stored ?? "[]") as Array<{ id: string; disabled?: boolean }>;
			expect(parsed.find((item) => item.id === "a")?.disabled).toBe(true);
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
			expect(applied).toEqual([BUNDLE_KEY]);
			const stored = await vault.getSecret({
				pluginId: "com.narrafork.kiro-external",
				key: BUNDLE_KEY,
			});
			const parsed = JSON.parse(stored ?? "[]") as Array<{ id: string; priority?: number }>;
			expect(parsed.find((item) => item.id === "b")?.priority).toBe(7);
		});
	}, 60_000);

	test("removing a credential shrinks the stored bundle", async () => {
		await withPlugin(async ({ invoke, vault }) => {
			const { output } = await invoke("credentials.remove", { credentialId: "a" });
			expect(output).toMatchObject({ credentialCount: 1 });
			const stored = await vault.getSecret({
				pluginId: "com.narrafork.kiro-external",
				key: BUNDLE_KEY,
			});
			expect(JSON.parse(stored ?? "[]")).toHaveLength(1);
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
			const stored = await vault.getSecret({
				pluginId: "com.narrafork.kiro-external",
				key: BUNDLE_KEY,
			});
			const parsed = JSON.parse(stored ?? "[]") as Array<Record<string, unknown>>;
			const target = parsed.find((item) => item.id === "a");
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
			// Secret rotation goes through import, not through this command.
			await expect(
				invoke("credentials.update", { credentialId: "a", fields: { refreshToken: "sneaky" } }),
			).rejects.toThrow();
		});
	}, 60_000);

	test("rejects an unknown command", async () => {
		await withPlugin(async ({ invoke }) => {
			await expect(invoke("does-not-exist", {})).rejects.toThrow();
		});
	}, 60_000);
});
