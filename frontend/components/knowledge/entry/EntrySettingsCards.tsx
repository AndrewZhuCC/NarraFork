/**
 * The entry settings sections (§5.8), each editable only with its own capability bit and
 * read-only otherwise:
 *  - Basics (title / tags / keywords / status) — `canEditMeta` (PATCH /entries/:id);
 *  - Access control (level, controlled tags, review tags) — `canManageAcl` (PATCH …/acl);
 *  - Ownership (transfer) — `canTransferOwner`;
 *  - Danger zone (delete) — `canDelete`; hidden without it (nothing to show read-only).
 */
import {
	Alert,
	Badge,
	Button,
	Group,
	Modal,
	Paper,
	Stack,
	Text,
	TextInput,
	Title,
} from "@mantine/core";
import { IconAlertTriangle, IconInfoCircle, IconTrash, IconUserShare } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../../hooks/useAuth";
import { useDeleteKnowledgeEntry, useKnowledgeTags } from "../../../hooks/useKnowledge";
import { api, type KnowledgeCollectionListItem, type KnowledgeEntryDetail } from "../../../lib/api";
import { EntryAclPanel } from "../EntryAclPanel";
import { EntryMetaPanel } from "../EntryMetaPanel";
import { TransferOwnerModal } from "../TransferOwnerModal";

function SettingsCard({
	title,
	description,
	readOnly,
	danger,
	children,
}: {
	title: string;
	description?: string;
	readOnly?: boolean;
	danger?: boolean;
	children: ReactNode;
}) {
	const { t } = useTranslation("knowledge");
	return (
		<Paper
			withBorder
			p="md"
			radius="md"
			style={danger ? { borderColor: "var(--mantine-color-red-6)" } : undefined}
		>
			<Stack gap="sm">
				<Group justify="space-between" align="flex-start" gap="xs">
					<div>
						<Title order={5} c={danger ? "red" : undefined}>
							{title}
						</Title>
						{description ? (
							<Text size="xs" c="dimmed">
								{description}
							</Text>
						) : null}
					</div>
					{readOnly ? (
						<Badge size="sm" variant="light" color="gray">
							{t("settingsReadOnly")}
						</Badge>
					) : null}
				</Group>
				{children}
			</Stack>
		</Paper>
	);
}

function Field({ label, children }: { label: string; children: ReactNode }) {
	return (
		<Group gap="xs" wrap="nowrap" align="flex-start">
			<Text size="sm" c="dimmed" w={140} style={{ flexShrink: 0 }}>
				{label}
			</Text>
			<div style={{ minWidth: 0 }}>{children}</div>
		</Group>
	);
}

function Badges({ values, color }: { values: string[]; color?: string }) {
	if (values.length === 0) {
		return (
			<Text size="sm" c="dimmed">
				—
			</Text>
		);
	}
	return (
		<Group gap={4} wrap="wrap">
			{values.map((v) => (
				<Badge key={v} size="sm" variant="light" color={color}>
					{v}
				</Badge>
			))}
		</Group>
	);
}

export function BasicsCard({ entry }: { entry: KnowledgeEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const editable = entry.viewerCaps.canEditMeta;
	return (
		<SettingsCard title={t("settingsBasics")} readOnly={!editable}>
			{editable ? (
				<EntryMetaPanel entry={entry} />
			) : (
				<Stack gap="xs">
					<Field label={t("title_field")}>
						<Text size="sm">{entry.title}</Text>
					</Field>
					<Field label={t("tags")}>
						<Badges values={entry.tagsJson ?? []} />
					</Field>
					<Field label={t("keywords")}>
						<Badges values={entry.keywordsJson ?? []} color="indigo" />
					</Field>
					<Field label={t("status")}>
						<Text size="sm">
							{entry.status === "archived" ? t("statusArchived") : t("statusActive")}
						</Text>
					</Field>
				</Stack>
			)}
		</SettingsCard>
	);
}

export function AccessCard({
	entry,
	collection,
}: {
	entry: KnowledgeEntryDetail;
	collection: KnowledgeCollectionListItem | undefined;
}) {
	const { t } = useTranslation("knowledge");
	const editable = entry.viewerCaps.canManageAcl;
	const tags = useKnowledgeTags();
	const tagName = useMemo(() => {
		const byId = new Map((tags.data ?? []).map((tag) => [tag.id, tag.name]));
		return (id: string) => byId.get(id) ?? id;
	}, [tags.data]);
	const review = entry.reviewTagsJson ?? [];

	return (
		<SettingsCard
			title={t("settingsAccess")}
			description={t("settingsAccessDesc")}
			readOnly={!editable}
		>
			{editable ? (
				// The admin panel carries its own heading; its empty-review-tags warning explains
				// the fail-closed rule.
				<EntryAclPanel entry={entry} />
			) : (
				<Stack gap="xs">
					<Field label={t("classificationLevel")}>
						{entry.classificationLevel ? (
							<Badge size="sm" color="grape" variant="light">
								{entry.classificationLevel}
							</Badge>
						) : (
							<Text size="sm">
								{t("levelInheritedValue", { level: collection?.defaultLevel ?? "—" })}
							</Text>
						)}
					</Field>
					<Field label={t("controlledTags")}>
						<Badges values={(entry.controlledTagsJson ?? []).map(tagName)} color="orange" />
					</Field>
					<Field label={t("reviewTags")}>
						<Badges values={review.map(tagName)} color="blue" />
					</Field>
					{review.length === 0 ? (
						<Alert color="yellow" p="xs" icon={<IconInfoCircle size={14} />}>
							<Text size="xs">{t("reviewTagsEmptyWarning")}</Text>
						</Alert>
					) : null}
				</Stack>
			)}
		</SettingsCard>
	);
}

export function OwnershipCard({ entry }: { entry: KnowledgeEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const { data: user } = useCurrentUser();
	const canTransfer = entry.viewerCaps.canTransferOwner;
	// The user directory endpoint is admin-only; a non-admin owner sees the modal's own
	// fallback. This gates a DATA source, not an action.
	const isAdmin = user?.role === "admin";
	const [open, setOpen] = useState(false);
	const users = useQuery({
		queryKey: ["admin", "users"],
		queryFn: api.listUsers,
		enabled: isAdmin && open,
	});

	return (
		<SettingsCard title={t("settingsOwnership")} readOnly={!canTransfer}>
			<Group justify="space-between" wrap="wrap">
				<Field label={t("owner")}>
					<Text size="sm">{entry.ownerUsername ?? t("entryNoOwner")}</Text>
				</Field>
				{canTransfer ? (
					<Button
						size="xs"
						variant="light"
						leftSection={<IconUserShare size={14} />}
						onClick={() => setOpen(true)}
					>
						{t("transferOwnerOpen")}
					</Button>
				) : null}
			</Group>
			{canTransfer ? (
				<TransferOwnerModal
					kind="entry"
					targetId={entry.id}
					targetName={entry.title}
					currentOwnerUserId={entry.ownerUserId}
					currentOwnerName={entry.ownerUsername}
					users={(users.data ?? []) as { id: string; username: string; role: string }[]}
					opened={open}
					onClose={() => setOpen(false)}
				/>
			) : null}
		</SettingsCard>
	);
}

export function DangerZoneCard({ entry }: { entry: KnowledgeEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const del = useDeleteKnowledgeEntry();
	const [open, setOpen] = useState(false);
	const [confirmText, setConfirmText] = useState("");
	if (!entry.viewerCaps.canDelete) return null;
	// Typing the title is required so a mis-click can't destroy an entry and its whole revision
	// history (the DELETE cascades to revisions and links).
	const confirmed = confirmText.trim() === entry.title.trim();

	return (
		<SettingsCard title={t("settingsDanger")} danger>
			<Group justify="space-between" wrap="wrap">
				<Text size="sm">{t("deleteEntryWarning")}</Text>
				<Button
					size="xs"
					color="red"
					variant="light"
					leftSection={<IconTrash size={14} />}
					onClick={() => {
						setConfirmText("");
						setOpen(true);
					}}
				>
					{t("deleteEntry")}
				</Button>
			</Group>
			<Modal opened={open} onClose={() => setOpen(false)} title={t("deleteEntryTitle")}>
				<Stack gap="md">
					<Alert color="red" icon={<IconAlertTriangle size={16} />} p="xs">
						<Text size="xs">{t("deleteEntryWarning")}</Text>
					</Alert>
					<TextInput
						label={t("deleteEntryConfirmLabel")}
						placeholder={entry.title}
						value={confirmText}
						onChange={(ev) => setConfirmText(ev.currentTarget.value)}
						size="sm"
					/>
					{del.isError ? (
						<Text size="sm" c="red">
							{(del.error as Error).message}
						</Text>
					) : null}
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => setOpen(false)}>
							{t("cancel")}
						</Button>
						<Button
							size="xs"
							color="red"
							loading={del.isPending}
							disabled={!confirmed}
							onClick={() =>
								del.mutate(entry.id, {
									onSuccess: () => {
										setOpen(false);
										navigate({ to: "/knowledge", search: { collection: entry.collectionId } });
									},
								})
							}
						>
							{t("delete")}
						</Button>
					</Group>
				</Stack>
			</Modal>
		</SettingsCard>
	);
}
