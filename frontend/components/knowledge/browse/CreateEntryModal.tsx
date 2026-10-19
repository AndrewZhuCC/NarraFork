/**
 * "New entry" from the browse page: target collection (only collections with
 * `viewerCaps.canCreateEntry`), title, tags and the shared `KnowledgeEditor` for the body.
 */
import { Button, Group, Modal, Select, Stack, TagsInput, Text, TextInput } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { useCreateKnowledgeEntry } from "../../../hooks/useKnowledge";
import { KnowledgeEditor } from "../editor/KnowledgeEditor";

export function CreateEntryModal({
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
	const [target, setTarget] = useState<string | null>(null);
	const [title, setTitle] = useState("");
	const [content, setContent] = useState("");
	const [tags, setTags] = useState<string[]>([]);

	const effectiveTarget = target ?? collectionId ?? collectionOptions[0]?.value ?? null;

	const save = () => {
		if (!effectiveTarget || !title.trim()) return;
		create.mutate(
			{ collectionId: effectiveTarget, title: title.trim(), content, tags },
			{
				onSuccess: (entry) => {
					// Clear the form (and close the modal) synchronously so the editor's
					// unsaved-changes guard is released before we navigate to the new entry.
					flushSync(() => {
						setTitle("");
						setContent("");
						setTags([]);
						setTarget(null);
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
					allowDeselect={false}
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
				{create.isError ? (
					<Text size="sm" c="red">
						{(create.error as Error).message}
					</Text>
				) : null}
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
