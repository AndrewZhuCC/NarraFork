import { Badge, Card, Group, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { EntitySearchResult } from "../../lib/api";
import { formatSmartTime } from "../../lib/format";
import { highlightSearchText } from "../../lib/search-utils";

const linkStyle = { textDecoration: "none", color: "inherit" } as const;
const MAX_SNIPPET_CHARS = 2_000;

function humanize(value: string): string {
	const normalized = value.replace(/[_-]+/g, " ").trim();
	return normalized ? normalized.replace(/\b\w/g, (char) => char.toUpperCase()) : value;
}

/** A knowledge entry or chapter hit; the whole card links to it. */
export function EntityResultCard({ result, query }: { result: EntitySearchResult; query: string }) {
	const { t } = useTranslation("search");
	const time = result.updatedAt ?? result.createdAt;
	const card = (
		<Card shadow="sm" padding="md" withBorder style={{ cursor: "pointer" }}>
			<Group gap="xs" mb={6} wrap="nowrap" style={{ minWidth: 0 }}>
				<Badge size="xs" color={result.type === "knowledge" ? "teal" : "blue"}>
					{t(`label_${result.type}`)}
				</Badge>
				<Text fw={500} truncate>
					{highlightSearchText(
						result.title || t("resultUntitled", { id: result.id.slice(0, 8) }),
						query,
					)}
				</Text>
			</Group>
			<Text size="sm" c="dimmed" lineClamp={3} mb={8}>
				{highlightSearchText(result.snippet.slice(0, MAX_SNIPPET_CHARS), query)}
			</Text>
			<Group gap={6} wrap="wrap">
				{result.projectName && <Badge variant="outline">{result.projectName}</Badge>}
				{result.collectionName && (
					<Badge variant="outline" color="teal">
						{result.collectionName}
					</Badge>
				)}
				{result.status && (
					<Badge variant="dot">
						{t(`status_${result.type}_${result.status}`, { defaultValue: humanize(result.status) })}
					</Badge>
				)}
				{time && (
					<Text size="xs" c="dimmed">
						{formatSmartTime(time)}
					</Text>
				)}
			</Group>
		</Card>
	);
	return result.type === "knowledge" ? (
		<Link to="/knowledge/$entryId" params={{ entryId: result.id }} style={linkStyle}>
			{card}
		</Link>
	) : (
		<Link to="/chapters/$chapterId" params={{ chapterId: result.id }} style={linkStyle}>
			{card}
		</Link>
	);
}
