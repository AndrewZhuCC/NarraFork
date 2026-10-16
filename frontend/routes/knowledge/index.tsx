/**
 * Knowledge browse page (docs/KNOWLEDGE_UI_REDESIGN.md §5.1). All view state is in the URL
 * (`collection`, `q`, `tag`, `level`, `status`, `sort` — `lib/knowledge-browse-search.ts`):
 * collection tree on the left, a cursor-paginated table of entries on the right. The admin tab
 * (`tab=admin`) stays here until the admin pages get their own routes (P2d).
 *
 * Legacy tab URLs redirect in `beforeLoad` (§4.2): `?tab=review[&submission=<id>]` → inbox /
 * request page, `?tab=mylibrary` → `/knowledge/mine`.
 */
import {
	Badge,
	Button,
	Container,
	Group,
	Menu,
	Stack,
	Tabs,
	Text,
	Title,
	Tooltip,
} from "@mantine/core";
import {
	IconChevronDown,
	IconFilePlus,
	IconGitPullRequest,
	IconNotebook,
	IconPlus,
} from "@tabler/icons-react";
import { createFileRoute, Link, redirect, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AclAdminPanel } from "../../components/knowledge/AclAdminPanel";
import { BrowsePanel } from "../../components/knowledge/browse/BrowsePanel";
import { CreateEntryModal } from "../../components/knowledge/browse/CreateEntryModal";
import { useCurrentUser } from "../../hooks/useAuth";
import { useKnowledgeCollections, useReviewInboxCount } from "../../hooks/useKnowledge";
import { parseKnowledgeBrowseSearch } from "../../lib/knowledge-browse-search";
import { legacyKnowledgeIndexRedirect } from "../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/")({
	// Reads the raw location search: validateSearch drops the legacy values.
	beforeLoad: ({ location }) => {
		const target = legacyKnowledgeIndexRedirect(location.search as Record<string, unknown>);
		if (target) throw redirect({ ...target, replace: true });
	},
	component: KnowledgePage,
	validateSearch: parseKnowledgeBrowseSearch,
});

function KnowledgePage() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate({ from: "/knowledge/" });
	const search = Route.useSearch();
	const { data: user } = useCurrentUser();
	const isAdmin = user?.role === "admin";
	const tab = search.tab === "admin" && isAdmin ? "admin" : "browse";
	const collections = useKnowledgeCollections();
	const [createOpen, setCreateOpen] = useState(false);
	// Bounded server-side count; `capped` means "at least this many" → render "100+".
	const inbox = useReviewInboxCount();
	const inboxCount = inbox.data?.count ?? 0;
	const inboxLabel = inbox.data?.capped ? `${inboxCount}+` : String(inboxCount);

	// Only collections the server lets the caller create entries in (viewerCaps.canCreateEntry).
	const creatable = (collections.data ?? [])
		.filter((c) => c.viewerCaps.canCreateEntry)
		.map((c) => ({ value: c.id, label: c.name }));

	return (
		<Container size="xl" py="lg">
			<Group justify="space-between" align="flex-start" mb="md" wrap="wrap">
				<Stack gap={4}>
					<Title order={2}>{t("title")}</Title>
					<Text size="sm" c="dimmed">
						{t("subtitle")}
					</Text>
				</Stack>
				<Group gap="xs">
					<Menu position="bottom-end" withinPortal>
						<Menu.Target>
							<Button
								leftSection={<IconPlus size={16} />}
								rightSection={<IconChevronDown size={14} />}
							>
								{t("browseNew")}
							</Button>
						</Menu.Target>
						<Menu.Dropdown>
							<Menu.Item
								leftSection={<IconFilePlus size={14} />}
								disabled={creatable.length === 0}
								onClick={() => setCreateOpen(true)}
							>
								{t("createEntry")}
							</Menu.Item>
							<Menu.Item
								leftSection={<IconNotebook size={14} />}
								onClick={() => navigate({ to: "/knowledge/mine/new" })}
							>
								{t("newPersonalEntry")}
							</Menu.Item>
						</Menu.Dropdown>
					</Menu>
					<Button
						component={Link}
						to="/knowledge/mine"
						variant="light"
						leftSection={<IconNotebook size={16} />}
					>
						{t("mineTitle")}
					</Button>
					<Button
						component={Link}
						to="/knowledge/review"
						variant="light"
						leftSection={<IconGitPullRequest size={16} />}
						rightSection={
							inboxCount > 0 ? (
								<Tooltip label={t("reviewInboxTooltip", { count: inboxCount })}>
									<Badge size="sm" circle variant="filled" color="indigo">
										{inboxLabel}
									</Badge>
								</Tooltip>
							) : null
						}
					>
						{t("reviewInboxTitle")}
					</Button>
				</Group>
			</Group>

			{isAdmin ? (
				<Tabs
					value={tab}
					onChange={(value) =>
						navigate({
							search: (prev) => ({ ...prev, tab: value === "admin" ? "admin" : undefined }),
							replace: true,
						})
					}
					keepMounted={false}
				>
					<Tabs.List mb="md">
						<Tabs.Tab value="browse">{t("tabEntries")}</Tabs.Tab>
						<Tabs.Tab value="admin">{t("tabAdmin")}</Tabs.Tab>
					</Tabs.List>
					<Tabs.Panel value="browse">
						<BrowsePanel search={search} />
					</Tabs.Panel>
					<Tabs.Panel value="admin">
						<AclAdminPanel />
					</Tabs.Panel>
				</Tabs>
			) : (
				<BrowsePanel search={search} />
			)}

			<CreateEntryModal
				opened={createOpen}
				onClose={() => setCreateOpen(false)}
				collectionId={
					search.collection && creatable.some((o) => o.value === search.collection)
						? search.collection
						: null
				}
				collectionOptions={creatable}
			/>
		</Container>
	);
}
