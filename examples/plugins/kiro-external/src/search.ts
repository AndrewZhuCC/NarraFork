/**
 * `provider.search` — web search through Kiro's own MCP `web_search` tool.
 *
 * ## Why this exists as a separate contribution
 *
 * A Kiro subscription includes a hosted `web_search` MCP tool. The built-in adapter already
 * exposes it as the `kiro:local` search channel by calling `kiroMcpCall()` directly. This
 * contribution is the out-of-process equivalent, reached through
 * `contributes.searchProviders` instead of a hardcoded channel kind.
 *
 * Both remain available: the built-in channel was left untouched, matching the decision that
 * governs the rest of this plugin — coexist rather than replace, so the two paths can be
 * compared against the same account.
 *
 * ## Credentials
 *
 * None of its own. The manifest binds this contribution to the `kiro` provider via
 * `providerId`, so the host resolves *that* provider's vault fields and passes them in
 * `config` on every call. The user enters a credential once and both chat and search use it.
 * `resolveManager` is therefore reused verbatim, including its content-keyed manager cache:
 * search and chat share one refreshed access token rather than each forcing a refresh.
 */

import { KiroMcpClient } from "@server/lib/kiro/mcp-client";
import type { McpResponse } from "@server/lib/kiro/types";
import { resolveManager } from "./credentials";

/** One search hit, shaped as `providerSearchResultItemSchema` expects. */
export interface SearchResultItem {
	title?: string;
	url?: string;
	snippet?: string;
	publishedAt?: string;
	source?: string;
}

export interface SearchResult {
	text?: string;
	results?: SearchResultItem[];
}

export interface SearchParams {
	query: string;
	maxResults?: number;
}

/** Reads the search params out of an RPC payload, rejecting anything unusable. */
export function readSearchParams(params: unknown): SearchParams {
	if (!params || typeof params !== "object" || Array.isArray(params)) {
		throw new SearchInputError("provider.search requires an object payload");
	}
	const record = params as Record<string, unknown>;
	const query = typeof record.query === "string" ? record.query.trim() : "";
	if (!query) throw new SearchInputError("provider.search requires a non-empty query");
	const maxResults =
		typeof record.maxResults === "number" && Number.isSafeInteger(record.maxResults)
			? record.maxResults
			: undefined;
	return { query, ...(maxResults != null ? { maxResults } : {}) };
}

export class SearchInputError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "SearchInputError";
	}
}

/**
 * Flatten an MCP tool response into the contract's result shape.
 *
 * Upstream returns prose in `content[].text` rather than structured hits, so `text` is what
 * gets populated and `results` is left absent. The host renders whichever it receives.
 *
 * An upstream error is raised rather than returned as text: a failed channel must fail, so
 * the host's router moves on to the next one instead of treating an error message as an
 * answer.
 *
 * ## Intentional divergence from the built-in search adapter
 *
 * The built-in `textFromMcpResponse` (server/lib/search/router.ts) converts MCP `error` and
 * `isError` payloads into text and returns them as search "results". That means a quota-
 * exhausted message or a transient upstream failure gets fed to the model as if it were a
 * factual answer — clearly wrong. This plugin treats those cases as errors and throws, so
 * the host's multi-channel router falls back to the next available search channel. The
 * built-in behaviour is a known issue, not a reference to match.
 */
export function searchResultFromMcp(response: McpResponse, query: string): SearchResult {
	if (response.error) {
		throw new Error(response.error.message ?? "Kiro MCP search failed");
	}
	const text = (response.result?.content ?? [])
		.filter((content) => content.type === "text" && content.text)
		.map((content) => content.text)
		.join("\n\n")
		.trim();
	if (response.result?.isError) {
		throw new Error(text || "Kiro MCP search reported an error");
	}
	if (!text) {
		// The contract rejects an empty result, and rightly so: an empty success would stop
		// the router's fallback chain at a channel that found nothing.
		throw new Error(`Kiro MCP search returned no content for: ${query}`);
	}
	return { text };
}

/** Run one search. `config` carries the bound provider's resolved credentials. */
export async function runSearch(
	config: unknown,
	params: SearchParams,
	signal?: AbortSignal,
): Promise<SearchResult> {
	const manager = await resolveManager(config);
	const client = new KiroMcpClient(manager);
	const args: Record<string, unknown> = { query: params.query };
	// Upstream ignores unknown arguments, but only send what was asked for.
	if (params.maxResults != null) args.maxResults = params.maxResults;
	const response = await client.callTool("web_search", args, signal);
	return searchResultFromMcp(response, params.query);
}
