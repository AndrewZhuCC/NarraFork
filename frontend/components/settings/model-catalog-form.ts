import { decimalValue, type JSONValue, type RawMetadata } from "@shared/model-catalog/card";

export type FieldEdit = { mode: "set"; value: string } | { mode: "unknown" } | { mode: "reset" };
export type CatalogEdits = Record<string, FieldEdit>;
export type FieldKind =
	| "integer"
	| "price"
	| "multiplier"
	| "boolean"
	| "list"
	| "modalities"
	| "mode"
	| "basis"
	| "string";

/** Editable raw keys. Each is a key the server's patch contract accepts, written as-is. */
export const catalogFields: ReadonlyArray<{
	key: string;
	section: "capabilities" | "prices";
	kind: FieldKind;
}> = [
	{ key: "working_context_tokens", section: "capabilities", kind: "integer" },
	{ key: "max_input_tokens", section: "capabilities", kind: "integer" },
	{ key: "max_output_tokens", section: "capabilities", kind: "integer" },
	{ key: "context_window", section: "capabilities", kind: "integer" },
	{ key: "supported_modalities", section: "capabilities", kind: "modalities" },
	{ key: "supported_output_modalities", section: "capabilities", kind: "modalities" },
	{ key: "supports_web_search", section: "capabilities", kind: "boolean" },
	{ key: "supports_reasoning", section: "capabilities", kind: "boolean" },
	{ key: "reasoning_mode", section: "capabilities", kind: "mode" },
	{ key: "reasoning_effort_levels", section: "capabilities", kind: "list" },
	{ key: "can_disable_reasoning", section: "capabilities", kind: "boolean" },
	{ key: "default_reasoning_effort", section: "capabilities", kind: "string" },
	...[
		"input_cost_per_token",
		"output_cost_per_token",
		"cache_read_input_token_cost",
		"cache_creation_input_token_cost",
	].map((key) => ({ key, section: "prices" as const, kind: "price" as const })),
	{ key: "long_context_input_token_threshold", section: "prices", kind: "integer" },
	{ key: "long_context_pricing_basis", section: "prices", kind: "basis" },
	{ key: "long_context_input_cost_multiplier", section: "prices", kind: "multiplier" },
	{ key: "long_context_output_cost_multiplier", section: "prices", kind: "multiplier" },
];
export const fieldOptions: Partial<Record<FieldKind, string[]>> = {
	boolean: ["true", "false"],
	mode: ["levels", "fixed", "budget", "unknown"],
	basis: ["full", "marginal"],
};

/** Prices are stored per token and shown per million tokens; the conversion is exact. */
export function fieldText(value: JSONValue | undefined, kind?: FieldKind): string {
	if (value === undefined || value === null) return "";
	if (kind === "price" && (typeof value === "number" || typeof value === "string"))
		return decimalValue(value, 6);
	return Array.isArray(value) ? value.join(", ") : String(value);
}

/** Only explicit user operations enter a patch. Rendering effective values is never a write. */
export function buildCatalogPatch(
	edits: CatalogEdits,
): { set?: Record<string, JSONValue>; reset?: string[] } | null {
	const set: Record<string, JSONValue> = {};
	const reset: string[] = [];
	for (const [key, edit] of Object.entries(edits)) {
		const field = catalogFields.find((candidate) => candidate.key === key);
		if (!field) throw new Error(`Unknown field: ${key}`);
		if (edit.mode === "reset") {
			reset.push(key);
			continue;
		}
		if (edit.mode === "unknown") {
			set[key] = null;
			continue;
		}
		const text = edit.value.trim();
		switch (field.kind) {
			case "integer": {
				const value = Number(text);
				if (!/^\d+$/.test(text) || !Number.isSafeInteger(value) || value <= 0) throw new Error(key);
				set[key] = value;
				break;
			}
			case "price":
				if (!/^(0|[1-9]\d*)(\.\d+)?$/.test(text)) throw new Error(key);
				set[key] = decimalValue(text, -6);
				break;
			case "multiplier":
				if (!/^(0|[1-9]\d*)(\.\d+)?$/.test(text) || Number(text) <= 0) throw new Error(key);
				set[key] = Number(text);
				break;
			case "boolean":
				if (text !== "true" && text !== "false") throw new Error(key);
				set[key] = text === "true";
				break;
			case "list":
			case "modalities": {
				const values = text
					.split(",")
					.map((v) => v.trim())
					.filter(Boolean);
				if (
					field.kind === "modalities" &&
					values.some((v) => !["text", "image", "audio", "video"].includes(v))
				)
					throw new Error(key);
				set[key] = values;
				break;
			}
			case "mode":
			case "basis":
				if (!fieldOptions[field.kind]!.includes(text)) throw new Error(key);
				set[key] = text;
				break;
			default:
				set[key] = text;
		}
	}
	if (!Object.keys(set).length && !reset.length) return null;
	return { ...(Object.keys(set).length ? { set } : {}), ...(reset.length ? { reset } : {}) };
}

/** A new definition's metadata: the values set, with nothing to reset. */
export function metadataFromPatch(patch: { set?: Record<string, JSONValue> } | null): RawMetadata {
	return { ...(patch?.set ?? {}) };
}
