// Kiro plugin public API

import { randomUUID } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { getNarraforkPath } from "../narrafork-home";
import { KiroApiClient } from "./api-client";
import {
	loadAllCredentials,
	loadConfig,
	loadUsageCache,
	saveAllCredentials,
	saveConfig,
	saveUsageCache,
} from "./config";
import { parseEventStream, type StreamEvent } from "./event-stream-parser";
import { KiroMcpClient } from "./mcp-client";
import { MultiTokenManager } from "./multi-token-manager";
import type {
	KiroChatRequest,
	KiroCredentials,
	KiroModel,
	KiroPluginConfig,
	ManagerSnapshot,
	McpResponse,
	UsageCacheEntry,
} from "./types";
import { kiroUsageFetchQueue } from "./usage-fetch-queue";

let tokenManager: MultiTokenManager | null = null;
let apiClient: KiroApiClient | null = null;
let mcpClient: KiroMcpClient | null = null;
let defaultModel: KiroModel = "claude-sonnet-4.5";
let credentialsPath: string | null = null;
let pluginConfig: KiroPluginConfig | null = null;

function hasRefreshToken(creds: KiroCredentials): boolean {
	return typeof creds.refreshToken === "string" && creds.refreshToken.trim().length > 0;
}

export { getProxy, registerProxyResolver } from "./proxy";

/** Initialize the kiro plugin. Call once at app startup. */
export function initKiro(config: KiroPluginConfig): boolean {
	pluginConfig = config;
	credentialsPath = config.credentialsPath;
	const { credentials, isMultipleFormat } = loadAllCredentials(config.credentialsPath);
	const usableCredentials = credentials.filter(hasRefreshToken);
	if (usableCredentials.length === 0) return false;
	if (usableCredentials.length !== credentials.length && isMultipleFormat) {
		try {
			saveAllCredentials(config.credentialsPath, usableCredentials);
		} catch {
			// Non-fatal: keep initializing from the valid in-memory subset.
		}
	}

	const kiroConfig = loadConfig(config.configPath);
	tokenManager = new MultiTokenManager(
		kiroConfig,
		usableCredentials,
		config.credentialsPath,
		isMultipleFormat,
	);
	apiClient = new KiroApiClient(tokenManager);
	mcpClient = new KiroMcpClient(tokenManager);
	if (config.defaultModel) defaultModel = config.defaultModel;
	return true;
}

/** Check if kiro is configured and available. */
export function isKiroAvailable(): boolean {
	return apiClient !== null;
}

/** Build a minimal chat request. Frontend can also construct KiroChatRequest directly. */
export function buildChatRequest(
	text: string,
	model: KiroModel = "claude-sonnet-4.5",
): KiroChatRequest {
	return {
		conversationState: {
			conversationId: randomUUID(),
			chatTriggerType: "MANUAL",
			currentMessage: {
				userInputMessage: {
					content: text,
					modelId: model,
					userInputMessageContext: {},
					origin: "AI_EDITOR",
				},
			},
		},
	};
}

/** Stream structured events from Claude via Kiro API. Accepts full request body. */
export async function* kiroChatRaw(
	request: KiroChatRequest,
	signal?: AbortSignal,
): AsyncGenerator<StreamEvent> {
	if (!apiClient) throw new Error("Kiro not initialized — call initKiro() first");

	const { response: res, credentialId, release } = await apiClient.sendChatRequest(request, signal);
	if (!res.body) {
		release();
		throw new Error("No response body");
	}

	try {
		// Emit synthetic credential event so consumers can associate metering with the credential
		yield { eventType: "credentialIdEvent", data: { credentialId } };
		yield* parseEventStream(res.body);
	} finally {
		release();
	}
}

/** Convenience: stream events from a simple text prompt. */
export async function* kiroChat(text: string, model?: KiroModel): AsyncGenerator<StreamEvent> {
	yield* kiroChatRaw(buildChatRequest(text, model ?? defaultModel));
}

/** Collect full response from Claude via Kiro API. */
export async function kiroGenerate(text: string, model?: KiroModel): Promise<string> {
	const result = await kiroGenerateWithMeta(text, model);
	return result.text;
}

/** Like kiroGenerate but also returns contextUsagePercentage if available. */
export async function kiroGenerateWithMeta(
	text: string,
	model?: KiroModel,
): Promise<{ text: string; contextPercent?: number }> {
	const chunks: string[] = [];
	let contextPercent: number | undefined;
	for await (const evt of kiroChat(text, model)) {
		if (evt.eventType === "assistantResponseEvent" && evt.data.content != null) {
			chunks.push(String(evt.data.content));
		}
		if (evt.eventType === "contextUsageEvent") {
			const pct = evt.data.contextUsagePercentage ?? evt.data.context_usage_percentage;
			if (pct != null) contextPercent = Number(pct);
		}
	}
	return { text: chunks.join(""), contextPercent };
}

// --- MCP ---

export async function kiroMcpCall(
	name: string,
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<McpResponse> {
	if (!mcpClient) throw new Error("Kiro not initialized — call initKiro() first");
	return mcpClient.callTool(name, args, signal);
}

export async function kiroMcpListTools(): Promise<McpResponse> {
	if (!mcpClient) throw new Error("Kiro not initialized — call initKiro() first");
	return mcpClient.listTools();
}

// --- Management API ---

export function kiroSnapshot(): ManagerSnapshot | null {
	return tokenManager?.snapshot() ?? null;
}

export function kiroSetDisabled(id: string, disabled: boolean): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.setDisabled(id, disabled);
}

export function kiroSetLoadBalancingMode(mode: "priority" | "balanced"): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.setLoadBalancingMode(mode);
	if (pluginConfig) {
		try {
			saveConfig(pluginConfig.configPath, tokenManager.currentConfig);
		} catch {
			// Non-critical: runtime mode is already updated.
		}
	}
}

export function kiroSetPriority(id: string, priority: number): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.setPriority(id, priority);
}

export function kiroResetAndEnable(id: string): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.resetAndEnable(id);
}

export function kiroRemoveCredential(id: string): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.removeCredential(id);
}

// --- Credential Management API ---

export async function kiroManualRefresh(id: string): Promise<void> {
	if (!tokenManager) throw new Error("Kiro not initialized");
	await tokenManager.manualRefresh(id);
}

export function kiroUpdateCredential(
	id: string,
	fields: { email?: string; displayName?: string; region?: string },
): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.updateCredential(id, fields);
}

export function kiroImportCredentials(creds: KiroCredentials[]): {
	added: number;
	duplicates: number;
} {
	const importableCreds = creds.filter(hasRefreshToken);
	const ignored = creds.length - importableCreds.length;
	if (tokenManager) {
		const result = tokenManager.addCredentials(importableCreds);
		return { ...result, duplicates: result.duplicates + ignored };
	}
	// Kiro not yet initialized — bootstrap with default config if needed
	if (!pluginConfig) {
		const kiroDir = getNarraforkPath("kiro");
		mkdirSync(kiroDir, { recursive: true });
		pluginConfig = {
			credentialsPath: resolve(kiroDir, "credentials.json"),
			configPath: resolve(kiroDir, "config.json"),
		};
		credentialsPath = pluginConfig.credentialsPath;
		// Persist kiro config to settings.json so it survives restart
		const settingsPath = getNarraforkPath("settings.json");
		try {
			if (existsSync(settingsPath)) {
				const raw = JSON.parse(readFileSync(settingsPath, "utf-8"));
				if (!raw.kiro?.credentialsPath) {
					raw.kiro = {
						credentialsPath: pluginConfig.credentialsPath,
						configPath: pluginConfig.configPath,
					};
					writeFileSync(settingsPath, JSON.stringify(raw, null, 2));
				}
			}
		} catch {
			// Non-fatal: settings write failure won't block import
		}
	}
	// Write credentials to file, then init. Empty/plain `{}` imports are treated as a reload
	// trigger for existing credentials and are not persisted as bogus accounts.
	const { credentials: existing } = loadAllCredentials(pluginConfig.credentialsPath);
	const existingImportable = existing.filter(hasRefreshToken);
	const merged = [...existingImportable, ...importableCreds];
	if (merged.length === 0) {
		throw new Error("No credentials with refreshToken to import");
	}
	mkdirSync(dirname(pluginConfig.credentialsPath), { recursive: true });
	saveAllCredentials(pluginConfig.credentialsPath, merged);
	const ok = initKiro(pluginConfig);
	if (!ok) throw new Error("Imported credentials but initKiro still failed — check refreshTokens");
	const afterCount = kiroSnapshot()?.total ?? 0;
	const added = afterCount - existingImportable.length;
	return { added: Math.max(added, 0), duplicates: creds.length - Math.max(added, 0) };
}

export async function kiroGetUsage(id: string): Promise<UsageCacheEntry> {
	if (!tokenManager || !credentialsPath) throw new Error("Kiro not initialized");
	const result = await tokenManager.getUsageLimits(id);
	// Sync meterUsage on the credential to the authoritative API value
	tokenManager.setMeterUsage(id, result.currentUsage);
	// Save to cache
	try {
		const cache = loadUsageCache(credentialsPath);
		cache[id] = result;
		saveUsageCache(credentialsPath, cache);
	} catch {}
	return result;
}

export async function kiroListModels(credentialId?: string): Promise<{
	models: Array<Record<string, unknown>>;
	credentialId: string;
}> {
	if (!tokenManager) throw new Error("Kiro not initialized");
	return tokenManager.listAvailableModels(credentialId);
}

/** Fetch and store the model list for a single credential. */
export async function kiroFetchCredentialModels(credentialId: string): Promise<string[]> {
	if (!tokenManager) throw new Error("Kiro not initialized");
	return tokenManager.fetchAndStoreModels(credentialId);
}

/** Fetch model lists for all credentials, store per-credential, return deduplicated union. */
export async function kiroFetchAllCredentialModels(): Promise<Array<Record<string, unknown>>> {
	if (!tokenManager) throw new Error("Kiro not initialized");
	return tokenManager.fetchAllCredentialModels();
}

export function kiroGetAllUsageCache(): Record<string, UsageCacheEntry> {
	if (!credentialsPath) return {};
	return loadUsageCache(credentialsPath);
}

export function kiroQueueUsageFetch(id: string): void {
	kiroUsageFetchQueue.enqueue(id);
}

export function kiroUsageQueueSnapshot(): ReturnType<typeof kiroUsageFetchQueue.getSnapshot> {
	return kiroUsageFetchQueue.getSnapshot();
}

/** Accumulate metering usage for a credential (debounced persistence). */
export function kiroAddMeterUsage(credentialId: string, amount: number): void {
	if (!tokenManager) return;
	tokenManager.addMeterUsage(credentialId, amount);
}

kiroUsageFetchQueue.setExecutor((credentialId) => kiroGetUsage(credentialId));

export { EventStreamDecoder, type StreamEvent } from "./event-stream-parser";
// Re-export types for consumers
export type {
	KiroChatRequest,
	KiroConversationState,
	KiroCredentials,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroModel,
	KiroPluginConfig,
	KiroTool,
	KiroToolResult,
	KiroUserInputMessage,
	KiroUserInputMessageContext,
	ManagerSnapshot,
	McpResponse,
	UsageCacheEntry,
} from "./types";
export {
	KiroApiError,
	KiroAuthError,
	KiroError,
	KiroParseError,
	KiroQuotaError,
} from "./types";
