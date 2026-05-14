// Kiro adapter — the sole contact point between NarraFork core and the kiro plugin.
// If the kiro directory is missing, everything degrades gracefully.
//
// Types are inlined here so this file compiles even when server/lib/kiro/ is absent.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { registerKiroModelChecker, registerKiroModelLister, settings } from "./settings";

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
	id: string;
	priority: number;
	disabled: boolean;
	disabledReason?: DisabledReason;
	failureCount: number;
	successCount: number;
	lastUsedAt?: string;
	email?: string;
	displayName?: string;
	hasProfileArn: boolean;
	expiresAt?: string;
	authMethod?: string;
	subscriptionTitle?: string;
	region?: string;
	meterUsage?: number;
	availableModels?: string[];
}

export interface ManagerSnapshot {
	entries: CredentialSnapshot[];
	currentId: string;
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
	id: string;
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

export interface KiroHistoryUserMessage {
	userInputMessage: {
		content: string;
		modelId: string;
		origin?: string;
		images?: KiroImage[];
		userInputMessageContext?: KiroUserInputMessageContext;
	};
}

export interface KiroHistoryAssistantMessage {
	assistantResponseMessage: {
		content?: string;
		toolUses?: Array<{
			toolUseId: string;
			name: string;
			input: Record<string, unknown>;
		}>;
	};
}

export type KiroHistoryMessage = KiroHistoryUserMessage | KiroHistoryAssistantMessage;

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
		history?: KiroHistoryMessage[];
		agentContinuationId?: string;
		agentTaskType?: string;
		workspaceId?: string;
		envState?: Record<string, unknown>;
		shellState?: Record<string, unknown>;
	};
	profileArn?: string;
}

export interface KiroModule {
	initKiro(config: KiroPluginConfig): boolean;
	isKiroAvailable(): boolean;
	kiroChatRaw(request: KiroChatRequest, signal?: AbortSignal): AsyncGenerator<StreamEvent>;
	kiroChat(text: string, model?: KiroModel): AsyncGenerator<StreamEvent>;
	kiroGenerate(text: string, model?: KiroModel): Promise<string>;
	kiroGenerateWithMeta(
		text: string,
		model?: KiroModel,
	): Promise<{ text: string; contextPercent?: number }>;
	kiroMcpCall(
		name: string,
		args: Record<string, unknown>,
		signal?: AbortSignal,
	): Promise<McpResponse>;
	kiroMcpListTools(): Promise<McpResponse>;
	kiroSnapshot(): ManagerSnapshot | null;
	kiroSetDisabled(id: string, disabled: boolean): void;
	kiroSetLoadBalancingMode(mode: "priority" | "balanced"): void;
	kiroSetPriority(id: string, priority: number): void;
	kiroResetAndEnable(id: string): void;
	kiroRemoveCredential(id: string): void;
	kiroManualRefresh(id: string): Promise<void>;
	kiroUpdateCredential(
		id: string,
		fields: { email?: string; displayName?: string; region?: string },
	): void;
	kiroImportCredentials(creds: ImportCredentialInput[]): { added: number; duplicates: number };
	kiroGetUsage(id: string): Promise<UsageCacheEntry>;
	kiroGetAllUsageCache(): Record<string, UsageCacheEntry>;
	kiroAddMeterUsage(credentialId: string, amount: number): void;
	kiroListModels(
		credentialId?: string,
	): Promise<{ models: Array<Record<string, unknown>>; credentialId: string }>;
	kiroFetchCredentialModels(credentialId: string): Promise<string[]>;
	kiroFetchAllCredentialModels(): Promise<Array<Record<string, unknown>>>;
	registerProxyResolver(resolver: () => string | undefined): void;
}

let mod: KiroModule | null = null;
const isKiroPluginDisabled = process.env.NARRAFORK_DISABLE_KIRO_PLUGIN === "1";

if (!isKiroPluginDisabled) {
	try {
		// Path constructed at runtime so TypeScript doesn't resolve it statically.
		// This allows compilation even when server/lib/kiro/ is absent.
		const kiroPath = `./kiro/${"index"}`;
		const loaded: KiroModule = await import(kiroPath);
		mod = loaded;
		// Register proxy resolver so kiro plugin reads proxy from settings at runtime
		loaded.registerProxyResolver(() => settings.kiro?.proxy);
	} catch {
		// kiro directory absent — plugin unavailable
	}
}

export function initKiro(config: KiroPluginConfig): boolean {
	if (isKiroPluginDisabled) return false;
	if (!mod) return false;
	return mod.initKiro(config);
}

export function isKiroAvailable(): boolean {
	return mod?.isKiroAvailable() ?? false;
}

/** Test helper: replace the optional Kiro plugin module for runtime fixture tests. */
export function __setKiroModuleForTests(module: Partial<KiroModule> | null): void {
	mod = module as KiroModule | null;
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
	signal?: AbortSignal,
): Promise<McpResponse> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroMcpCall(name, args, signal);
}

export async function kiroMcpListTools(): Promise<McpResponse> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroMcpListTools();
}

export function kiroSnapshot(): ManagerSnapshot | null {
	return mod?.kiroSnapshot() ?? null;
}

export function kiroSetDisabled(id: string, disabled: boolean): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroSetDisabled(id, disabled);
}

export function kiroSetLoadBalancingMode(mode: "priority" | "balanced"): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroSetLoadBalancingMode(mode);
}

export function kiroSetPriority(id: string, priority: number): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroSetPriority(id, priority);
}

export function kiroResetAndEnable(id: string): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroResetAndEnable(id);
}

export function kiroRemoveCredential(id: string): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroRemoveCredential(id);
}

export async function kiroManualRefresh(id: string): Promise<void> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	await mod.kiroManualRefresh(id);
}

export function kiroUpdateCredential(
	id: string,
	fields: { email?: string; displayName?: string; region?: string },
): void {
	if (!mod) throw new Error("Kiro plugin is not installed");
	mod.kiroUpdateCredential(id, fields);
}

export function kiroImportCredentials(creds: ImportCredentialInput[]): {
	added: number;
	duplicates: number;
} {
	if (!mod) throw new Error("Kiro plugin is not installed");
	// Works even when kiro is not yet initialized (no credentials loaded)
	return mod.kiroImportCredentials(creds);
}

export async function kiroGetUsage(id: string): Promise<UsageCacheEntry> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroGetUsage(id);
}

export function kiroGetAllUsageCache(): Record<string, UsageCacheEntry> {
	return mod?.kiroGetAllUsageCache() ?? {};
}

/** Accumulate metering usage for a credential (debounced persistence). */
export function kiroAddMeterUsage(credentialId: string, amount: number): void {
	mod?.kiroAddMeterUsage(credentialId, amount);
}

export async function kiroListModels(credentialId?: string): Promise<{
	models: Array<Record<string, unknown>>;
	credentialId: string;
}> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroListModels(credentialId);
}

/** Fetch and store the model list for a single credential. */
export async function kiroFetchCredentialModels(credentialId: string): Promise<string[]> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroFetchCredentialModels(credentialId);
}

/** Fetch model lists for all credentials, store per-credential, return deduplicated union. */
export async function kiroFetchAllCredentialModels(): Promise<Array<Record<string, unknown>>> {
	if (!mod) throw new Error("Kiro plugin is not installed");
	return mod.kiroFetchAllCredentialModels();
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
	if (!mod?.isKiroAvailable()) return cachedKiroModels;
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
