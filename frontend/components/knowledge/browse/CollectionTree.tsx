/**
 * Left column of the browse page (§5.1): "All collections" + one row per readable collection.
 * Management actions sit in each row's "⋯" menu, each shown from that collection's
 * `viewerCaps` (edit / access / transfer owner / delete). No per-collection entry counts: an
 * ACL-respecting count would need a full per-entry scan (see P0).
 */
import {
	ActionIcon,
	Button,
	Group,
	Menu,
	Modal,
	NavLink,
	Paper,
	ScrollArea,
	Stack,
	Text,
	Textarea,
	TextInput,
	Tooltip,
} from "@mantine/core";
import {
	IconDots,
	IconFolder,
	IconFolderOpen,
	IconLock,
	IconPencil,
	IconPlus,
	IconTrash,
	IconUserShare,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../../hooks/useAuth";
import {
	useCreateKnowledgeCollection,
	useDeleteKnowledgeCollection,
	useUpdateKnowledgeCollection,
} from "../../../hooks/useKnowledge";
import { api, type KnowledgeCollection, type KnowledgeCollectionListItem } from "../../../lib/api";
import { CollectionAclPanel } from "../CollectionAclPanel";
import { TransferOwnerModal } from "../TransferOwnerModal";

export function CollectionTree({
	collections,
	activeId,
	onSelect,
}: {
	collections: KnowledgeCollectionListItem[];
	activeId: string | null;
	onSelect: (collectionId: string | null) => void;
}) {
	const { t } = useTranslation("knowledge");
	const [createOpen, setCreateOpen] = useState(false);

	return (
		<Paper withBorder p="sm" radius="md">
			<Stack gap="xs">
				<Group justify="space-between" align="center" mb={4}>
					<Text size="xs" fw={700} c="dimmed" tt="uppercase">
						{t("collections")}
					</Text>
					<Tooltip label={t("createCollection")}>
						<ActionIcon
							size="sm"
							variant="subtle"
							onClick={() => setCreateOpen(true)}
							aria-label={t("createCollection")}
						>
							<IconPlus size={14} />
						</ActionIcon>
					</Tooltip>
				</Group>

				<NavLink
					label={t("allCollections")}
					leftSection={<IconFolderOpen size={16} />}
					active={activeId === null}
					onClick={() => onSelect(null)}
					variant="filled"
					styles={{ label: { fontWeight: 500 } }}
				/>

				<ScrollArea.Autosize mah={480} type="auto">
					<Stack gap={2}>
						{collections.map((c) => (
							<CollectionRow
								key={c.id}
								collection={c}
								active={activeId === c.id}
								onSelect={() => onSelect(c.id)}
							/>
						))}
					</Stack>
				</ScrollArea.Autosize>
			</Stack>
			<CreateCollectionModal opened={createOpen} onClose={() => setCreateOpen(false)} />
		</Paper>
	);
}

function CollectionRow({
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
	const caps = collection.viewerCaps;
	// Gates only the admin-only user directory used to pick a transfer target (a data source).
	const isAdmin = user?.role === "admin";
	const del = useDeleteKnowledgeCollection();
	const [editing, setEditing] = useState(false);
	const [deleting, setDeleting] = useState(false);
	const [aclOpen, setAclOpen] = useState(false);
	const [transferOpen, setTransferOpen] = useState(false);
	const users = useQuery({
		queryKey: ["admin", "users"],
		queryFn: api.listUsers,
		enabled: isAdmin && transferOpen,
	});
	const hasActions = caps.canEdit || caps.canManageAcl || caps.canTransferOwner || caps.canDelete;

	return (
		<>
			<NavLink
				label={collection.name}
				leftSection={active ? <IconFolderOpen size={16} /> : <IconFolder size={16} />}
				active={active}
				onClick={onSelect}
				rightSection={
					hasActions ? (
						<Menu position="bottom-end" withinPortal>
							<Menu.Target>
								<ActionIcon
									size="xs"
									variant="subtle"
									color="gray"
									aria-label={t("collectionActions")}
									onClick={(e) => e.stopPropagation()}
								>
									<IconDots size={14} />
								</ActionIcon>
							</Menu.Target>
							<Menu.Dropdown onClick={(e) => e.stopPropagation()}>
								{caps.canEdit ? (
									<Menu.Item
										leftSection={<IconPencil size={14} />}
										onClick={() => setEditing(true)}
									>
										{t("edit")}
									</Menu.Item>
								) : null}
								{caps.canManageAcl ? (
									<Menu.Item leftSection={<IconLock size={14} />} onClick={() => setAclOpen(true)}>
										{t("collectionAclOpen")}
									</Menu.Item>
								) : null}
								{caps.canTransferOwner ? (
									<Menu.Item
										leftSection={<IconUserShare size={14} />}
										onClick={() => setTransferOpen(true)}
									>
										{t("transferOwnerOpen")}
									</Menu.Item>
								) : null}
								{caps.canDelete ? (
									<Menu.Item
										color="red"
										leftSection={<IconTrash size={14} />}
										onClick={() => setDeleting(true)}
									>
										{t("delete")}
									</Menu.Item>
								) : null}
							</Menu.Dropdown>
						</Menu>
					) : null
				}
			/>

			{caps.canEdit ? (
				<EditCollectionModal
					collection={editing ? collection : null}
					onClose={() => setEditing(false)}
				/>
			) : null}
			{caps.canDelete ? (
				<Modal opened={deleting} onClose={() => setDeleting(false)} title={t("deleteConfirmTitle")}>
					<Stack>
						<Text size="sm">{t("deleteCollectionConfirm", { name: collection.name })}</Text>
						{del.isError ? (
							<Text size="sm" c="red">
								{(del.error as Error).message}
							</Text>
						) : null}
						<Group justify="flex-end">
							<Button variant="subtle" onClick={() => setDeleting(false)}>
								{t("cancel")}
							</Button>
							<Button
								color="red"
								loading={del.isPending}
								onClick={() => del.mutate(collection.id, { onSuccess: () => setDeleting(false) })}
							>
								{t("delete")}
							</Button>
						</Group>
					</Stack>
				</Modal>
			) : null}
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
				{create.isError ? (
					<Text size="sm" c="red">
						{(create.error as Error).message}
					</Text>
				) : null}
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
