/**
 * Entry read page (docs/KNOWLEDGE_UI_REDESIGN.md §5.2): header + notices + the body, full width,
 * then "Related entries". Reading first — editing (`/edit`), history (`/history`) and every
 * metadata / ACL / ownership control (`/settings`) are their own pages.
 *
 * Legacy hashes redirect in `beforeLoad` (§4.2): `#submissions[/<id>]` → review inbox / request
 * page, `#draft` → the personal version (or the create prompt), `#content` / `#history` /
 * `#links` → this page / the history page / this page at `#related`.
 */
import { Paper, Stack, Text } from "@mantine/core";
import { createFileRoute, redirect, useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { EntryBanners } from "../../../components/knowledge/entry/EntryBanners";
import { EntryPageFrame } from "../../../components/knowledge/entry/EntryPageFrame";
import { EntryReadHeader } from "../../../components/knowledge/entry/EntryReadHeader";
import { EntryRelatedSection } from "../../../components/knowledge/entry/EntryRelatedSection";
import { MarkdownContent } from "../../../components/narrator/markdown/MarkdownContent";
import { api } from "../../../lib/api";
import {
	isLegacyDraftHash,
	KNOWLEDGE_RELATED_ANCHOR,
	legacyDraftHashRedirect,
	legacyEntryHashRedirect,
	legacyEntryTabRedirect,
	parseKnowledgeEntrySearch,
} from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/$entryId/")({
	beforeLoad: async ({ params, location, context }) => {
		// Legacy `#submissions[/<id>]`: requests have their own page and the review inbox.
		const target = legacyEntryHashRedirect(params.entryId, location.hash);
		if (target) throw redirect({ ...target, replace: true });
		// Legacy tab hashes: the tabs became pages.
		const tab = legacyEntryTabRedirect(params.entryId, location.hash);
		if (tab) throw redirect({ ...tab, replace: true });
		// Legacy `#draft`: the personal version has its own page. Which one is only known from
		// the entry's viewerCaps, so this row reads the (cached) entry query first. An
		// unreadable entry falls through to the create prompt, which the not-found page never
		// shows.
		if (isLegacyDraftHash(location.hash)) {
			let myPersonalEntryId: string | null = null;
			try {
				const entry = await context.queryClient.ensureQueryData({
					queryKey: ["knowledge", "entry", params.entryId],
					queryFn: () => api.getKnowledgeEntry(params.entryId),
				});
				myPersonalEntryId = entry.viewerCaps.myPersonalEntryId;
			} catch {
				myPersonalEntryId = null;
			}
			const dest = legacyDraftHashRedirect(params.entryId, location.hash, myPersonalEntryId);
			if (dest) throw redirect({ ...dest, replace: true });
		}
	},
	validateSearch: parseKnowledgeEntrySearch,
	component: EntryReadPage,
});

function EntryReadPage() {
	const { t } = useTranslation("knowledge");
	const { entryId } = Route.useParams();
	const { personal } = Route.useSearch();
	const navigate = useNavigate();

	return (
		<EntryPageFrame entryId={entryId}>
			{(entry, collection) => (
				<Stack gap="lg">
					<EntryReadHeader
						entry={entry}
						collection={collection}
						promptCreate={personal === "create"}
						onPromptClosed={() =>
							navigate({
								to: "/knowledge/$entryId",
								params: { entryId },
								search: {},
								replace: true,
							})
						}
					/>
					<EntryBanners entryId={entryId} caps={entry.viewerCaps} />
					<Paper withBorder p="lg" radius="md">
						{entry.currentContent ? (
							<MarkdownContent text={entry.currentContent} />
						) : (
							<Text c="dimmed" size="sm">
								{t("noContent")}
							</Text>
						)}
					</Paper>
					<EntryRelatedSection
						entryId={entryId}
						collectionId={entry.collectionId}
						canManageLinks={entry.viewerCaps.canManageLinks}
					/>
					<ScrollToHash />
				</Stack>
			)}
		</EntryPageFrame>
	);
}

/**
 * `#related` (the legacy `#links` landing, or a shared link) points into content that only
 * exists once the entry has loaded, after the router's own hash scroll ran. Mounted inside the
 * loaded page, this scrolls once the anchor exists.
 */
function ScrollToHash() {
	const hash = useRouterState({ select: (s) => s.location.hash });
	useEffect(() => {
		if (hash !== KNOWLEDGE_RELATED_ANCHOR) return;
		document.getElementById(KNOWLEDGE_RELATED_ANCHOR)?.scrollIntoView({ block: "start" });
	}, [hash]);
	return null;
}
