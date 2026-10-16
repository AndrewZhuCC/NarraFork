import type { ProviderReadiness } from "@shared/provider-readiness";
import type { PluginProviderConfigService } from "./plugin-provider-config-service";
import type { PluginProviderRegistry } from "./plugin-provider-registry";

const READINESS_CONCURRENCY = 4;
const MAX_READINESS_PROVIDERS = 1_000;
// Finish before the outer provider source's 2s deadline, retaining healthy local results.
const READINESS_BUDGET_MS = 1_500;

/** Live, credential-free inspection. No model discovery, adapter construction or activation. */
export async function listPluginProviderReadiness(
	registry: PluginProviderRegistry,
	configService: Pick<PluginProviderConfigService, "isConfigured">,
	signal: AbortSignal,
): Promise<ProviderReadiness[]> {
	signal.throwIfAborted();
	const entries = registry
		.list()
		.filter((entry) => entry.kind === "executable-plugin")
		.slice(0, MAX_READINESS_PROVIDERS);
	const result: ProviderReadiness[] = entries.map((entry) => ({
		id: entry.providerInstanceId,
		prefix: entry.providerPrefix,
		name: entry.displayName || entry.providerPrefix,
		ready: false,
	}));
	const controller = new AbortController();
	const onAbort = () => controller.abort(signal.reason);
	signal.addEventListener("abort", onAbort, { once: true });
	if (signal.aborted) onAbort();
	const timer = setTimeout(
		() => controller.abort(new Error("Plugin provider readiness budget exceeded")),
		READINESS_BUDGET_MS,
	);
	let onStop: (() => void) | undefined;
	const stopped = new Promise<void>((resolve) => {
		onStop = resolve;
		controller.signal.addEventListener("abort", onStop, { once: true });
		if (controller.signal.aborted) resolve();
	});
	let next = 0;
	const worker = async () => {
		while (next < entries.length && !controller.signal.aborted) {
			const index = next++;
			const entry = entries[index];
			let ready = false;
			let projection = entry;
			try {
				ready =
					entry.status === "available" &&
					registry.hasAdapterFactory(entry.providerInstanceId) &&
					(await configService.isConfigured(entry.providerInstanceId, controller.signal));
				// Revocation or unregistration during a vault read must take effect immediately.
				const current = registry.get(entry.providerInstanceId);
				if (current) projection = current;
				ready =
					ready &&
					current?.status === "available" &&
					registry.hasAdapterFactory(entry.providerInstanceId);
			} catch {
				// A broken local config/vault does not hide other registered providers.
				ready = false;
			}
			// No new work or result mutations after either cancellation or the local budget.
			if (controller.signal.aborted) return;
			result[index] = {
				id: projection.providerInstanceId,
				prefix: projection.providerPrefix,
				name: projection.displayName || projection.providerPrefix,
				ready,
			};
		}
	};
	try {
		await Promise.race([
			Promise.all(Array.from({ length: Math.min(READINESS_CONCURRENCY, entries.length) }, worker)),
			stopped,
		]);
		signal.throwIfAborted();
		return result;
	} finally {
		clearTimeout(timer);
		signal.removeEventListener("abort", onAbort);
		if (onStop) controller.signal.removeEventListener("abort", onStop);
	}
}
