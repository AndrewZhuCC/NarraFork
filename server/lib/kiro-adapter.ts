// Kiro adapter — the sole contact point between NarraFork core and the kiro plugin.
// If the kiro directory is missing, everything degrades gracefully.
//
// Types are inlined here so this file compiles even when server/lib/kiro/ is absent.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { registerKiroModelChecker, registerKiroModelLister } from "./settings";

// --- Inlined types (mirror of kiro/types.ts public surface) ---

export type KiroModel =
	| "claude-sonnet-4.5"
	| "claude-opus-4.5"
	| "claude-opus-4.6"
	| "claude-haiku-4.5";

export interface KiroPluginConfig {
	credentialsPath: string;
	configPath: string;
	defaultModel?: KiroModel;
}

export type DisabledReason = "manual" | "too_many_failures" | "quota_exhausted";
export type LoadBalancingMode = "priority" | "balanced";

export interface CredentialSnapshot {
	id: number;
	priority: number;
	disabled: boolean;
	disabledReason?: DisabledReason;
	failureCount: number;
	successCount: number;
	lastUsedAt?: string;
	email?: string;
	hasProfileArn: boolean;
	expiresAt?: string;
}

export interface ManagerSnapshot {
	entries: CredentialSnapshot[];
	currentId: number;
	loadBalancingMode: LoadBalancingMode;
	total: number;
	available: number;
}

export interface McpResponse {
	error?: { code?: number; message?: string };
	id: string;
	jsonrpc: string;
	result?: {
		content: Array<{ type: string; text: string }>;
		isError: boolean;
	};
}

export interface UsageCacheEntry {
	id: number;
	subscriptionTitle?: string;
	currentUsage: number;
	usageLimit: number;
	remaining: number;
	usagePercentage: number;
	nextResetAt?: number;
	queriedAt: string;
}

export interface ImportCredentialInput {
	refreshToken?: string;
	authMethod?: string;
	clientId?: string;
	clientSecret?: string;
	priority?: number;
	region?: string;
	authRegion?: string;
	apiRegion?: string;
	email?: string;
	proxyUrl?: string;
	proxyUsername?: string;
	proxyPassword?: string;
}

// --- Dynamic import with graceful fallback ---

export interface StreamEvent {
	eventType: string;
	data: Record<string, unknown>;
}

export interface KiroImage {
	format: "jpeg" | "png" | "gif" | "webp";
	source: { bytes: string };
}

export interface KiroToolSpecification {
	name: string;
	description: string;
	inputSchema: { json: Record<string, unknown> };
}

export interface KiroTool {
	toolSpecification: KiroToolSpecification;
}

export interface KiroToolResult {
	toolUseId: string;
	content: Array<Record<string, unknown>>;
	status?: string;
	isError?: boolean;
}

export interface KiroUserInputMessageContext {
	toolResults?: KiroToolResult[];
	tools?: KiroTool[];
}

export interface KiroChatRequest {
	conversationState: {
		conversationId: string;
		currentMessage: {
			userInputMessage: {
				content: string;
				modelId: string;
				userInputMessageContext?: KiroUserInputMessageContext;
				userIntent?: string;
				origin?: string;
				images?: KiroImage[];
				cachePoint?: string;
				clientCacheConfig?: Record<string, unknown>;
				agentMode?: string;
				source?: string;
			};
		};
		chatTriggerType?: string;
		history?: Record<string, unknown>[];
		agentContinuationId?: string;
		agentTaskType?: string;
		workspaceId?: string;
		envState?: Record<string, unknown>;
		shellState?: Record<string, unknown>;
	};
	profileArn?: string;
}

interface KiroModule {
	initKiro(config: KiroPluginConfig): boolean;
	isKiroAvailable(): boolean;
	kiroChatRaw(request: KiroChatRequest, signal?: AbortSignal): AsyncGenerator<StreamEvent>;
	kiroChat(text: string, model?: KiroModel): AsyncGenerator<StreamEvent>;
	kiroGenerate(text: string, model?: KiroModel): Promise<string>;
	kiroGenerateWithMeta(
		text: string,
		model?: KiroModel,
	): Promise<{ text: string; contextPercent?: number }>;
	kiroMcpCall(name: string, args: Record<string, unknown>): Promise<McpResponse>;
	kiroMcpListTools(): Promise<McpResponse>;
	kiroSnapshot(): ManagerSnapshot | null;
	kiroSetDisabled(id: number, disabled: boolean): void;
	kiroSetPriority(id: number, priority: number): void;
	kiroResetAndEnable(id: number): void;
	kiroRemoveCredential(id: number): void;
	kiroManualRefresh(id: number): Promise<void>;
	kiroImportCredentials(creds: ImportCredentialInput[]): { added: number; duplicates: number };
	kiroGetUsage(id: number): Promise<UsageCacheEntry>;
	kiroGetAllUsageCache(): Record<string, UsageCacheEntry>;
	kiroListModels(
		credentialId?: number,
	): Promise<{ models: Array<Record<string, unknown>>; credentialId: number }>;
}

let mod: KiroModule | null = null;

try {
	// Path constructed at runtime so TypeScript doesn't resolve it statically.
	// This allows compilation even when server/lib/kiro/ is absent.
	const kiroPath = `./kiro/${"index"}`;
	mod = await import(kiroPath);
} catch {
	// kiro directory absent — plugin unavailable
}

export function initKiro(config: KiroPluginConfig): boolean {
	if (!mod) return false;
	return mod.initKiro(config);
}

export function isKiroAvailable(): boolean {
	return mod?.isKiroAvailable() ?? false;
}

export async function* kiroChat(text: string, model?: KiroModel): AsyncGenerator<StreamEvent> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	yield* mod.kiroChat(text, model);
}

export async function* kiroChatRaw(
	request: KiroChatRequest,
	signal?: AbortSignal,
): AsyncGenerator<StreamEvent> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	yield* mod.kiroChatRaw(request, signal);
}

export async function kiroGenerate(text: string, model?: KiroModel): Promise<string> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroGenerate(text, model);
}

export async function kiroGenerateWithMeta(
	text: string,
	model?: KiroModel,
): Promise<{ text: string; contextPercent?: number }> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroGenerateWithMeta(text, model);
}

export async function kiroMcpCall(
	name: string,
	args: Record<string, unknown>,
): Promise<McpResponse> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroMcpCall(name, args);
}

export async function kiroMcpListTools(): Promise<McpResponse> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroMcpListTools();
}

export function kiroSnapshot(): ManagerSnapshot | null {
	return mod?.kiroSnapshot() ?? null;
}

export function kiroSetDisabled(id: number, disabled: boolean): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroSetDisabled(id, disabled);
}

export function kiroSetPriority(id: number, priority: number): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroSetPriority(id, priority);
}

export function kiroResetAndEnable(id: number): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroResetAndEnable(id);
}

export function kiroRemoveCredential(id: number): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroRemoveCredential(id);
}

export async function kiroManualRefresh(id: number): Promise<void> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	await mod.kiroManualRefresh(id);
}

export function kiroImportCredentials(creds: ImportCredentialInput[]): {
	added: number;
	duplicates: number;
} {
	if (!mod) throw new Error("Kiro plugin is not installed");
	// Works even when kiro is not yet initialized (no credentials loaded)
	return mod.kiroImportCredentials(creds);
}

export async function kiroGetUsage(id: number): Promise<UsageCacheEntry> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroGetUsage(id);
}

export function kiroGetAllUsageCache(): Record<string, UsageCacheEntry> {
	return mod?.kiroGetAllUsageCache() ?? {};
}

export async function kiroListModels(credentialId?: number): Promise<{
	models: Array<Record<string, unknown>>;
	credentialId: number;
}> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroListModels(credentialId);
}

// --- Cached model list for system-wide use ---

const modelCachePath = resolve(homedir(), ".narrafork", "model-cache.json");

let cachedKiroModels: Array<Record<string, unknown>> = [];

/** Load model cache from disk (synchronous, called once at import / init time). */
function loadModelCacheFromDisk(): void {
	try {
		if (existsSync(modelCachePath)) {
			const raw = JSON.parse(readFileSync(modelCachePath, "utf-8"));
			if (Array.isArray(raw)) {
				cachedKiroModels = raw;
			}
		}
	} catch {
		// Corrupted file — ignore, will be overwritten on next refresh
	}
}

/** Persist model cache to disk. */
function saveModelCacheToDisk(models: Array<Record<string, unknown>>): void {
	try {
		mkdirSync(resolve(homedir(), ".narrafork"), { recursive: true });
		writeFileSync(modelCachePath, JSON.stringify(models, null, 2));
	} catch {
		// Non-critical — cache will still work in-memory
	}
}

// Eagerly load from disk so kiroGetCachedModels() returns data immediately
loadModelCacheFromDisk();

// Register kiro model checker and lister
registerKiroModelChecker((model) =>
	cachedKiroModels.some((m) => String(m.model_id ?? m.modelId ?? "") === model),
);
registerKiroModelLister(() =>
	cachedKiroModels
		.map((m) => String(m.model_id ?? m.modelId ?? ""))
		.filter(Boolean)
		.map((id) => `kiro:${id}`),
);

/** Fetch models from the API and cache them. Called once after kiro init. */
export async function kiroRefreshModelCache(): Promise<Array<Record<string, unknown>>> {
	if (!mod || !mod.isKiroAvailable()) return cachedKiroModels;
	try {
		const result = await mod.kiroListModels();
		cachedKiroModels = result.models.map((m) => {
			const name = String(m.model_name ?? m.modelName ?? "");
			return { ...m, model_short_name: name.replace(/^Claude\s+/i, "") };
		});
		saveModelCacheToDisk(cachedKiroModels);
		return cachedKiroModels;
	} catch {
		// Keep whatever was cached before (memory or disk)
		return cachedKiroModels;
	}
}

/** Return the cached kiro model list (synchronous). */
export function kiroGetCachedModels(): Array<Record<string, unknown>> {
	return cachedKiroModels;
}
