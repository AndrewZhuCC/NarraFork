import { Hono } from "hono";
import {
	type MessageSearchScope,
	searchService,
	sortSearchResults,
} from "../services/search-service";

export const searchRoutes = new Hono();

/** Entity names `/api/search` accepts. Anything else in the query is ignored. */
const SEARCH_ENTITIES = ["messages", "narrators", "knowledge"] as const;

/**
 * Conversation content `include` adds to the default (primary sessions, what people wrote
 * and read). Unknown names are ignored, like unknown entities.
 */
const SEARCH_INCLUDES = ["subagents", "system", "tools"] as const;

function parseScope(include: string | undefined): MessageSearchScope {
	const names = new Set((include ?? "").split(",").map((name) => name.trim()));
	return {
		subagents: names.has("subagents"),
		system: names.has("system"),
		tools: names.has("tools"),
	} satisfies Record<(typeof SEARCH_INCLUDES)[number], boolean>;
}

searchRoutes.get("/", async (c) => {
	const q = c.req.query("q");
	if (!q?.trim()) return c.json({ results: [] });

	const entitiesParam = c.req.query("entities") ?? SEARCH_ENTITIES.join(",");
	const entities = entitiesParam
		.split(",")
		.filter((e): e is (typeof SEARCH_ENTITIES)[number] =>
			(SEARCH_ENTITIES as readonly string[]).includes(e),
		);

	if (entities.length === 0) {
		return c.json({ results: [] });
	}

	const rawLimit = Number.parseInt(c.req.query("limit") ?? "50", 10);
	const limit = Math.max(1, Math.min(Number.isNaN(rawLimit) ? 50 : rawLimit, 100));
	const sort = c.req.query("sort") === "time" ? "time" : "relevance";
	const scope = parseScope(c.req.query("include"));

	const user = c.get("user");
	const principal = { userId: user.sub, isAdmin: user.role === "admin" };
	const query = q.trim();

	const results = await searchService.search({
		query,
		entities,
		limit,
		sort,
		scope,
		signal: c.req.raw.signal,
		// Narrator and message hits are filtered to what this user may read.
		principal,
	});

	// Knowledge lives on its own path: its visibility gate reads grants, so it
	// cannot ride along in the SQL search. Merged here and re-sorted with
	// the shared comparator so one ordering governs the whole response.
	if (entities.includes("knowledge")) {
		const knowledgeResults = await searchService.searchKnowledge({
			query,
			limit,
			principal,
			sort,
			signal: c.req.raw.signal,
		});
		if (knowledgeResults.length > 0) {
			return c.json({ results: sortSearchResults([...results, ...knowledgeResults], sort) });
		}
	}

	return c.json({ results });
});
