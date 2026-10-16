/**
 * Knowledge admin › Tags (`/knowledge/admin/tags`): ACL tags grouped by tag type, controlled
 * toggle, create, plus the tag-type manager. Split out of the former AclAdminPanel
 * (docs/KNOWLEDGE_UI_REDESIGN.md §8.5) with behaviour unchanged.
 */
import {
	Accordion,
	ActionIcon,
	Badge,
	Box,
	Button,
	Checkbox,
	Group,
	Loader,
	Paper,
	Select,
	Stack,
	Text,
	TextInput,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconPlus, IconSettings, IconTrash } from "@tabler/icons-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useCreateKnowledgeTag,
	useDeleteKnowledgeTag,
	useKnowledgeTags,
	useKnowledgeTagTypes,
	useUpdateKnowledgeTag,
} from "../../../hooks/useKnowledge";
import { TagTypesModal } from "./TagTypesModal";

export function TagsAdmin() {
	const { t } = useTranslation("knowledge");
	const tags = useKnowledgeTags();
	const tagTypes = useKnowledgeTagTypes();
	const createTag = useCreateKnowledgeTag();
	const deleteTag = useDeleteKnowledgeTag();
	const updateTag = useUpdateKnowledgeTag();
	const [typeModal, typeModalH] = useDisclosure(false);

	const [form, setForm] = useState<{
		name: string;
		controlled: boolean;
		typeId: string | null;
	}>({ name: "", controlled: false, typeId: null });

	// Group tags by type
	const grouped = useMemo(() => {
		const types = tagTypes.data ?? [];
		const allTags = tags.data ?? [];
		const groups = new Map<
			string,
			{ typeId: string | null; typeName: string; tags: typeof allTags }
		>();

		const uncategorized = t("tagUncategorized");
		groups.set("__uncategorized", { typeId: null, typeName: uncategorized, tags: [] });
		for (const tt of types) {
			groups.set(tt.id, { typeId: tt.id, typeName: tt.name, tags: [] });
		}

		for (const tag of allTags) {
			const key = tag.typeId ?? "__uncategorized";
			const group = groups.get(key);
			if (group) group.tags.push(tag);
			else {
				// Type was deleted — put in uncategorized
				groups.get("__uncategorized")?.tags.push(tag);
			}
		}

		return [...groups.values()].filter((g) => g.tags.length > 0 || g.typeId !== null);
	}, [tags.data, tagTypes.data, t]);

	return (
		<Stack gap="md">
			<Group justify="space-between">
				<Text size="xs" c="dimmed">
					{t("tagsDesc")}
				</Text>
				<Button
					size="xs"
					variant="subtle"
					leftSection={<IconSettings size={14} />}
					onClick={typeModalH.open}
				>
					{t("manageTagTypes")}
				</Button>
			</Group>

			{tags.isLoading ? (
				<Loader size="sm" />
			) : grouped.length === 0 ? (
				<Text size="sm" c="dimmed">
					{t("noTags")}
				</Text>
			) : (
				<Accordion variant="separated" multiple defaultValue={grouped.map((g) => g.typeName)}>
					{grouped.map((g) => (
						<Accordion.Item key={g.typeName} value={g.typeName}>
							<Accordion.Control>
								<Group gap="xs">
									<Text size="sm" fw={600}>
										{g.typeName}
									</Text>
									<Badge size="xs" variant="light" color="gray">
										{g.tags.length}
									</Badge>
								</Group>
							</Accordion.Control>
							<Accordion.Panel>
								<Stack gap="xs">
									{g.tags.map((tag) => (
										<Group key={tag.id} justify="space-between" wrap="nowrap">
											<Group gap="xs" wrap="nowrap">
												<Box
													w={3}
													h={20}
													style={{
														borderRadius: 2,
														backgroundColor: tag.controlled
															? "var(--mantine-color-orange-6)"
															: "var(--mantine-color-default-border)",
													}}
												/>
												<Text size="sm">{tag.name}</Text>
												{tag.controlled ? (
													<Badge size="xs" color="orange" variant="light">
														{t("controlled")}
													</Badge>
												) : null}
											</Group>
											<Group gap="xs" wrap="nowrap">
												<ActionIcon
													variant="subtle"
													size="xs"
													onClick={() =>
														updateTag.mutate({
															id: tag.id,
															controlled: !tag.controlled,
														})
													}
													title={tag.controlled ? t("tagMakeNormal") : t("tagMakeControlled")}
												>
													<Badge
														size="xs"
														variant="outline"
														color={tag.controlled ? "gray" : "orange"}
														style={{ cursor: "pointer" }}
													>
														{tag.controlled ? "→ N" : "→ C"}
													</Badge>
												</ActionIcon>
												<ActionIcon
													variant="subtle"
													color="red"
													size="xs"
													onClick={() => deleteTag.mutate(tag.id)}
												>
													<IconTrash size={12} />
												</ActionIcon>
											</Group>
										</Group>
									))}
								</Stack>
							</Accordion.Panel>
						</Accordion.Item>
					))}
				</Accordion>
			)}

			<Paper withBorder p="sm" bg="var(--mantine-color-body)">
				<Group gap="xs" align="flex-end">
					<TextInput
						size="xs"
						label={t("tagName")}
						placeholder={t("tagNamePlaceholder")}
						value={form.name}
						onChange={(e) => {
							const v = e.currentTarget.value;
							setForm((f) => ({ ...f, name: v }));
						}}
						style={{ flex: 1 }}
					/>
					<Select
						size="xs"
						w={140}
						label={t("tagType")}
						placeholder={t("tagUncategorized")}
						clearable
						value={form.typeId}
						onChange={(v) => setForm((f) => ({ ...f, typeId: v }))}
						data={(tagTypes.data ?? []).map((tt) => ({ value: tt.id, label: tt.name }))}
					/>
					<Checkbox
						size="xs"
						label={t("controlled")}
						checked={form.controlled}
						onChange={(e) => {
							const checked = e.currentTarget.checked;
							setForm((f) => ({ ...f, controlled: checked }));
						}}
						mt={22}
					/>
					<Button
						size="xs"
						leftSection={<IconPlus size={14} />}
						loading={createTag.isPending}
						disabled={!form.name.trim()}
						onClick={() =>
							createTag.mutate(
								{
									name: form.name.trim(),
									controlled: form.controlled,
									typeId: form.typeId ?? undefined,
								},
								{ onSuccess: () => setForm({ name: "", controlled: false, typeId: null }) },
							)
						}
					>
						{t("createTag")}
					</Button>
				</Group>
			</Paper>

			<TagTypesModal opened={typeModal} onClose={typeModalH.close} />
		</Stack>
	);
}
