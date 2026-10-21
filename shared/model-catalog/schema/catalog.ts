/**
 * Model catalog contract. There is exactly one metadata representation: the open raw
 * map of source keys (LiteLLM style, e.g. `max_input_tokens`, `input_cost_per_token`).
 * It is what the public catalog publishes, what settings.json stores, what the runtime
 * keeps in memory and what every editor patches. Typed reads go through
 * `../profile`, which derives values and is never stored or converted back.
 */
export type JSONValue =
	| null
	| boolean
	| number
	| string
	| JSONValue[]
	| { [key: string]: JSONValue };
export type RawMetadata = { [key: string]: JSONValue };

export interface MetadataSource {
	label: string;
	url?: string;
	fields?: string[];
	verifiedAt?: string;
}
export interface MatchRules {
	ids?: string[];
	aliases?: string[];
	prefixes?: string[];
	volatileSuffixes?: boolean;
	/** Field groups a weak (prefix/suffix) match may borrow; see `MATCH_FIELD_GROUPS`. */
	fields?: string[];
}
export type EntryStatus = "verified" | "unverified" | "legacy-unverified" | "deprecated";
export interface RawModel {
	id: string;
	name?: string;
	vendor?: string;
	family?: string;
	notes?: string;
	matches?: MatchRules;
	metadata: RawMetadata;
	status?: EntryStatus;
	sources?: MetadataSource[];
}
export interface RawVariant extends RawModel {
	modelId: string;
	providerKey: string;
	upstreamModelIds: string[];
}
export interface RawCatalog {
	schemaVersion: 2;
	/** Format-qualified storage key; pinning uses sourceVersion, not this key. */
	catalogVersion: string;
	sourceVersion: string;
	publishedAt: string;
	models: RawModel[];
	variants: RawVariant[];
}
export interface RawBinding {
	id: string;
	providerId?: string;
	channelId?: string;
	upstreamModelId: string;
	modelId?: string;
	variantId?: string;
	overrides?: RawMetadata;
}
export interface RawOverride {
	target: "model" | "variant" | "binding";
	targetId: string;
	metadata: RawMetadata;
	source?: "user" | "legacy-local";
}
export interface RawLocalState {
	revision: number;
	/** Read-only migration provenance, keyed by target:id; edits clear only their own paths. */
	legacyFields?: Record<string, string[]>;
	models?: RawModel[];
	variants?: RawVariant[];
	bindings?: RawBinding[];
	overrides?: RawOverride[];
	/**
	 * Overrides for one provider's entry, keyed by the full model value a provider list
	 * shows (`provider:model`). They apply by that name, never assert an identity, and sit
	 * above every other local layer: a per-provider decision beats a model-wide one.
	 */
	connections?: Record<string, RawMetadata>;
	hiddenModelIds?: string[];
	hiddenVariantIds?: string[];
}
export interface ModelQuery {
	upstreamModelId: string;
	providerKey?: string;
	providerId?: string;
	channelId?: string;
	modelId?: string;
	variantId?: string;
}
export type MetadataLayer =
	| "default"
	| "preset-model"
	| "preset-variant"
	| "discovered"
	| "local-model"
	| "local-variant"
	| "local-binding"
	| "local-connection";
export interface FieldSource {
	layer: MetadataLayer;
	id?: string;
	catalogVersion?: string;
	explicit?: boolean;
	legacy?: boolean;
}
export type MatchedVia = "binding" | "exact" | "alias" | "suffix" | "prefix" | "none";
