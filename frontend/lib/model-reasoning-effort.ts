import type { ModelCard } from "@shared/model-catalog/card";
import { modelProfile } from "@shared/model-catalog/profile";
import {
	clampReasoningEffort,
	REASONING_EFFORT_VALUES,
	type ReasoningEffort,
} from "@shared/reasoning-effort";
import {
	claudeVersionAtLeast,
	GENERIC_REASONING_EFFORT_TIERS,
	modelAcceptsReasoningEffort,
	parseClaudeModel,
} from "@shared/reasoning-effort-support";
import type { ModelOption } from "./constants";

interface EffortSettings {
	codexAvailable?: boolean;
	openaiProviders?: { prefix?: string; apiMode?: string }[];
	anthropicProviders?: { prefix?: string }[];
	geminiProviders?: { prefix?: string }[];
	agent?: { reasoningEffortBlocklist?: Parameters<typeof modelAcceptsReasoningEffort>[1] };
}

export function getCodexCapableProviders(settings?: EffortSettings): ReadonlySet<string> {
	const providers = new Set<string>();
	if (settings?.codexAvailable) providers.add("codex");
	for (const provider of settings?.openaiProviders ?? []) {
		if (provider?.prefix && (provider.apiMode ?? "responses") === "codex")
			providers.add(provider.prefix);
	}
	return providers;
}

function getBareModelForReasoning(model?: string, modelOption?: ModelOption): string {
	if (modelOption?.bareModel) return modelOption.bareModel;
	if (!model) return "";
	const modelWithoutProvider = model.includes(":") ? model.split(":").slice(1).join(":") : model;
	const channel = modelOption?.channel ?? modelWithoutProvider.split(":")[0];
	if (channel && modelWithoutProvider.startsWith(`${channel}:`))
		return modelWithoutProvider.slice(channel.length + 1);
	return modelWithoutProvider.startsWith("codex:")
		? modelWithoutProvider.slice("codex:".length)
		: modelWithoutProvider;
}

export function isCodexReasoningChannel(model?: string, option?: ModelOption): boolean {
	return (
		option?.channelType?.toLowerCase() === "codex" ||
		!!model?.slice(model.indexOf(":") + 1).startsWith("codex:")
	);
}

const CODEX_OPTIONS: Record<string, readonly ReasoningEffort[]> = {
	"gpt-6-astra": ["low", "medium", "high", "xhigh", "max"],
	"gpt-5.6-sol": ["none", "low", "medium", "high", "xhigh", "max"],
	"gpt-5.6-terra": ["none", "low", "medium", "high", "xhigh", "max"],
	"gpt-5.6-luna": ["none", "low", "medium", "high", "xhigh", "max"],
	"gpt-5.5": ["none", "low", "medium", "high", "xhigh"],
};

/** Shared narrator policy: declared model-card tiers precede legacy model-name fallbacks. */
export function getModelReasoningEffort({
	model,
	modelOption,
	settings,
	modelCard,
}: {
	model?: string;
	modelOption?: ModelOption;
	settings?: EffortSettings;
	modelCard?: ModelCard | null;
}): { supported: boolean; options: readonly ReasoningEffort[] } {
	const prefix = model?.split(":")[0];
	const bare = getBareModelForReasoning(model, modelOption);
	const codex =
		(!!prefix && getCodexCapableProviders(settings).has(prefix)) ||
		isCodexReasoningChannel(model, modelOption);
	const supported =
		!!prefix &&
		(codex || modelAcceptsReasoningEffort(bare, settings?.agent?.reasoningEffortBlocklist));
	let options: readonly ReasoningEffort[] = GENERIC_REASONING_EFFORT_TIERS;
	if (!model) return { supported, options };
	const profile =
		modelCard && modelCard.matchedVia !== "none" ? modelProfile(modelCard) : undefined;
	const cardTiers = profile?.reasoning.levels?.filter(
		(level): level is ReasoningEffort =>
			level !== "none" && REASONING_EFFORT_VALUES.includes(level as ReasoningEffort),
	);
	// Cards omit `none` so clamping low cannot accidentally disable thinking.
	// Append it only when the card and existing narrator policy allow reasoning off.
	if (cardTiers?.length)
		options =
			profile?.reasoning.canDisable !== false && bare !== "gpt-6-astra"
				? ["none", ...cardTiers]
				: cardTiers;
	else if (model.toLowerCase().includes("deepseek")) options = ["none", "high", "xhigh"];
	else if (codex) options = CODEX_OPTIONS[bare] ?? ["none", "low", "medium", "high"];
	else if (
		modelOption?.channelType === "anthropic" ||
		(!!prefix && settings?.anthropicProviders?.some((p) => p.prefix === prefix))
	) {
		const parsed = parseClaudeModel(bare);
		const xhigh =
			parsed &&
			(parsed.family === "fable" ||
				parsed.family === "mythos" ||
				((parsed.family === "sonnet" || parsed.family === "opus") &&
					claudeVersionAtLeast(parsed, 4, 7)));
		options = xhigh
			? ["none", "low", "medium", "high", "xhigh", "max"]
			: ["none", "low", "medium", "high", "max"];
	} else if (prefix && settings?.geminiProviders?.some((p) => p.prefix === prefix))
		options = ["none", "low", "medium", "high"];
	return { supported, options };
}

export function displayedModelReasoningEffort(
	model: string | undefined,
	desired: ReasoningEffort | null | undefined,
	defaultEffort: ReasoningEffort,
	options: readonly ReasoningEffort[],
): ReasoningEffort {
	let effort = desired ?? defaultEffort;
	if (
		desired &&
		model?.toLowerCase().includes("deepseek") &&
		(desired === "low" || desired === "medium") &&
		!options.includes(desired)
	)
		effort = "high";
	return clampReasoningEffort(effort, options);
}
