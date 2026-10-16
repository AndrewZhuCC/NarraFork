import {
	ActionIcon,
	Badge,
	Button,
	Card,
	Container,
	Grid,
	Group,
	Modal,
	NavLink,
	Paper,
	ScrollArea,
	Select,
	Stack,
	Tabs,
	TagsInput,
	Text,
	Textarea,
	TextInput,
	ThemeIcon,
	Title,
	Tooltip,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import {
	IconBook2,
	IconFileSymlink,
	IconFileText,
	IconFolder,
	IconFolderOpen,
	IconGitMerge,
	IconGitPullRequest,
	IconLock,
	IconNotebook,
	IconPencil,
	IconPlus,
	IconSearch,
	IconTrash,
	IconUserShare,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, redirect, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { AclAdminPanel } from "../../components/knowledge/AclAdminPanel";
import { CollectionAclPanel } from "../../components/knowledge/CollectionAclPanel";
import { KnowledgeEditor } from "../../components/knowledge/editor/KnowledgeEditor";
import cardClasses from "../../components/knowledge/KnowledgeCards.module.css";
import { SubmissionStatusBadge } from "../../components/knowledge/SubmissionStatusBadge";
import { TransferOwnerModal } from "../../components/knowledge/TransferOwnerModal";
import { useCurrentUser } from "../../hooks/useAuth";
import {
	useCreateKnowledgeCollection,
	useCreateKnowledgeEntry,
	useCreatePersonalEntry,
	useDeleteKnowledgeCollection,
	useKnowledgeCollections,
	useKnowledgeEntriesPaged,
	useMyOpenKnowledgeSubmissions,
	useMyPersonalEntries,
	useReviewInboxCount,
	useUpdateKnowledgeCollection,
} from "../../hooks/useKnowledge";
import type {
	KnowledgeCollection,
	KnowledgeCollectionListItem,
	KnowledgeEntryPage,
	KnowledgeOpenSubmission,
} from "../../lib/api";
import { api } from "../../lib/api";
import {
	knowledgeRequestDestination,
	legacyKnowledgeIndexRedirect,
} from "../../lib/knowledge-review-nav";

const KNOWLEDGE_TABS = ["browse", "mylibrary", "admin"] as const;
type KnowledgeTab = (typeof KNOWLEDGE_TABS)[number];

export interface KnowledgeIndexSearch {
	tab?: KnowledgeTab;
}

function parseKnowledgeIndexSearch(search: Record<string, unknown>): KnowledgeIndexSearch {
	const tab = KNOWLEDGE_TABS.includes(search.tab as KnowledgeTab)
		? (search.tab as KnowledgeTab)
		: undefined;
	return tab && tab !== "browse" ? { tab } : {};
}

export const Route = createFileRoute("/knowledge/")({
	// Legacy review-center URLs (§4.2): `?tab=review[&submission=<id>]` → inbox / request page.
	// Reads the raw location search: validateSearch drops `tab=review` and `submission`.
	beforeLoad: ({ location }) => {
		const target = legacyKnowledgeIndexRedirect(location.search as Record<string, unknown>);
		if (target) throw redirect({ ...target, replace: true });
	},
	component: KnowledgePage,
	validateSearch: parseKnowledgeIndexSearch,
});

function KnowledgePage() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const { tab: searchTab } = Route.useSearch();
	const { data: user } = useCurrentUser();
	const isAdmin = user?.role === "admin";
	const tab: KnowledgeTab = searchTab === "admin" && !isAdmin ? "browse" : (searchTab ?? "browse");
	// Bounded server-side count; `capped` means "at least this many" → render "100+".
	const inbox = useReviewInboxCount();
	const inboxCount = inbox.data?.count ?? 0;
	const inboxLabel = inbox.data?.capped ? `${inboxCount}+` : String(inboxCount);

	return (
		<Container size="lg" py="lg">
			<Group justify="space-between" align="flex-start" mb="md" wrap="wrap">
				<Stack gap="xs">
					<Title order={2}>{t("title")}</Title>
					<Text size="sm" c="dimmed">
						{t("subtitle")}
					</Text>
				</Stack>
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

			<Tabs
				value={tab}
				onChange={(value) => {
					const next = value === "mylibrary" || (value === "admin" && isAdmin) ? value : undefined;
					navigate({
						to: "/knowledge",
						search: next ? { tab: next } : {},
						replace: true,
					});
				}}
				keepMounted={false}
			>
				<Tabs.List mb="md">
					<Tabs.Tab value="browse">{t("tabEntries")}</Tabs.Tab>
					<Tabs.Tab value="mylibrary">{t("tabMyLibrary")}</Tabs.Tab>
					{isAdmin ? <Tabs.Tab value="admin">{t("tabAdmin")}</Tabs.Tab> : null}
				</Tabs.List>

				<Tabs.Panel value="browse">
					<BrowseTab />
				</Tabs.Panel>
				<Tabs.Panel value="mylibrary">
					<MyLibraryTab />
				</Tabs.Panel>
				{isAdmin ? (
					<Tabs.Panel value="admin">
						<AclAdminPanel />
					</Tabs.Panel>
				) : null}
			</Tabs>
		</Container>
	);
}

interface BrowseRow {
	id: string;
	title: string;
	snippet: string | null;
	tags: string[];
}

/** List rows carry `tagsJson`; FTS search rows carry `tags` + `snippet`. */
function toBrowseRow(e: KnowledgeEntryPage["items"][number]): BrowseRow {
	if ("snippet" in e) return { id: e.id, title: e.title, snippet: e.snippet, tags: e.tags ?? [] };
	return {
		id: e.id,
		title: e.title,
		snippet: null,
		tags: Array.isArray(e.tagsJson) ? e.tagsJson : [],
	};
}

function BrowseTab() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const collections = useKnowledgeCollections();
	const [collectionId, setCollectionId] = useState<string | null>(null);
	const [q, setQ] = useState("");
	// Keyset-paginated: the list used to stop silently at the first 100 rows.
	const entries = useKnowledgeEntriesPaged({
		collectionId: collectionId ?? undefined,
		q: q.trim() || undefined,
	});
	const entryRows = useMemo(
		() => (entries.data?.pages ?? []).flatMap((p) => p.items).map(toBrowseRow),
		[entries.data],
	);

	const [colModal, colModalH] = useDisclosure(false);
	const [entryModal, entryModalH] = useDisclosure(false);

	// Only collections the server lets the caller create entries in (viewerCaps.canCreateEntry);
	// offering the rest would only lead to a refused POST.
	const creatableOptions = useMemo(
		() =>
			(collections.data ?? [])
				.filter((c) => c.viewerCaps.canCreateEntry)
				.map((c) => ({ value: c.id, label: c.name })),
		[collections.data],
	);

	// No per-collection counts: deriving them from the loaded (filtered, paginated) list
	// showed wrong numbers, and an exact ACL-respecting count would need a full per-entry
	// ACL scan on the main thread. The sidebar is navigation only.

	return (
		<Grid gap="md">
			{/* Left Column: Collection Navigation Sidebar */}
			<Grid.Col span={{ base: 12, sm: 4, md: 3 }}>
				<Paper withBorder p="sm" radius="md">
					<Stack gap="xs">
						<Group justify="space-between" align="center" mb={4}>
							<Text size="xs" fw={700} c="dimmed" tt="uppercase">
								{t("collections")}
							</Text>
							<Tooltip label={t("createCollection")}>
								<ActionIcon size="sm" variant="subtle" onClick={colModalH.open}>
									<IconPlus size={14} />
								</ActionIcon>
							</Tooltip>
						</Group>

						<NavLink
							label={t("allCollections")}
							leftSection={<IconFolderOpen size={16} />}
							active={collectionId === null}
							onClick={() => setCollectionId(null)}
							variant="filled"
							styles={{ label: { fontWeight: 500 } }}
						/>

						<ScrollArea.Autosize mah={400} type="auto">
							<Stack gap={2}>
								{collections.data?.map((c) => (
									<CollectionItem
										key={c.id}
										collection={c}
										active={collectionId === c.id}
										onSelect={() => setCollectionId(c.id)}
									/>
								))}
							</Stack>
						</ScrollArea.Autosize>
					</Stack>
				</Paper>
			</Grid.Col>

			{/* Right Column: Entry Search and List */}
			<Grid.Col span={{ base: 12, sm: 8, md: 9 }}>
				<Stack gap="md">
					<Paper withBorder p="sm" radius="md">
						<Group justify="space-between" align="center" gap="xs">
							<TextInput
								placeholder={t("searchPlaceholder")}
								value={q}
								onChange={(e) => setQ(e.currentTarget.value)}
								leftSection={<IconSearch size={16} />}
								style={{ flex: 1 }}
							/>
							<Button
								size="sm"
								leftSection={<IconPlus size={14} />}
								onClick={entryModalH.open}
								disabled={creatableOptions.length === 0}
							>
								{t("createEntry")}
							</Button>
						</Group>
					</Paper>

					{entries.isLoading ? (
						<Paper
							withBorder
							p="xl"
							radius="md"
							style={{ display: "flex", justifyContent: "center" }}
						>
							<Text size="sm" c="dimmed">
								{t("loading")}
							</Text>
						</Paper>
					) : entries.isError ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<Text size="sm" c="red">
								{(entries.error as Error).message}
							</Text>
						</Paper>
					) : entryRows.length === 0 && !entries.hasNextPage ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<ThemeIcon variant="light" size="xl" radius="xl" color="gray" mb="xs">
								<IconFileText size={24} />
							</ThemeIcon>
							<Text size="sm" fw={600} c="dimmed">
								{t("noEntries")}
							</Text>
						</Paper>
					) : (
						<Stack gap="xs">
							{entryRows.length === 0 ? (
								// A non-empty cursor with no rows: the server's scan budget ran out
								// behind entries this user cannot read. More may follow.
								<Text size="xs" c="dimmed" ta="center">
									{t("entriesNoneVisibleYet")}
								</Text>
							) : null}
							{entryRows.map((e) => (
								<Card
									key={e.id}
									withBorder
									padding="md"
									radius="md"
									className={cardClasses.entryCard}
									onClick={() => navigate({ to: "/knowledge/$entryId", params: { entryId: e.id } })}
								>
									<Group justify="space-between" wrap="nowrap" align="flex-start">
										<div style={{ flex: 1, minWidth: 0 }}>
											<Group gap="xs" align="center" mb={4}>
												<IconFileText size={16} style={{ color: "var(--mantine-color-blue-5)" }} />
												<Text size="sm" fw={600} truncate="end">
													{e.title}
												</Text>
											</Group>
											{e.snippet ? (
												<Text size="xs" c="dimmed" lineClamp={2} style={{ wordBreak: "break-all" }}>
													{e.snippet}
												</Text>
											) : null}
										</div>
										<Group gap={4} style={{ flexShrink: 0 }}>
											{(e.tags ?? []).slice(0, 4).map((tag) => (
												<Badge key={tag} size="xs" variant="light">
													{tag}
												</Badge>
											))}
										</Group>
									</Group>
								</Card>
							))}
							{entries.hasNextPage ? (
								<Group justify="center" mt="xs">
									<Button
										variant="default"
										size="xs"
										loading={entries.isFetchingNextPage}
										onClick={() => entries.fetchNextPage()}
									>
										{t("loadMoreEntries")}
									</Button>
								</Group>
							) : null}
						</Stack>
					)}
				</Stack>
			</Grid.Col>

			<CreateCollectionModal opened={colModal} onClose={colModalH.close} />
			<CreateEntryModal
				opened={entryModal}
				onClose={entryModalH.close}
				collectionId={
					collectionId && creatableOptions.some((o) => o.value === collectionId)
						? collectionId
						: null
				}
				collectionOptions={creatableOptions}
			/>
		</Grid>
	);
}

function CollectionItem({
	collection,
	active,
	onSelect,
}: {
	collection: KnowledgeCollectionListItem;
	active: boolean;
	onSelect: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const { data: user } = useCurrentUser();
	// Actions come from the server's per-collection capability bits. `isAdmin` below only
	// gates the admin-only user directory used to pick a transfer target (a data source).
	const caps = collection.viewerCaps;
	const isAdmin = user?.role === "admin";
	const del = useDeleteKnowledgeCollection();
	const [editing, setEditing] = useState<KnowledgeCollection | null>(null);
	const [pendingDelete, setPendingDelete] = useState<KnowledgeCollection | null>(null);
	const [aclOpen, setAclOpen] = useState(false);
	const [transferOpen, setTransferOpen] = useState(false);
	// Owner display + transfer targets need usernames; the endpoint is admin-only, so the
	// query only runs for admins (a non-admin owner transfers by picking from nothing, which
	// the modal handles by showing an empty select — server still enforces the real rule).
	const users = useQuery({
		queryKey: ["admin", "users"],
		queryFn: api.listUsers,
		enabled: isAdmin && (aclOpen || transferOpen),
	});

	return (
		<>
			<NavLink
				label={collection.name}
				leftSection={active ? <IconFolderOpen size={16} /> : <IconFolder size={16} />}
				active={active}
				onClick={onSelect}
				rightSection={
					<Group gap={4} wrap="nowrap" onClick={(e) => e.stopPropagation()}>
						{caps.canManageAcl ? (
							<ActionIcon
								size="xs"
								variant="subtle"
								color="gray"
								onClick={() => setAclOpen(true)}
								title={t("collectionAclOpen")}
							>
								<IconLock size={12} />
							</ActionIcon>
						) : null}
						{caps.canTransferOwner ? (
							<ActionIcon
								size="xs"
								variant="subtle"
								color="gray"
								onClick={() => setTransferOpen(true)}
								title={t("transferOwnerOpen")}
							>
								<IconUserShare size={12} />
							</ActionIcon>
						) : null}
						{caps.canEdit ? (
							<ActionIcon
								size="xs"
								variant="subtle"
								color="gray"
								onClick={() => setEditing(collection)}
								title={t("edit")}
							>
								<IconPencil size={12} />
							</ActionIcon>
						) : null}
						{caps.canDelete ? (
							<ActionIcon
								size="xs"
								variant="subtle"
								color="red"
								onClick={() => setPendingDelete(collection)}
								title={t("delete")}
							>
								<IconTrash size={12} />
							</ActionIcon>
						) : null}
					</Group>
				}
			/>

			<EditCollectionModal collection={editing} onClose={() => setEditing(null)} />
			<DeleteCollectionConfirm
				collection={pendingDelete}
				loading={del.isPending}
				onClose={() => setPendingDelete(null)}
				onConfirm={() => {
					if (pendingDelete) {
						del.mutate(pendingDelete.id, { onSuccess: () => setPendingDelete(null) });
					}
				}}
			/>
			{caps.canManageAcl ? (
				<CollectionAclPanel
					collectionId={aclOpen ? collection.id : null}
					opened={aclOpen}
					onClose={() => setAclOpen(false)}
				/>
			) : null}
			{caps.canTransferOwner ? (
				<TransferOwnerModal
					kind="collection"
					targetId={collection.id}
					targetName={collection.name}
					currentOwnerUserId={collection.ownerUserId}
					users={(users.data ?? []) as { id: string; username: string; role: string }[]}
					opened={transferOpen}
					onClose={() => setTransferOpen(false)}
				/>
			) : null}
		</>
	);
}

function CreateCollectionModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
	const { t } = useTranslation("knowledge");
	const create = useCreateKnowledgeCollection();
	const [name, setName] = useState("");
	const [description, setDescription] = useState("");
	const save = () => {
		if (!name.trim()) return;
		create.mutate(
			{ name: name.trim(), description: description.trim() || undefined },
			{
				onSuccess: () => {
					setName("");
					setDescription("");
					onClose();
				},
			},
		);
	};
	return (
		<Modal opened={opened} onClose={onClose} title={t("createCollection")}>
			<Stack>
				<TextInput
					label={t("name")}
					value={name}
					onChange={(e) => setName(e.currentTarget.value)}
				/>
				<Textarea
					label={t("description")}
					value={description}
					onChange={(e) => setDescription(e.currentTarget.value)}
					autosize
					minRows={2}
				/>
				<Group justify="flex-end">
					<Button variant="subtle" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button onClick={save} disabled={!name.trim()} loading={create.isPending}>
						{t("save")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}

function EditCollectionModal({
	collection,
	onClose,
}: {
	collection: KnowledgeCollection | null;
	onClose: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const update = useUpdateKnowledgeCollection();
	const [name, setName] = useState("");
	const [description, setDescription] = useState("");

	useEffect(() => {
		if (collection) {
			setName(collection.name);
			setDescription(collection.description ?? "");
		}
	}, [collection]);

	const save = () => {
		if (!collection || !name.trim()) return;
		update.mutate(
			{ id: collection.id, name: name.trim(), description: description.trim() || null },
			{ onSuccess: onClose },
		);
	};

	return (
		<Modal opened={!!collection} onClose={onClose} title={t("editCollection")}>
			<Stack>
				<TextInput
					label={t("name")}
					value={name}
					onChange={(e) => setName(e.currentTarget.value)}
				/>
				<Textarea
					label={t("description")}
					value={description}
					onChange={(e) => setDescription(e.currentTarget.value)}
					autosize
					minRows={2}
				/>
				<Group justify="flex-end">
					<Button variant="subtle" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button onClick={save} disabled={!name.trim()} loading={update.isPending}>
						{t("save")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}

function DeleteCollectionConfirm({
	collection,
	loading,
	onClose,
	onConfirm,
}: {
	collection: KnowledgeCollection | null;
	loading: boolean;
	onClose: () => void;
	onConfirm: () => void;
}) {
	const { t } = useTranslation("knowledge");
	return (
		<Modal opened={!!collection} onClose={onClose} title={t("deleteConfirmTitle")}>
			<Stack>
				<Text size="sm">{t("deleteCollectionConfirm", { name: collection?.name ?? "" })}</Text>
				<Group justify="flex-end">
					<Button variant="subtle" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button color="red" onClick={onConfirm} loading={loading}>
						{t("delete")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}

function CreateEntryModal({
	opened,
	onClose,
	collectionId,
	collectionOptions,
}: {
	opened: boolean;
	onClose: () => void;
	collectionId: string | null;
	collectionOptions: { value: string; label: string }[];
}) {
	const { t } = useTranslation("knowledge");
	const create = useCreateKnowledgeEntry();
	const navigate = useNavigate();
	const [target, setTarget] = useState<string | null>(collectionId);
	const [title, setTitle] = useState("");
	const [content, setContent] = useState("");
	const [tags, setTags] = useState<string[]>([]);

	const effectiveTarget = target ?? collectionId ?? collectionOptions[0]?.value ?? null;

	const save = () => {
		if (!effectiveTarget || !title.trim()) return;
		create.mutate(
			{
				collectionId: effectiveTarget,
				title: title.trim(),
				content,
				tags,
			},
			{
				onSuccess: (entry) => {
					// Clear the form (and close the modal) synchronously so the editor's
					// unsaved-changes guard is released before we navigate to the new entry.
					flushSync(() => {
						setTitle("");
						setContent("");
						setTags([]);
						onClose();
					});
					navigate({ to: "/knowledge/$entryId", params: { entryId: entry.id } });
				},
			},
		);
	};

	return (
		<Modal opened={opened} onClose={onClose} title={t("createEntry")} size="lg">
			<Stack>
				<Select
					label={t("collections")}
					data={collectionOptions}
					value={effectiveTarget}
					onChange={setTarget}
				/>
				<TextInput
					label={t("title_field")}
					value={title}
					onChange={(e) => setTitle(e.currentTarget.value)}
				/>
				<TagsInput
					label={t("tags")}
					placeholder={t("tagsPlaceholder")}
					value={tags}
					onChange={setTags}
					clearable
				/>
				<KnowledgeEditor
					label={t("content")}
					value={content}
					onChange={setContent}
					savedValue=""
					onSave={save}
					saving={create.isPending}
					minRows={6}
					maxRows={18}
				/>
				<Group justify="flex-end">
					<Button variant="subtle" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button
						onClick={save}
						disabled={!effectiveTarget || !title.trim()}
						loading={create.isPending}
					>
						{t("save")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}

function MyLibraryTab() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const entries = useMyPersonalEntries({ status: "active" });
	const collections = useKnowledgeCollections();
	const create = useCreatePersonalEntry();
	// One bounded query badges every card with its publish state (pending / conflict /
	// changes requested), so users see progress without opening the detail page.
	const openSubs = useMyOpenKnowledgeSubmissions();
	const [title, setTitle] = useState("");
	const [content, setContent] = useState("");
	const [target, setTarget] = useState<string | null>(null);
	const [keywords, setKeywords] = useState<string[]>([]);

	const collectionOptions = useMemo(
		() => (collections.data ?? []).map((c) => ({ value: c.id, label: c.name })),
		[collections.data],
	);
	const colName = useMemo(() => {
		const m = new Map<string, string>();
		for (const c of collections.data ?? []) m.set(c.id, c.name);
		return m;
	}, [collections.data]);
	// draftId → newest in-flight submission (the list is ordered createdAt DESC).
	const openByDraft = useMemo(() => {
		const m = new Map<string, KnowledgeOpenSubmission>();
		for (const s of openSubs.data ?? []) {
			if (!m.has(s.draftId)) m.set(s.draftId, s);
		}
		return m;
	}, [openSubs.data]);

	const save = () => {
		if (!title.trim()) return;
		create.mutate(
			{
				title: title.trim(),
				content: content || undefined,
				targetCollectionId: target ?? undefined,
				keywords: keywords.length > 0 ? keywords : undefined,
			},
			{
				onSuccess: () => {
					setTitle("");
					setContent("");
					setTarget(null);
					setKeywords([]);
				},
			},
		);
	};

	return (
		<Grid gap="lg">
			{/* Left Column: Create New Personal Entry */}
			<Grid.Col span={{ base: 12, md: 5 }}>
				<Paper withBorder p="md" radius="md" bg="var(--mantine-color-body)">
					<Stack gap="sm">
						<div>
							<Text size="sm" fw={600}>
								{t("newPersonalEntry")}
							</Text>
							<Text size="xs" c="dimmed">
								{t("myLibraryDesc")}
							</Text>
						</div>

						<TextInput
							label={t("title_field")}
							placeholder={t("title_field")}
							value={title}
							onChange={(e) => setTitle(e.currentTarget.value)}
							required
						/>

						<Select
							label={t("publishTarget")}
							placeholder={t("noTargetCollection")}
							clearable
							data={collectionOptions}
							value={target}
							onChange={setTarget}
						/>

						<KnowledgeEditor
							label={t("content")}
							placeholder={t("content")}
							value={content}
							onChange={setContent}
							savedValue=""
							onSave={save}
							saving={create.isPending}
							minRows={4}
							maxRows={12}
						/>

						<TagsInput
							label={t("keywords")}
							description={t("keywordsHint")}
							placeholder={t("keywordsPlaceholder")}
							value={keywords}
							onChange={setKeywords}
							clearable
						/>

						<Button
							onClick={save}
							disabled={!title.trim()}
							loading={create.isPending}
							fullWidth
							mt="xs"
						>
							{t("save")}
						</Button>
					</Stack>
				</Paper>
			</Grid.Col>

			{/* Right Column: Personal Entry List */}
			<Grid.Col span={{ base: 12, md: 7 }}>
				<Stack gap="md">
					<Text size="sm" fw={600} c="dimmed" tt="uppercase">
						{t("tabMyLibrary")}
					</Text>

					{entries.isLoading ? (
						<Paper
							withBorder
							p="xl"
							radius="md"
							style={{ display: "flex", justifyContent: "center" }}
						>
							<Text size="sm" c="dimmed">
								{t("loading")}
							</Text>
						</Paper>
					) : (entries.data?.length ?? 0) === 0 ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<ThemeIcon variant="light" size="xl" radius="xl" color="gray" mb="xs">
								<IconNotebook size={24} />
							</ThemeIcon>
							<Text size="sm" fw={600} c="dimmed">
								{t("noPersonalEntries")}
							</Text>
						</Paper>
					) : (
						<Stack gap="xs">
							{(entries.data ?? []).map((p) => {
								// An in-flight request opens its request page (findings / withdraw /
								// resubmit). Otherwise linked entries open on the shared entry's Draft tab.
								const openSub = openByDraft.get(p.id);
								const openEntry = () => {
									if (openSub) {
										navigate(knowledgeRequestDestination(openSub.id));
										return;
									}
									if (p.entryId) {
										navigate({
											to: "/knowledge/$entryId",
											params: { entryId: p.entryId },
											hash: "draft",
										});
										return;
									}
									navigate({
										to: "/knowledge/personal/$personalEntryId",
										params: { personalEntryId: p.id },
									});
								};
								const openStatus = openSub?.status;
								return (
									<Card
										key={p.id}
										withBorder
										padding="md"
										radius="md"
										style={{ cursor: "pointer" }}
										onClick={openEntry}
									>
										<Group justify="space-between" wrap="nowrap" align="center">
											<div style={{ flex: 1, minWidth: 0 }}>
												<Group gap="xs" align="center" mb={4}>
													<IconBook2 size={16} style={{ color: "var(--mantine-color-grape-5)" }} />
													<Text size="sm" fw={600} truncate="end">
														{p.title ?? p.id.slice(0, 8)}
													</Text>
												</Group>
												<Text size="xs" c="dimmed">
													{p.entryId ? (
														<Group gap={4} wrap="nowrap">
															<IconFileSymlink size={12} />
															<span>{t("personalEntryLinked")}</span>
														</Group>
													) : p.targetCollectionId ? (
														<Group gap={4} wrap="nowrap">
															<IconFolder size={12} />
															<span>
																{t("publishTarget")}:{" "}
																{colName.get(p.targetCollectionId) ?? p.targetCollectionId}
															</span>
														</Group>
													) : (
														<span>{t("noTargetCollection")}</span>
													)}
												</Text>
											</div>
											<Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
												{/* Drift used to be visible only inside the entry's Draft tab, so a user
												    had to open every entry to discover which copies had fallen behind.
												    The list endpoint resolves it for the whole page in one query. */}
												{p.drifted ? (
													<Tooltip label={t("driftBadgeTooltip")} withArrow>
														<Badge
															size="xs"
															variant="light"
															color="orange"
															leftSection={<IconGitMerge size={10} />}
														>
															{t("driftBadge")}
														</Badge>
													</Tooltip>
												) : null}
												{openStatus ? <SubmissionStatusBadge status={openStatus} /> : null}
												<Badge size="xs" variant="light" color={p.entryId ? "blue" : "grape"}>
													{p.entryId ? t("personalEntryLinked") : t("personalEntryStandalone")}
												</Badge>
											</Group>
										</Group>
									</Card>
								);
							})}
						</Stack>
					)}
				</Stack>
			</Grid.Col>
		</Grid>
	);
}
