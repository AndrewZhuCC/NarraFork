/** Kept dependency-free so permission gates never load the preview compiler. */
export const GEN_UI_TOOL_NAME = "GenUI";
/**
 * What this tool was called before it became GenUI. Recorded tool calls, narrators'
 * `enabledTools` and exported transcripts still carry it, so everything that reads
 * history accepts both; nothing new is ever written under it.
 */
export const GEN_UI_LEGACY_TOOL_NAME = "IntelligentUI";
export const GEN_UI_TOOL_NAMES: readonly string[] = [GEN_UI_TOOL_NAME, GEN_UI_LEGACY_TOOL_NAME];

/** A recorded call of this tool, under its current or its former name. */
export function isGenUiToolName(name: unknown): boolean {
	return name === GEN_UI_TOOL_NAME || name === GEN_UI_LEGACY_TOOL_NAME;
}

/** Result metadata keys, each with the name results were recorded under before the rename. */
const LEGACY_METADATA_KEYS = Object.freeze({
	genUi: "intelligentUi",
	genUiInline: "intelligentUiInline",
	genUiKit: "intelligentUiKit",
});
export type GenUiMetadataKey = keyof typeof LEGACY_METADATA_KEYS;

/** Read a GenUI field of a tool result's metadata, whichever name it was recorded under. */
export function readGenUiMetadata(
	metadata: Record<string, unknown> | null | undefined,
	key: GenUiMetadataKey,
): unknown {
	if (!metadata) return undefined;
	return metadata[key] ?? metadata[LEGACY_METADATA_KEYS[key]];
}
export const GEN_UI_ACTIONS = ["create", "show", "render", "kit_status", "kit_build"] as const;
export type GenUiToolAction = (typeof GEN_UI_ACTIONS)[number];

/**
 * Actions that write nothing to the workspace. `render` only displays markup carried in
 * its own input. Unknown/future actions remain mutating until explicitly classified.
 */
export function isGenUiReadAction(action: unknown): boolean {
	return action === "show" || action === "render" || action === "kit_status";
}

/** Inline card heights. Fixed per size so the chat can lay rows out without measuring. */
export const GEN_UI_INLINE_HEIGHTS = Object.freeze({ sm: 240, md: 400, lg: 580 });
export type GenUiInlineSize = keyof typeof GEN_UI_INLINE_HEIGHTS;

export function readGenUiInlineSize(value: unknown): GenUiInlineSize {
	return value === "sm" || value === "lg" ? value : "md";
}

/**
 * How the agent loop reads a call's arguments while the model is still writing them:
 * `short` fields are extracted whole, `large` ones are forwarded delta by delta. `source`
 * is the UI itself; the short fields decide the card's kind and height before it starts.
 */
export const GEN_UI_STREAM_FIELDS = Object.freeze({
	short: ["action", "title", "size"],
	large: ["source"],
});
