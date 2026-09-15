import { cardEffortLevels, lookupModelCard } from "@shared/model-card";
import { clampReasoningEffort, type ReasoningEffort } from "@shared/reasoning-effort";
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

/** Shared narrator policy, including the legacy DeepSeek-before-model-card priority. */
export function getModelReasoningEffort({
	model,
	modelOption,
	settings,
	modelCardIndex,
}: {
	model?: string;
	modelOption?: ModelOption;
	settings?: EffortSettings;
	modelCardIndex?: Parameters<typeof lookupModelCard>[1] | null;
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
	if (model.toLowerCase().includes("deepseek"))
		return { supported, options: ["none", "high", "xhigh"] };
	const cardTiers = modelCardIndex
		? cardEffortLevels(lookupModelCard(bare, modelCardIndex)?.card)
		: undefined;
	// Cards omit `none` so clamping low cannot accidentally disable thinking.
	// Append it only for models whose existing narrator policy allows reasoning off.
	if (cardTiers?.length) options = bare !== "gpt-6-astra" ? ["none", ...cardTiers] : cardTiers;
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
		(desired === "low" || desired === "medium")
	)
		effort = "high";
	return clampReasoningEffort(effort, options);
}
