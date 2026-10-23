import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { ensurePrefix } from "../components/providers/types";
import { FOLLOW_DEFAULT_MODEL, FOLLOW_SUMMARY_MODEL } from "../lib/constants";
import { normalizeSettingsModelRef, readInstanceModelRefs } from "./useInstanceSettings";

// Issue #133: a summary model that follows the default model came back empty
// after "save + refresh". The stored sentinel survived, but the settings form
// pushed it through `ensurePrefix`, producing "openai:__default__" — a value no
// model list contains, so Mantine rendered the role as unset and the next save
// persisted the broken reference.

describe("settings model references", () => {
	test("a summary model following the default model survives a reload", () => {
		const refs = readInstanceModelRefs({ summaryModel: FOLLOW_DEFAULT_MODEL });

		// Must stay byte-identical to the sentinel: that is the option value the
		// summary Select offers ("follow default"), and the only key the model
		// grouping produces for it.
		expect(refs.summaryModel).toBe(FOLLOW_DEFAULT_MODEL);
		expect(refs.summaryModel).not.toContain(":");
	});

	test("legacy prefixed sentinels are healed instead of kept", () => {
		// Builds affected by the bug already wrote these into settings.json.
		expect(normalizeSettingsModelRef("openai:__default__")).toBe(FOLLOW_DEFAULT_MODEL);
		expect(normalizeSettingsModelRef("deepseek:__summary__")).toBe(FOLLOW_SUMMARY_MODEL);
		expect(normalizeSettingsModelRef("  openai:__default__  ")).toBe(FOLLOW_DEFAULT_MODEL);
	});

	test("concrete references and aggregations pass through untouched", () => {
		for (const model of [
			"custom:mimo-v2.6-flash",
			"tokendance:mimo-v2.6-flash",
			"__agg__:summary-pool",
			"__agg__:summary-pool:custom:mimo-v2.6-flash",
			"plugin-provider:__summary-model",
		]) {
			expect(normalizeSettingsModelRef(model)).toBe(model);
		}
	});

	test("sentinels are never given a provider prefix", () => {
		// The prefix must not be applied by any other caller either: a prefixed
		// sentinel is not a model ID and cannot match any option.
		expect(ensurePrefix(FOLLOW_DEFAULT_MODEL)).toBe(FOLLOW_DEFAULT_MODEL);
		expect(ensurePrefix(FOLLOW_SUMMARY_MODEL)).toBe(FOLLOW_SUMMARY_MODEL);
		expect(ensurePrefix("__agg__:summary-pool")).toBe("__agg__:summary-pool");
	});

	test("legacy bare model names still resolve to their provider", () => {
		expect(ensurePrefix("gpt-4o")).toBe("openai:gpt-4o");
		expect(ensurePrefix("claude-sonnet")).toBe("anthropic:claude-sonnet-4.5");
		expect(normalizeSettingsModelRef("gpt-4o")).toBe("openai:gpt-4o");
	});

	test("absent defaults follow the summary model, explicit empty stays empty", () => {
		const absent = readInstanceModelRefs({});
		expect(absent.translationModel).toBe(FOLLOW_SUMMARY_MODEL);
		expect(absent.promptOptimizeModel).toBe(FOLLOW_SUMMARY_MODEL);

		// "unset" and "" are different instructions server-side, so the form must
		// not invent a fallback for an explicitly cleared field.
		expect(normalizeSettingsModelRef("", FOLLOW_SUMMARY_MODEL)).toBe("");
		expect(normalizeSettingsModelRef(undefined, FOLLOW_SUMMARY_MODEL)).toBe(FOLLOW_SUMMARY_MODEL);
		expect(readInstanceModelRefs({ summaryModel: "" }).summaryModel).toBe("");
	});

	test("every role maps through the same reference normalization", () => {
		const refs = readInstanceModelRefs({
			defaultModel: "custom:default-model",
			summaryModel: FOLLOW_DEFAULT_MODEL,
			translationModel: FOLLOW_SUMMARY_MODEL,
			promptOptimizeModel: "custom:optimize-model",
			subagentModels: {
				explore: FOLLOW_DEFAULT_MODEL,
				plan: "custom:plan-model",
				search: "",
				review: "custom:review-model",
			},
		});

		expect(refs).toEqual({
			defaultModel: "custom:default-model",
			summaryModel: FOLLOW_DEFAULT_MODEL,
			translationModel: FOLLOW_SUMMARY_MODEL,
			promptOptimizeModel: "custom:optimize-model",
			subagentExploreModel: FOLLOW_DEFAULT_MODEL,
			subagentPlanModel: "custom:plan-model",
			subagentSearchModel: "",
			subagentReviewModel: "custom:review-model",
		});
	});

	// Guard against the call site drifting away from the normalizer: a role that
	// reaches `ensurePrefix` directly is how #133 happened in the first place.
	test("the form state builds its model roles only through readInstanceModelRefs", () => {
		const source = readFileSync(new URL("./useInstanceSettings.ts", import.meta.url), "utf8");

		expect(source).toContain("...readInstanceModelRefs(settings.agent)");
		expect(source).not.toMatch(/:\s*ensurePrefix\(settings\.agent/);
	});
});
