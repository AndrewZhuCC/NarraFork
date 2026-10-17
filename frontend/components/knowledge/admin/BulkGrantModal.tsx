import {
	Badge,
	Button,
	Checkbox,
	Group,
	Modal,
	MultiSelect,
	Paper,
	Select,
	Stack,
	Text,
} from "@mantine/core";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useBulkKnowledgeGrant,
	useKnowledgeLevels,
	useKnowledgeTags,
} from "../../../hooks/useKnowledge";

/** Max userIds the bulk endpoint accepts per request (mirrors bulkKnowledgeGrantSchema). */
const BULK_GRANT_MAX_USERS = 200;

/**
 * Bulk grant: one credential → many users in a single request.
 *
 * Deliberately additive (it POSTs grants) rather than replacing a user's ACL like
 * UserPermissionDetail does, so granting a new compartment to ten people can't wipe the
 * unrelated grants they already hold.
 */
export function BulkGrantModal({
	opened,
	onClose,
	users,
}: {
	opened: boolean;
	onClose: () => void;
	users: { id: string; username: string; role: string }[];
}) {
	const { t } = useTranslation("knowledge");
	const levels = useKnowledgeLevels();
	const tags = useKnowledgeTags();
	const bulkGrant = useBulkKnowledgeGrant();

	const [userIds, setUserIds] = useState<string[]>([]);
	const [grantType, setGrantType] = useState<"clearance" | "tag" | "review">("clearance");
	const [clearanceLevel, setClearanceLevel] = useState<string | null>(null);
	const [tagId, setTagId] = useState<string | null>(null);
	const [canWrite, setCanWrite] = useState(false);

	// Admins already bypass every axis, so granting to them is a no-op — leave them out.
	const grantableUsers = useMemo(() => users.filter((u) => u.role !== "admin"), [users]);

	const userOptions = useMemo(
		() => grantableUsers.map((u) => ({ value: u.id, label: u.username })),
		[grantableUsers],
	);
	const usernameById = useMemo(() => new Map(users.map((u) => [u.id, u.username])), [users]);

	const levelOptions = useMemo(
		() => (levels.data ?? []).map((l) => ({ value: l.name, label: l.label || l.name })),
		[levels.data],
	);
	// grantType=tag is the compartment axis (controlled tags only); review authority can be
	// attached to any tag, matching the single-user panel's behaviour.
	const tagOptions = useMemo(() => {
		const rows = tags.data ?? [];
		const usable = grantType === "tag" ? rows.filter((tg) => tg.controlled) : rows;
		return usable.map((tg) => ({ value: tg.id, label: tg.name }));
	}, [tags.data, grantType]);

	const overLimit = userIds.length > BULK_GRANT_MAX_USERS;
	const credentialReady = grantType === "clearance" ? !!clearanceLevel : !!tagId;
	const canSubmit = userIds.length > 0 && credentialReady && !overLimit;

	const submit = () => {
		if (!canSubmit) return;
		bulkGrant.mutate({
			userIds,
			grantType,
			clearanceLevel: grantType === "clearance" ? (clearanceLevel ?? undefined) : undefined,
			tagId: grantType === "clearance" ? undefined : (tagId ?? undefined),
			canWrite: grantType === "clearance" ? canWrite : false,
		});
	};

	const result = bulkGrant.data;

	return (
		<Modal opened={opened} onClose={onClose} title={t("bulkGrantTitle")} size="lg">
			<Stack gap="md">
				<Text size="xs" c="dimmed">
					{t("bulkGrantDesc")}
				</Text>

				<MultiSelect
					label={t("bulkGrantSelectUsers")}
					placeholder={t("bulkGrantSelectUsersPlaceholder")}
					description={t("bulkGrantAdminNote")}
					data={userOptions}
					value={userIds}
					onChange={setUserIds}
					searchable
					clearable
					size="sm"
					maxDropdownHeight={220}
				/>
				<Group gap="xs">
					<Button
						size="compact-xs"
						variant="default"
						onClick={() =>
							setUserIds(grantableUsers.slice(0, BULK_GRANT_MAX_USERS).map((u) => u.id))
						}
					>
						{t("bulkGrantSelectAllUsers")}
					</Button>
					<Button size="compact-xs" variant="subtle" onClick={() => setUserIds([])}>
						{t("bulkGrantClearSelection")}
					</Button>
					{overLimit ? (
						<Text size="xs" c="red">
							{t("bulkGrantLimitHint")}
						</Text>
					) : null}
				</Group>

				<Select
					label={t("bulkGrantCredential")}
					data={[
						{ value: "clearance", label: t("bulkGrantKind_clearance") },
						{ value: "tag", label: t("bulkGrantKind_tag") },
						{ value: "review", label: t("bulkGrantKind_review") },
					]}
					value={grantType}
					onChange={(v) => {
						setGrantType((v as "clearance" | "tag" | "review") ?? "clearance");
						// Clear the other axis so a stale id can't be submitted with the new type.
						setTagId(null);
						setClearanceLevel(null);
					}}
					size="sm"
					w={260}
				/>

				{grantType === "clearance" ? (
					<Group align="flex-end" gap="md">
						<Select
							label={t("bulkGrantPickLevel")}
							placeholder={t("aclLevelPlaceholder")}
							data={levelOptions}
							value={clearanceLevel}
							onChange={setClearanceLevel}
							size="sm"
							w={260}
						/>
						<Checkbox
							size="sm"
							mb={6}
							label={t("bulkGrantCanWrite")}
							checked={canWrite}
							onChange={(e) => setCanWrite(e.currentTarget.checked)}
						/>
					</Group>
				) : (
					<Select
						label={t("bulkGrantPickTag")}
						placeholder={t("aclTagsPlaceholder")}
						data={tagOptions}
						value={tagId}
						onChange={setTagId}
						searchable
						size="sm"
						w={320}
						nothingFoundMessage={t("noTags")}
					/>
				)}

				{bulkGrant.isError ? (
					<Text size="sm" c="red">
						{(bulkGrant.error as Error).message}
					</Text>
				) : null}

				{result ? (
					<Paper withBorder p="sm">
						<Text size="sm" fw={600} mb={4}>
							{t("bulkGrantResultTitle")}
						</Text>
						<Text size="xs" c="dimmed" mb="xs">
							{t("bulkGrantResultSummary", {
								granted: result.granted,
								skipped: result.skipped,
								failed: result.failed,
							})}
						</Text>
						<Stack gap={2} mah={200} style={{ overflowY: "auto" }}>
							{result.results.map((r) => (
								<Group key={r.userId} justify="space-between" wrap="nowrap">
									<Text size="xs" truncate="end">
										{usernameById.get(r.userId) ?? r.userId}
									</Text>
									<Badge
										size="xs"
										variant="light"
										color={
											r.status === "granted" ? "teal" : r.status === "skipped" ? "gray" : "red"
										}
									>
										{t(`bulkGrantStatus_${r.status}`)}
									</Badge>
								</Group>
							))}
						</Stack>
					</Paper>
				) : null}

				<Group justify="flex-end">
					<Button variant="subtle" size="xs" onClick={onClose}>
						{t("close")}
					</Button>
					<Button size="xs" loading={bulkGrant.isPending} disabled={!canSubmit} onClick={submit}>
						{t("bulkGrantSubmit", { count: userIds.length })}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}
