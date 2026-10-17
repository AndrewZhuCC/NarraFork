import { ActionIcon, Badge, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useCreateKnowledgeTagType,
	useDeleteKnowledgeTagType,
	useKnowledgeTagTypes,
	useUpdateKnowledgeTagType,
} from "../../../hooks/useKnowledge";

/** Create / rename / delete ACL tag types (builtin types can be renamed, not deleted). */
export function TagTypesModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
	const { t } = useTranslation("knowledge");
	const tagTypes = useKnowledgeTagTypes();
	const createTagType = useCreateKnowledgeTagType();
	const deleteTagType = useDeleteKnowledgeTagType();
	const updateTagType = useUpdateKnowledgeTagType();
	const [name, setName] = useState("");
	// Inline rename state: builtin types are renameable (only DELETE is blocked server-side).
	const [editingId, setEditingId] = useState<string | null>(null);
	const [editName, setEditName] = useState("");

	const startRename = (tt: { id: string; name: string }) => {
		setEditingId(tt.id);
		setEditName(tt.name);
	};

	const saveRename = (id: string) => {
		const next = editName.trim();
		if (!next) return;
		updateTagType.mutate({ id, name: next }, { onSuccess: () => setEditingId(null) });
	};

	return (
		<Modal opened={opened} onClose={onClose} title={t("tagTypes")} size="sm">
			<Stack gap="sm">
				<Text size="xs" c="dimmed">
					{t("tagTypesDesc")}
				</Text>
				{tagTypes.data?.map((tt) =>
					editingId === tt.id ? (
						<Group key={tt.id} gap="xs" align="flex-end" wrap="nowrap">
							<TextInput
								size="xs"
								label={t("tagTypeRenameTitle")}
								value={editName}
								onChange={(e) => setEditName(e.currentTarget.value)}
								onKeyDown={(e) => {
									if (e.key === "Enter") saveRename(tt.id);
									if (e.key === "Escape") setEditingId(null);
								}}
								style={{ flex: 1 }}
							/>
							<Button
								size="xs"
								loading={updateTagType.isPending}
								disabled={!editName.trim()}
								onClick={() => saveRename(tt.id)}
							>
								{t("save")}
							</Button>
							<Button size="xs" variant="default" onClick={() => setEditingId(null)}>
								{t("cancel")}
							</Button>
						</Group>
					) : (
						<Group key={tt.id} justify="space-between">
							<Group gap="xs">
								<Text size="sm">{tt.name}</Text>
								{tt.builtin ? (
									<Badge size="xs" color="gray" variant="light">
										{t("tagTypeBuiltin")}
									</Badge>
								) : null}
							</Group>
							<Group gap={4} wrap="nowrap">
								<ActionIcon
									variant="subtle"
									size="xs"
									title={t("tagTypeRename")}
									onClick={() => startRename(tt)}
								>
									<IconPencil size={12} />
								</ActionIcon>
								{tt.builtin ? null : (
									<ActionIcon
										variant="subtle"
										color="red"
										size="xs"
										onClick={() => deleteTagType.mutate(tt.id)}
									>
										<IconTrash size={12} />
									</ActionIcon>
								)}
							</Group>
						</Group>
					),
				)}
				{updateTagType.isError ? (
					<Text size="xs" c="red">
						{(updateTagType.error as Error).message}
					</Text>
				) : null}
				<Group gap="xs" align="flex-end">
					<TextInput
						size="xs"
						label={t("tagTypeName")}
						value={name}
						onChange={(e) => setName(e.currentTarget.value)}
						style={{ flex: 1 }}
					/>
					<Button
						size="xs"
						loading={createTagType.isPending}
						disabled={!name.trim()}
						onClick={() =>
							createTagType.mutate({ name: name.trim() }, { onSuccess: () => setName("") })
						}
					>
						{t("createTagType")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}
