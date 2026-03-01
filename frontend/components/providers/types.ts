import type { ModelOption } from "../../lib/constants";

export interface OpenAIProviderState {
	id: string;
	name: string;
	prefix: string;
	apiKey: string;
	baseUrl: string;
	defaultModel: string;
	apiMode: "responses" | "completions" | "codex";
	codexAccountId: string;
}

/** Kiro account tier — determines model availability. */
export type KiroAccountTier = "free" | "paid" | "enterprise";

/** Ensure a model value has a "provider:" prefix. */
export function ensurePrefix(val: string): string {
	if (!val || val.includes(":")) return val;
	if (["claude-haiku", "claude-sonnet", "claude-opus"].includes(val)) return `kiro:${val}`;
	return `openai:${val}`;
}

/**
 * Classify a Kiro credential's account tier.
 *
 * Decision logic (in priority order):
 * 1. authMethod === "idc" or hasProfileArn → enterprise (IDC = AWS Identity Center)
 * 2. subscriptionTitle contains "enterprise" / "business" → enterprise
 * 3. subscriptionTitle contains "pro" / "individual" / "paid" / "builder" / "power" → paid
 * 4. subscriptionTitle contains "free" or is absent → free
 */
export function classifyKiroTier(entry: {
	authMethod?: string;
	hasProfileArn?: boolean;
	subscriptionTitle?: string;
}): KiroAccountTier {
	// IDC auth or profileArn presence is the definitive enterprise signal
	if (entry.authMethod === "idc" || entry.hasProfileArn) return "enterprise";

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
