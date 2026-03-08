import { z } from "zod/v4";
import { isKiroAvailable, kiroMcpCall, type McpResponse } from "../../kiro-adapter";
import { logger } from "../../logger";
import { getFirstNkpProvider, hasConfiguredNkpProvider } from "../../settings";
import type { ToolDefinition, ToolResult } from "../types";

/** Call MCP search via NKP provider. */
async function nkpMcpSearch(query: string): Promise<McpResponse> {
	const config = getFirstNkpProvider();
	if (!config) throw new Error("No NKP provider configured");

	const baseUrl = config.baseUrl.replace(/\/+$/, "");
	const response = await fetch(`${baseUrl}/v1/mcp/search`, {
		method: "POST",
		headers: {
			"Content-Type": "application/json",
			Authorization: `Bearer ${config.apiKey}`,
		},
		body: JSON.stringify({ query }),
	});

	if (!response.ok) {
		const errText = await response.text().catch(() => "");
		throw new Error(`NKP MCP search error ${response.status}: ${errText}`);
	}

	return (await response.json()) as McpResponse;
}

export const webSearchTool: ToolDefinition = {
	name: "WebSearch",
	description:
		"Search the web for current information. Returns relevant search results with titles, URLs, and snippets. " +
		"Use this when you need up-to-date information that may not be in your training data, " +
		"such as recent documentation, library versions, API references, or current events.",
	parameters: z.object({
		query: z.string().describe("Search query string"),
	}),
	isAvailable: () => isKiroAvailable() || hasConfiguredNkpProvider(),
	async execute(args): Promise<ToolResult> {
		const { query } = args as { query: string };

		const kiroUp = isKiroAvailable();
		const nkpUp = hasConfiguredNkpProvider();

		if (!kiroUp && !nkpUp) {
			return {
				output:
					"Web search is not available: Kiro plugin is not loaded and no NKP provider configured",
				isError: true,
			};
		}

		try {
			// Try Kiro first; on failure, fall back to NKP if available
			let response: McpResponse | undefined;
			if (kiroUp) {
				try {
					response = await kiroMcpCall("web_search", { query });
				} catch (kiroErr) {
					if (nkpUp) {
						logger.warn("Kiro web search failed, falling back to NKP", {
							error: kiroErr instanceof Error ? kiroErr.message : String(kiroErr),
						});
						response = await nkpMcpSearch(query);
					} else {
						throw kiroErr;
					}
				}
			} else {
				response = await nkpMcpSearch(query);
			}

			if (response.error) {
				return {
					output: `Search error: ${response.error.message ?? "Unknown error"}`,
					isError: true,
				};
			}

			if (!response.result?.content?.length) {
				return { output: "No results found" };
			}

			const text = response.result.content
				.filter((c) => c.type === "text" && c.text)
				.map((c) => c.text)
				.join("\n\n");

			return {
				output: text || "No results found",
				isError: response.result.isError,
				title: query.slice(0, 80),
			};
		} catch (err) {
			return {
				output: `Web search failed: ${err instanceof Error ? err.message : String(err)}`,
				isError: true,
			};
		}
	},
};
