import type { ModelOption } from "../../lib/constants";

export type CustomApiProtocol =
	| "anthropic-official"
	| "anthropic-compatible"
	| "codex-native"
	| "responses-compatible"
	| "completions-compatible";

export interface CustomApiProviderState {
	id: string;
	name: string;
	prefix: string;
	apiKey: string;
	baseUrl: string;
	defaultModel: string;
	protocol: CustomApiProtocol;
	defaultContextWindow?: number;
	defaultReasoningEffort?: "none" | "low" | "medium" | "high" | null;
	tlsRejectUnauthorized?: boolean;
	codexAccountId: string;
	codexWebSocket?: boolean;
	codexWebSearch?: boolean;
	codexImageGeneration?: boolean;
	disabled?: boolean;
}

export interface OpenAIProviderState {
	id: string;
	name: string;
	prefix: string;
	apiKey: string;
	baseUrl: string;
	defaultModel: string;
	/** @deprecated Use apiMode. Preserved so legacy responsesApi=false migrates correctly. */
	responsesApi?: boolean;
	apiMode?: "responses" | "completions" | "codex";
	codexAccountId: string;
	codexWebSocket?: boolean;
	codexWebSearch?: boolean;
	codexImageGeneration?: boolean;
	disabled?: boolean;
}

export interface AnthropicProviderState {
	id: string;
	name: string;
	prefix: string;
	apiKey: string;
	baseUrl: string;
	defaultModel: string;
	defaultReasoningEffort?: "none" | "low" | "medium" | "high" | null;
	tlsRejectUnauthorized?: boolean;
	officialApi?: boolean;
	disabled?: boolean;
}

/** Kiro account tier — determines model availability. */
export type KiroAccountTier = "free" | "paid" | "enterprise";

/** Ensure a model value has a "provider:" prefix. */
export function ensurePrefix(val: string): string {
	if (!val || val.includes(":")) return val;
	if (["claude-haiku-4.5", "claude-sonnet-4.5", "claude-opus-4.5", "claude-opus-4.6"].includes(val))
		return `kiro:${val}`;
	// Legacy short names — map to full IDs
	if (val === "claude-haiku") return "kiro:claude-haiku-4.5";
	if (val === "claude-sonnet") return "kiro:claude-sonnet-4.5";
	if (val === "claude-opus") return "kiro:claude-opus-4.6";
	return `openai:${val}`;
}

/**
 * Classify a Kiro credential's account tier.
 *
 * Decision logic (in priority order):
 * 1. authMethod === "idc" or hasProfileArn or hasStartUrl → enterprise
 * 2. subscriptionTitle contains "enterprise" / "business" → enterprise
 * 3. subscriptionTitle contains "pro" / "individual" / "paid" / "builder" / "power" → paid
 * 4. subscriptionTitle contains "free" or is absent → free
 */
export function classifyKiroTier(entry: {
	authMethod?: string;
	hasProfileArn?: boolean;
	hasStartUrl?: boolean;
	subscriptionTitle?: string;
}): KiroAccountTier {
	// IDC auth, profileArn, or startUrl (AWS SSO) is the definitive enterprise signal
	if (entry.authMethod === "idc" || entry.hasProfileArn || entry.hasStartUrl) {
		return "enterprise";
	}

	const title = entry.subscriptionTitle;
	if (!title) return "free";
	const lower = title.toLowerCase();
	if (lower.includes("enterprise") || lower.includes("business")) return "enterprise";
	if (
		lower.includes("pro") ||
		lower.includes("individual") ||
		lower.includes("paid") ||
		lower.includes("builder") ||
		lower.includes("power")
	) {
		return "paid";
	}
	return "free";
}

/** Tier display config. */
export const TIER_CONFIG: Record<
	KiroAccountTier,
	{ color: string; i18nKey: string; descKey: string }
> = {
	free: { color: "gray", i18nKey: "kiroAccountFree", descKey: "kiroAccountFreeDesc" },
	paid: { color: "blue", i18nKey: "kiroAccountPaid", descKey: "kiroAccountPaidDesc" },
	enterprise: {
		color: "violet",
		i18nKey: "kiroAccountEnterprise",
		descKey: "kiroAccountEnterpriseDesc",
	},
};

/**
 * Determine which models are available for each account tier.
 * - free: only a subset (haiku-class models)
 * - pro: all models
 * - enterprise: only claude-* models
 */
export function getModelAvailability(models: ModelOption[]): {
	free: ModelOption[];
	paid: ModelOption[];
	enterprise: ModelOption[];
} {
	const claudeModels = models.filter((m) => {
		const id = m.value.replace(/^kiro:/, "");
		return id.startsWith("claude-");
	});
	const freeModels = models.filter((m) => {
		const id = m.value.replace(/^kiro:/, "");
		return id.includes("haiku") || id.includes("sonnet-4");
	});
	return {
		free: freeModels,
		paid: models, // all
		enterprise: claudeModels,
	};
}
