import { Mark } from "@mantine/core";
import type { ReactNode } from "react";
import type { SearchSectionName, SearchSectionPage } from "./api";

/** The search page's tabs: everything, or one section. */
export type SearchTab = "all" | SearchSectionName;

/**
 * Parse the `tab` URL value. Also accepts the old `type` values, so a bookmarked
 * `?type=message` still opens the matching tab.
 */
export function normalizeSearchTab(value: unknown): SearchTab {
	switch (value) {
		case "sessions":
		case "session":
		case "message":
		case "messages":
		case "narrator":
		case "narrators":
			return "sessions";
		case "knowledge":
			return "knowledge";
		case "chapters":
		case "chapter":
			return "chapters";
		default:
			return "all";
	}
}

/** Sort modes the search page offers. */
export type SearchSortMode = "relevance" | "time";

/**
 * Default sort: relevance. Each section ranks within itself, and session relevance already
 * folds in recency, so the newest-first crutch is no longer needed.
 */
export const DEFAULT_SEARCH_SORT: SearchSortMode = "relevance";

export function normalizeSearchSort(value: unknown): SearchSortMode {
	return value === "time" ? "time" : DEFAULT_SEARCH_SORT;
}

/**
 * Content global search skips unless asked: subagent sessions, system-written rows
 * (injected knowledge hints, reminders, automation prompts), tool calls, archived sessions.
 */
export const SEARCH_INCLUDES = ["subagents", "system", "tools", "archived"] as const;
export type SearchInclude = (typeof SEARCH_INCLUDES)[number];

/** Parse the `include` URL value (comma-separated) into known names, in canonical order. */
export function normalizeSearchInclude(value: unknown): SearchInclude[] {
	const raw = Array.isArray(value) ? value.map(String) : String(value ?? "").split(",");
	const names = new Set(raw.map((name) => name.trim()));
	return SEARCH_INCLUDES.filter((name) => names.has(name));
}

/** Time range presets, in days. */
export const SEARCH_TIME_RANGES = { "7d": 7, "30d": 30, "90d": 90, "365d": 365 } as const;
export type SearchTimeRange = keyof typeof SEARCH_TIME_RANGES;

export function normalizeSearchRange(value: unknown): SearchTimeRange | undefined {
	return typeof value === "string" && value in SEARCH_TIME_RANGES
		? (value as SearchTimeRange)
		: undefined;
}

/**
 * Start of a range as an ISO instant, rounded down to the UTC day so it stays stable (and
 * cacheable) for the whole day instead of changing on every render.
 */
export function searchRangeStart(range: SearchTimeRange, now = Date.now()): string {
	const day = 86_400_000;
	return new Date(Math.floor(now / day) * day - SEARCH_TIME_RANGES[range] * day).toISOString();
}

/** A section's match count as shown: exact, or `N+` when the window filled up. */
export function formatSearchTotal(
	page: Pick<SearchSectionPage<unknown>, "total" | "totalIsLowerBound"> | undefined,
): string {
	if (!page) return "0";
	return `${page.total}${page.totalIsLowerBound ? "+" : ""}`;
}

export function normalizeSearchText(value: unknown): string {
	return String(value ?? "")
		.toLowerCase()
		.trim();
}

export function includesSearch(text: unknown, query: string): boolean {
	const normalizedQuery = normalizeSearchText(query);
	if (!normalizedQuery) return true;
	return normalizeSearchText(text).includes(normalizedQuery);
}

export function highlightSearchText(text: string, query: string): ReactNode {
	if (!query) return text;
	const normalizedQuery = query.trim();
	if (!normalizedQuery) return text;
	const escaped = normalizedQuery.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const parts = text.split(new RegExp(`(${escaped})`, "gi"));
	if (parts.length === 1) return text;
	return parts.map((part, i) =>
		part.toLowerCase() === normalizedQuery.toLowerCase() ? (
			// biome-ignore lint/suspicious/noArrayIndexKey: stable split output
			<Mark key={i} color="yellow">
				{part}
			</Mark>
		) : (
			part
		),
	);
}

export function compactSnippet(text: string, query: string, radius = 96): string {
	const clean = text.replace(/\s+/g, " ").trim();
	const normalizedQuery = normalizeSearchText(query);
	if (!normalizedQuery) return clean.slice(0, radius * 2);
	const index = clean.toLowerCase().indexOf(normalizedQuery);
	if (index < 0) return clean.slice(0, radius * 2);
	const start = Math.max(0, index - radius);
	const end = Math.min(clean.length, index + normalizedQuery.length + radius);
	return `${start > 0 ? "…" : ""}${clean.slice(start, end)}${end < clean.length ? "…" : ""}`;
}
