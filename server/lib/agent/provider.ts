import { getCodexManager } from "../codex-manager";
import { isKiroAvailable } from "../kiro-adapter";
import { logger } from "../logger";
import {
	getAnthropicProviderConfig,
	getNkpProviderConfig,
	getOpenaiProviderConfig,
	parseModelId,
	resolveProvider,
	settings,
} from "../settings";
import { AnthropicProvider } from "./anthropic-provider";
import { CodexProvider } from "./codex-provider";
import { KiroProvider } from "./kiro-provider";
import { NkpProvider } from "./nkp-provider";
import { OpenAIProvider } from "./openai-provider";
import type { AgentToolUse } from "./types";

function isKiroProviderDisabled(): boolean {
	return process.env.NARRAFORK_DISABLE_KIRO_PROVIDER === "1";
}

// === Provider-agnostic DB types (used by buildHistory) ===

export interface DbMessage {
	id: string;
	role: "user" | "assistant" | "system";
	contentJson: unknown;
	contentText: string | null;
	parentToolUseId: string | null;
	messageUuid: string | null;
	toolCalls?: DbToolCall[];
}

export interface DbToolCall {
	toolUseId: string;
	toolName: string;
	inputJson: unknown;
	outputJson: unknown;
	status: string;
}

// === Stream event emitted by provider.chat() ===

export interface ParsedStreamEvent {
	text?: string;
	toolUses?: AgentToolUse[];
	messageId?: string;
	conversationId?: string;
	reasoning?: string;
	/** Provider metadata for reasoning continuation (Codex encrypted content, item ID) */
	reasoningMetadata?: import("./types").ReasoningProviderMetadata;
	contextUsagePercentage?: number;
	metering?: { unit: string; unitPlural: string; usage: number };
	invalidState?: { reason: string; message: string };
	/** Kiro credential ID that served this request */
	credentialId?: string;
	/** NKP quota balance returned after each request */
	nkpQuotaBalance?: number | null;
	/** Streaming tool use chunk — accumulated by the loop */
	toolUseChunk?: {
		toolUseId: string;
		name?: string;
		input?: string;
		stop?: boolean;
	};
	/** Token usage info from OpenAI-compatible APIs (used to compute context usage %) */
	usage?: {
		promptTokens: number;
		completionTokens?: number;
		/** Reasoning tokens (o1/o3 models) */
		reasoningTokens?: number;
		/** Cached input tokens (prompt caching) */
		cachedInputTokens?: number;
	};
	/** Web search lifecycle event from Responses API (Codex native web_search tool) */
	webSearch?: {
		id: string;
		status: "in_progress" | "searching" | "completed";
		/** Search query (available on completion) */
		query?: string;
		queries?: string[];
	};
	/** Internal: set when Responses API format is detected from the gateway */
	_responsesApi?: boolean;
}

// === Chat parameters passed to provider.chat() ===

export interface ChatParams {
	conversationId: string;
	content: string;
	model: string;
	cwd: string;
	history: unknown[];
	tools: unknown[];
	toolResults: unknown[];
	signal: AbortSignal;
	/**
	 * Sticky session key for provider-side account affinity.
	 * For narrator loops this is narratorId.
	 */
	stickySessionKey?: string;
	/** Base64-encoded images to attach to the current user message */
	images?: Array<{ format: string; base64: string }>;
	/** Reasoning effort for Codex-mode providers (low, medium, high, xhigh) */
	reasoningEffort?: "low" | "medium" | "high" | "xhigh";
	/** Service tier for Codex-mode providers — "priority" enables fast mode */
	serviceTier?: string;
}

// === The adapter interface ===

export interface ProviderAdapter {
	/** Convert ToolDefinition[] to provider-specific tool format */
	formatTools(tools: import("./types").ResolvedToolDefinition[]): unknown[];

	/** Convert DB messages to provider history + trailing tool results */
	buildHistory(
		dbMessages: DbMessage[],
		model: string,
		narratorId?: string,
	): Promise<{ history: unknown[]; trailingToolResults: unknown[] }>;

	/** Inject system prompt into the history array (mutates in place) */
	injectSystemPrompt(
		history: unknown[],
		systemPrompt: string,
		model: string,
		locale?: string,
	): void;

	/** Stream a chat completion, yielding parsed events */
	chat(params: ChatParams): AsyncGenerator<ParsedStreamEvent>;

	/** Format a single tool result for the provider protocol */
	formatToolResult(toolUseId: string, output: string, isError: boolean): unknown;

	/** Append a user turn to history (mutates in place) */
	pushUserTurn(history: unknown[], content: string, model: string, toolResults: unknown[]): void;

	/** Append an assistant turn to history (mutates in place) */
	pushAssistantTurn(
		history: unknown[],
		text: string,
		toolUses: AgentToolUse[],
		reasoningBlocks?: Array<{
			text: string;
			providerMetadata?: import("./types").ReasoningProviderMetadata;
		}>,
	): void;

	/** Simple text generation — no tools, no loop. Returns generated text. */
	generate(text: string, model: string): Promise<string>;

	/**
	 * Like generate() but also returns contextUsagePercentage if available.
	 * `systemInstruction` is optional and lets providers with a native system/developer
	 * channel place instructions outside the user text.
	 */
	generateWithMeta(
		text: string,
		model: string,
		systemInstruction?: string,
	): Promise<{ text: string; contextPercent?: number }>;

	/**
	 * Generate text using a history-based conversation (system instruction + user content).
	 * Used for title generation where we need to separate instruction from content.
	 */
	generateWithHistory(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
	): Promise<string>;
}

// === Provider resolution ===

function createProviderByName(provider: string): ProviderAdapter | null {
	if (provider === "kiro") {
		if (isKiroProviderDisabled()) return null;
		return new KiroProvider();
	}
	if (provider === "codex") return new CodexProvider();

	const anthropicConfig = getAnthropicProviderConfig(provider);
	if (anthropicConfig) {
		return new AnthropicProvider(anthropicConfig);
	}

	const nkpConfig = getNkpProviderConfig(provider);
	if (nkpConfig) {
		return new NkpProvider(nkpConfig);
	}

	const openaiConfig = getOpenaiProviderConfig(provider);
	if (openaiConfig) {
		return new OpenAIProvider(openaiConfig);
	}

	return null;
}

function getConfiguredNonKiroProviders(): string[] {
	const seen = new Set<string>();
	const providers: string[] = [];

	for (const p of settings.openaiProviders ?? []) {
		if (!p.apiKey) continue;
		if (!seen.has(p.prefix)) {
			seen.add(p.prefix);
			providers.push(p.prefix);
		}
	}

	for (const p of settings.anthropicProviders ?? []) {
		if (!p.apiKey) continue;
		if (!seen.has(p.prefix)) {
			seen.add(p.prefix);
			providers.push(p.prefix);
		}
	}

	for (const p of settings.nkpProviders ?? []) {
		if (!p.apiKey || !p.baseUrl) continue;
		if (!seen.has(p.prefix)) {
			seen.add(p.prefix);
			providers.push(p.prefix);
		}
	}

	if (!seen.has("codex") && settings.codex) {
		try {
			if (getCodexManager().availableCount > 0) {
				providers.push("codex");
			}
		} catch {
			// Ignore codex manager errors during fallback discovery
		}
	}

	return providers;
}

function defaultModelForProvider(provider: string): string | null {
	if (provider === "codex") {
		const custom = settings.agent.customModels ?? [];
		const codexCustom = custom.find((m) => m.provider === "codex")?.value;
		if (codexCustom) {
			return codexCustom.includes(":") ? codexCustom : `codex:${codexCustom}`;
		}
		return "codex:gpt-5.3-codex";
	}

	if (provider === "kiro") {
		return "kiro:claude-sonnet";
	}

	const openai = getOpenaiProviderConfig(provider);
	if (openai?.defaultModel) {
		return `${provider}:${openai.defaultModel}`;
	}

	const anthropic = getAnthropicProviderConfig(provider);
	if (anthropic?.defaultModel) {
		return `${provider}:${anthropic.defaultModel}`;
	}

	const nkp = getNkpProviderConfig(provider);
	if (nkp?.defaultModel) {
		return `${provider}:${nkp.defaultModel}`;
	}

	const custom = settings.agent.customModels ?? [];
	const providerCustom = custom.find((m) => m.provider === provider)?.value;
	if (providerCustom) {
		return providerCustom.includes(":") ? providerCustom : `${provider}:${providerCustom}`;
	}

	return null;
}

export interface ProviderResolution {
	requestedProvider: string;
	requestedModel: string;
	provider: string;
	adapter: ProviderAdapter;
	model: string;
}

function buildResolution(
	requestedProvider: string,
	requestedModel: string,
	provider: string,
	adapter: ProviderAdapter,
): ProviderResolution {
	const bareRequestedModel = parseModelId(requestedModel).model;
	const model =
		provider === requestedProvider
			? requestedModel
			: (defaultModelForProvider(provider) ?? `${provider}:${bareRequestedModel || "default"}`);
	return {
		requestedProvider,
		requestedModel,
		provider,
		adapter,
		model,
	};
}

function getFirstAvailableNonKiroResolution(
	requestedProvider: string,
	requestedModel: string,
): ProviderResolution | null {
	for (const name of getConfiguredNonKiroProviders()) {
		const adapter = createProviderByName(name);
		if (adapter) {
			return buildResolution(requestedProvider, requestedModel, name, adapter);
		}
	}
	return null;
}

export function resolveProviderAndModel(model?: string): ProviderResolution {
	const requestedModel = model ?? settings.agent.defaultModel;
	const requestedProvider = resolveProvider(requestedModel);

	if (requestedProvider === "kiro") {
		if (isKiroProviderDisabled()) {
			const fallback = getFirstAvailableNonKiroResolution(requestedProvider, requestedModel);
			if (fallback) {
				logger.warn("Kiro provider disabled by environment variable, using fallback provider", {
					requestedProvider,
					requestedModel,
					fallbackProvider: fallback.provider,
					fallbackModel: fallback.model,
					env: "NARRAFORK_DISABLE_KIRO_PROVIDER",
				});
				return fallback;
			}
			throw new Error("Kiro provider disabled and no non-Kiro provider is configured.");
		}

		if (isKiroAvailable()) {
			return buildResolution(requestedProvider, requestedModel, "kiro", new KiroProvider());
		}

		const fallback = getFirstAvailableNonKiroResolution(requestedProvider, requestedModel);
		if (fallback) {
			logger.warn("Kiro provider unavailable, falling back to non-Kiro provider", {
				requestedProvider,
				requestedModel,
				fallbackProvider: fallback.provider,
				fallbackModel: fallback.model,
			});
			return fallback;
		}

		throw new Error("Kiro provider unavailable and no non-Kiro provider is configured.");
	}

	const explicit = createProviderByName(requestedProvider);
	if (explicit) {
		return buildResolution(requestedProvider, requestedModel, requestedProvider, explicit);
	}

	const fallback = getFirstAvailableNonKiroResolution(requestedProvider, requestedModel);
	if (fallback) {
		logger.warn("Unknown provider requested, falling back to configured provider", {
			requestedProvider,
			requestedModel,
			fallbackProvider: fallback.provider,
			fallbackModel: fallback.model,
		});
		return fallback;
	}

	if (!isKiroProviderDisabled() && isKiroAvailable()) {
		logger.warn("Unknown provider requested, falling back to Kiro", {
			requestedProvider,
			requestedModel,
			fallbackModel: "kiro:claude-sonnet",
		});
		return buildResolution(requestedProvider, requestedModel, "kiro", new KiroProvider());
	}

	throw new Error(
		`Provider "${requestedProvider}" is not configured, and no fallback provider is available.`,
	);
}

export function getProvider(provider: string): ProviderAdapter {
	const explicit = createProviderByName(provider);
	if (explicit) return explicit;
	const fallback = resolveProviderAndModel(`${provider}:default`);
	return fallback.adapter;
}
