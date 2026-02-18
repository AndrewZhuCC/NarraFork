// Kiro plugin public API

import { KiroApiClient } from "./api-client";
import { loadAllCredentials, loadConfig, loadUsageCache, saveAllCredentials, saveUsageCache } from "./config";
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

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { homedir } from "node:os";
import { dirname, resolve } from "node:path";

let tokenManager: MultiTokenManager | null = null;
let apiClient: KiroApiClient | null = null;
let mcpClient: KiroMcpClient | null = null;
let defaultModel: KiroModel = "claude-sonnet-4.5";
let credentialsPath: string | null = null;
let pluginConfig: KiroPluginConfig | null = null;

/** Initialize the kiro plugin. Call once at app startup. */
export function initKiro(config: KiroPluginConfig): boolean {
	pluginConfig = config;
	credentialsPath = config.credentialsPath;
	const { credentials, isMultipleFormat } = loadAllCredentials(
		config.credentialsPath,
	);
	if (!credentials.some((c) => c.refreshToken)) return false;

	const kiroConfig = loadConfig(config.configPath);
	tokenManager = new MultiTokenManager(
		kiroConfig,
		credentials,
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
): AsyncGenerator<StreamEvent> {
	if (!apiClient)
		throw new Error("Kiro not initialized — call initKiro() first");

	const res = await apiClient.sendChatRequest(request);
	if (!res.body) throw new Error("No response body");

	yield* parseEventStream(res.body);
}

/** Convenience: stream events from a simple text prompt. */
export async function* kiroChat(
	text: string,
	model?: KiroModel,
): AsyncGenerator<StreamEvent> {
	yield* kiroChatRaw(buildChatRequest(text, model ?? defaultModel));
}

/** Collect full response from Claude via Kiro API. */
export async function kiroGenerate(
	text: string,
	model?: KiroModel,
): Promise<string> {
	const chunks: string[] = [];
	for await (const evt of kiroChat(text, model)) {
		if (evt.eventType === "assistantResponseEvent" && evt.data.content != null) {
			chunks.push(String(evt.data.content));
		}
	}
	return chunks.join("");
}

// --- MCP ---

export async function kiroMcpCall(
	name: string,
	args: Record<string, unknown>,
): Promise<McpResponse> {
	if (!mcpClient)
		throw new Error("Kiro not initialized — call initKiro() first");
	return mcpClient.callTool(name, args);
}

// --- Management API ---

export function kiroSnapshot(): ManagerSnapshot | null {
	return tokenManager?.snapshot() ?? null;
}

export function kiroSetDisabled(id: number, disabled: boolean): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.setDisabled(id, disabled);
}

export function kiroSetPriority(id: number, priority: number): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.setPriority(id, priority);
}

export function kiroResetAndEnable(id: number): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.resetAndEnable(id);
}

export function kiroRemoveCredential(id: number): void {
	if (!tokenManager) throw new Error("Kiro not initialized");
	tokenManager.removeCredential(id);
}

// --- Credential Management API ---

export async function kiroManualRefresh(id: number): Promise<void> {
	if (!tokenManager) throw new Error("Kiro not initialized");
	await tokenManager.manualRefresh(id);
}

export function kiroImportCredentials(
	creds: KiroCredentials[],
): { added: number; duplicates: number } {
	if (tokenManager) {
		return tokenManager.addCredentials(creds);
	}
	// Kiro not yet initialized — bootstrap with default config if needed
	if (!pluginConfig) {
		const kiroDir = resolve(homedir(), ".narrafork", "kiro");
		mkdirSync(kiroDir, { recursive: true });
		pluginConfig = {
			credentialsPath: resolve(kiroDir, "credentials.json"),
			configPath: resolve(kiroDir, "config.json"),
		};
		credentialsPath = pluginConfig.credentialsPath;
		// Persist kiro config to settings.json so it survives restart
		const settingsPath = resolve(homedir(), ".narrafork", "settings.json");
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
	// Write credentials to file, then init
	const { credentials: existing } = loadAllCredentials(pluginConfig.credentialsPath);
	const merged = [...existing, ...creds];
	mkdirSync(dirname(pluginConfig.credentialsPath), { recursive: true });
	saveAllCredentials(pluginConfig.credentialsPath, merged);
	const ok = initKiro(pluginConfig);
	if (!ok) throw new Error("Imported credentials but initKiro still failed — check refreshTokens");
	const afterCount = tokenManager!.snapshot().total;
	const added = afterCount - existing.length;
	return { added: Math.max(added, 0), duplicates: creds.length - Math.max(added, 0) };
}

export async function kiroGetUsage(id: number): Promise<UsageCacheEntry> {
	console.log("[kiro:usage] kiroGetUsage called, id=", id);
	if (!tokenManager || !credentialsPath) throw new Error("Kiro not initialized");
	console.log("[kiro:usage] calling tokenManager.getUsageLimits...");
	const result = await tokenManager.getUsageLimits(id);
	console.log("[kiro:usage] getUsageLimits returned, saving cache...");
	// Save to cache
	try {
		const cache = loadUsageCache(credentialsPath);
		cache[String(id)] = result;
		saveUsageCache(credentialsPath, cache);
		console.log("[kiro:usage] cache saved");
	} catch (err) {
		console.log("[kiro:usage] cache save failed:", err);
	}
	return result;
}

export function kiroGetAllUsageCache(): Record<string, UsageCacheEntry> {
	if (!credentialsPath) return {};
	return loadUsageCache(credentialsPath);
}

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
	KiroError,
	KiroAuthError,
	KiroApiError,
	KiroQuotaError,
	KiroParseError,
} from "./types";
export { EventStreamDecoder, type StreamEvent } from "./event-stream-parser";
