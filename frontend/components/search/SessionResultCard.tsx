import { Badge, Button, Card, Group, Loader, Stack, Text } from "@mantine/core";
import { IconChevronDown, IconChevronUp } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSessionHits } from "../../hooks/useSearch";
import type { SearchRequest, SessionSearchHit, SessionSearchResult } from "../../lib/api";
import { formatSmartTime } from "../../lib/format";
import { highlightSearchText, searchHighlightTerms } from "../../lib/search-utils";

const linkStyle = { textDecoration: "none", color: "inherit" } as const;
/** Long excerpts are cut before highlighting so a huge message cannot stall rendering. */
const MAX_SNIPPET_CHARS = 2_000;

/**
 * Where a hit's link lands: a tool call's own row for a hit inside one, else the message,
 * with the first query term as the needle that picks the right row of a message rendered
 * as several (text, thinking, tools).
 */
export function hitLinkTarget(hit: SessionSearchHit, query: string) {
	const needle = searchHighlightTerms(query)[0];
	return {
		hash: `msg-${hit.toolUseId ?? hit.id}`,
		search: needle ? { find: needle } : {},
	};
}

function HitRow({
	narratorId,
	hit,
	query,
}: {
	narratorId: string;
	hit: SessionSearchHit;
	query: string;
}) {
	const { t } = useTranslation("search");
	const target = hitLinkTarget(hit, query);
	return (
		<Link
			to="/narrators/$narratorId"
			params={{ narratorId }}
			hash={target.hash}
			search={target.search as never}
			style={linkStyle}
		>
			<Group gap="xs" wrap="nowrap" align="flex-start">
				<Text size="xs" c="dimmed" w={112} style={{ flexShrink: 0, whiteSpace: "nowrap" }}>
					{hit.createdAt ? formatSmartTime(hit.createdAt) : ""}
				</Text>
				{hit.toolUseId ? (
					<Badge size="xs" variant="dot" color="orange" style={{ flexShrink: 0 }}>
						{hit.toolName ? t("toolHitNamed", { tool: hit.toolName }) : t("toolHit")}
					</Badge>
				) : (
					hit.messageRole && (
						<Badge size="xs" variant="dot" style={{ flexShrink: 0 }}>
							{t(`messageRole_${hit.messageRole}`, { defaultValue: hit.messageRole })}
						</Badge>
					)
				)}
				<Text size="sm" c="dimmed" lineClamp={2} style={{ minWidth: 0 }}>
					{highlightSearchText(hit.snippet.slice(0, MAX_SNIPPET_CHARS), query)}
				</Text>
			</Group>
		</Link>
	);
}

/**
 * One session matching the search: its title, where it lives, how many of its messages
 * match, and those messages with their times, newest first. The card shows the latest few;
 * expanding it pages further back through the session's matches.
 */
export function SessionResultCard({
	result,
	request,
}: {
	result: SessionSearchResult;
	request: SearchRequest;
}) {
	const { t } = useTranslation("search");
	const [expanded, setExpanded] = useState(false);
	const more = useSessionHits(result.id, request, expanded);
	const loaded = more.data?.pages.flatMap((page) => page.hits);
	const hits = expanded && loaded ? loaded : result.hits;
	const hidden = Math.max(0, result.hitCount - result.hits.length);
	const subagent = result.narratorType === "subagent";

	return (
		<Card shadow="sm" padding="md" withBorder>
			<Group gap="xs" mb={6} justify="space-between" wrap="nowrap">
				<Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
					<Badge size="xs" color={subagent ? "orange" : "indigo"}>
						{subagent
							? result.subagentType
								? t("subagentWithType", { type: result.subagentType })
								: t("subagent")
							: t("label_session")}
					</Badge>
					<Link
						to="/narrators/$narratorId"
						params={{ narratorId: result.id }}
						style={{ ...linkStyle, minWidth: 0 }}
					>
						<Text fw={500} truncate>
							{highlightSearchText(
								result.title || t("resultUntitled", { id: result.id.slice(0, 8) }),
								request.q,
							)}
						</Text>
					</Link>
					{result.titleMatch && (
						<Badge size="xs" variant="light" color="teal" style={{ flexShrink: 0 }}>
							{t("titleMatch")}
						</Badge>
					)}
					{result.status === "archived" && (
						<Badge size="xs" variant="light" color="gray" style={{ flexShrink: 0 }}>
							{t("archived")}
						</Badge>
					)}
				</Group>
				{result.hitCount > 0 && (
					<Badge size="sm" variant="light" color="grape" style={{ flexShrink: 0 }}>
						{t("hitCount", { count: result.hitCount })}
					</Badge>
				)}
			</Group>

			<Group gap={6} wrap="wrap" mb={hits.length ? 8 : 0}>
				{result.projectName && <Badge variant="outline">{result.projectName}</Badge>}
				{result.lastActivityAt && (
					<Text size="xs" c="dimmed">
						{t("lastActivity", { time: formatSmartTime(result.lastActivityAt) })}
					</Text>
				)}
			</Group>

			{hits.length > 0 && (
				<Stack
					gap={6}
					pl="sm"
					style={{ borderLeft: "2px solid var(--mantine-color-default-border)" }}
				>
					{hits.map((hit) => (
						<HitRow key={hit.id} narratorId={result.id} hit={hit} query={request.q} />
					))}
				</Stack>
			)}

			{(hidden > 0 || expanded) && (
				<Group gap="xs" mt="xs">
					{!expanded ? (
						<Button
							size="compact-xs"
							variant="subtle"
							leftSection={<IconChevronDown size={14} />}
							onClick={() => setExpanded(true)}
						>
							{t("expandHits", { count: hidden })}
						</Button>
					) : (
						<>
							{more.isFetching && <Loader size="xs" />}
							{more.hasNextPage && (
								<Button
									size="compact-xs"
									variant="subtle"
									loading={more.isFetchingNextPage}
									onClick={() => more.fetchNextPage()}
								>
									{t("loadMoreHits")}
								</Button>
							)}
							<Button
								size="compact-xs"
								variant="subtle"
								color="gray"
								leftSection={<IconChevronUp size={14} />}
								onClick={() => setExpanded(false)}
							>
								{t("collapseHits")}
							</Button>
						</>
					)}
				</Group>
			)}
		</Card>
	);
}
