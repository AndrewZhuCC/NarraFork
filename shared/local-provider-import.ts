/**
 * Wire types for importing provider settings that other AI tools on the server
 * machine already hold (Claude Code, Codex, OpenCode, DeepSeek Harness).
 *
 * The scan response never carries a key: only a masked preview. The real key is
 * returned by the separate resolve call, which the UI makes only for the
 * candidates the admin explicitly chose to import.
 */

export const LOCAL_IMPORT_SOURCES = ["claude", "codex", "opencode", "deepseek-harness"] as const;

export type LocalImportSource = (typeof LOCAL_IMPORT_SOURCES)[number];

export type LocalImportProtocol =
	| "anthropic-messages"
	| "openai-responses"
	| "completions-compatible"
	| "gemini-compatible";

/** Upper bound on how many candidates one resolve request may ask for. */
export const LOCAL_IMPORT_MAX_RESOLVE_IDS = 32;

export interface LocalProviderCandidateSummary {
	/** Stable for the same source/provider/base URL across scans. */
	id: string;
	source: LocalImportSource;
	name: string;
	protocol: LocalImportProtocol;
	baseUrl: string;
	/** Masked key (last 4 characters), or "" when the tool has no key for it. */
	apiKeyPreview: string;
	hasKey: boolean;
	/** The file the provider was found in, for the admin to recognize it. */
	configPath: string;
	/** The tool's own default model for this provider, when it names one. */
	model?: string;
	/** A NarraFork provider with the same base URL and key already exists. */
	alreadyConfigured: boolean;
}

export type LocalImportNoteCode =
	/** A subscription/OAuth login that is not an API key; it is never imported. */
	"subscription-login-skipped";

export interface LocalImportNote {
	source: LocalImportSource;
	code: LocalImportNoteCode;
}

export interface LocalProviderScanResponse {
	candidates: LocalProviderCandidateSummary[];
	notes: LocalImportNote[];
	/** Which tools have a config directory on the server machine. */
	sources: Array<{ source: LocalImportSource; found: boolean }>;
}

export interface LocalProviderResolvedDraft {
	id: string;
	name: string;
	protocol: LocalImportProtocol;
	baseUrl: string;
	apiKey: string;
}

export interface LocalProviderResolveResponse {
	providers: LocalProviderResolvedDraft[];
	/** Requested ids that the fresh scan no longer finds (config changed meanwhile). */
	missing: string[];
}
