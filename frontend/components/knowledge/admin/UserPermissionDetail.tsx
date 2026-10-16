import {
	ActionIcon,
	Badge,
	Button,
	Group,
	Loader,
	MultiSelect,
	Paper,
	Select,
	Stack,
	Text,
	Title,
} from "@mantine/core";
import { IconX } from "@tabler/icons-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useKnowledgeLevels,
	useKnowledgeTags,
	useKnowledgeTagTypes,
	useSetUserAcl,
	useUserAcl,
} from "../../../hooks/useKnowledge";

/** Replace one user's clearance, controlled tags and review tags (PUT-style, `setUserAcl`). */
export function UserPermissionDetail({
	userId,
	username,
	onClose,
}: {
	userId: string;
	username: string;
	onClose: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const levels = useKnowledgeLevels();
	const tags = useKnowledgeTags();
	const tagTypes = useKnowledgeTagTypes();
	const acl = useUserAcl(userId);
	const setAcl = useSetUserAcl();

	const [clearanceLevel, setClearanceLevel] = useState<string | null>(null);
	const [tagIds, setTagIds] = useState<string[]>([]);
	const [reviewTagIds, setReviewTagIds] = useState<string[]>([]);

	// Sync from server when data arrives
	useEffect(() => {
		if (acl.data) {
			setClearanceLevel(acl.data.clearanceLevel);
			setTagIds(acl.data.tagIds);
			setReviewTagIds(acl.data.reviewTagIds);
		}
	}, [acl.data]);

	// Group controlled tags by type for visual chip groups
	const controlledGroups = useMemo(() => {
		const allTags = tags.data ?? [];
		const types = tagTypes.data ?? [];
		const typeName = new Map(types.map((tt) => [tt.id, tt.name]));
		const uncategorized = t("tagUncategorized");

		const controlled = allTags.filter((tg) => tg.controlled);
		const byGroup = new Map<string, { id: string; name: string }[]>();
		for (const tg of controlled) {
			const group = tg.typeId ? (typeName.get(tg.typeId) ?? uncategorized) : uncategorized;
			if (!byGroup.has(group)) byGroup.set(group, []);
			byGroup.get(group)?.push({ id: tg.id, name: tg.name });
		}
		return [...byGroup.entries()];
	}, [tags.data, tagTypes.data, t]);

	const levelOptions = useMemo(
		() =>
			(levels.data ?? []).map((l) => ({
				value: l.name,
				label: l.label ? `${l.label} (${l.name})` : l.name,
			})),
		[levels.data],
	);

	const allTagOptions = useMemo(
		() => (tags.data ?? []).map((tg) => ({ value: tg.id, label: tg.name })),
		[tags.data],
	);

	const toggleTag = (tagId: string) => {
		setTagIds((ids) => (ids.includes(tagId) ? ids.filter((x) => x !== tagId) : [...ids, tagId]));
	};

	const save = () => {
		setAcl.mutate({ userId, clearanceLevel, tagIds, reviewTagIds });
	};

	if (acl.isLoading || levels.isLoading || tags.isLoading || tagTypes.isLoading)
		return (
			<Group justify="center" py="md">
				<Loader size="sm" />
			</Group>
		);

	// Ensure value is valid (exists in data) to prevent Mantine crash
	const safeLevel = levelOptions.some((o) => o.value === clearanceLevel) ? clearanceLevel : null;

	return (
		<Paper withBorder p="md">
			<Stack gap="md">
				<Group justify="space-between">
					<Title order={5}>{t("userPermissionsFor", { username })}</Title>
					<ActionIcon variant="subtle" onClick={onClose}>
						<IconX size={14} />
					</ActionIcon>
				</Group>

				<Select
					label={t("classificationLevel")}
					description={t("userAclClearanceHint")}
					placeholder={t("aclLevelPlaceholder")}
					data={levelOptions}
					value={safeLevel}
					onChange={setClearanceLevel}
					clearable
					size="sm"
				/>

				{controlledGroups.length === 0 ? (
					<Text size="sm" c="dimmed">
						{t("noControlledTags")}
					</Text>
				) : (
					<div>
						<Text size="sm" fw={500} mb={4}>
							{t("controlledTags")}
						</Text>
						<Text size="xs" c="dimmed" mb="xs">
							{t("userAclTagsHint")}
						</Text>
						<Stack gap="xs">
							{controlledGroups.map(([group, items]) => (
								<div key={group}>
									<Text size="xs" c="dimmed" mb={2}>
										{group}
									</Text>
									<Group gap={6}>
										{items.map((item) => (
											<Badge
												key={item.id}
												size="md"
												variant={tagIds.includes(item.id) ? "filled" : "outline"}
												color={tagIds.includes(item.id) ? "teal" : "gray"}
												style={{ cursor: "pointer" }}
												onClick={() => toggleTag(item.id)}
											>
												{item.name}
											</Badge>
										))}
									</Group>
								</div>
							))}
						</Stack>
					</div>
				)}

				<MultiSelect
					label={t("reviewTags")}
					description={t("userAclReviewHint")}
					placeholder={t("aclTagsPlaceholder")}
					data={allTagOptions}
					value={reviewTagIds.filter((id) => allTagOptions.some((o) => o.value === id))}
					onChange={setReviewTagIds}
					searchable
					clearable
					size="sm"
				/>

				{setAcl.isError ? (
					<Text c="red" size="sm">
						{(setAcl.error as Error).message}
					</Text>
				) : null}

				<Group justify="flex-end">
					<Button onClick={save} loading={setAcl.isPending} size="sm">
						{t("save")}
					</Button>
				</Group>
			</Stack>
		</Paper>
	);
}
