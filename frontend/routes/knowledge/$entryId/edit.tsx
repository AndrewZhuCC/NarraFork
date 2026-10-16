/**
 * Direct edit of main (§4.1, §5.3): the shared `KnowledgeEditor` with the current main body as
 * its diff baseline, an optional change note (stored on the new revision), save → back to the
 * read page. Only for `viewerCaps.canWriteMain` (the `addRevision` gate); `beforeLoad` sends
 * everyone else — and an entry that cannot be read — to the read page.
 */
import { Button, Group, Stack, Text, TextInput, Title } from "@mantine/core";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { KnowledgeEditor } from "../../../components/knowledge/editor/KnowledgeEditor";
import { EntryPageFrame } from "../../../components/knowledge/entry/EntryPageFrame";
import { useAddKnowledgeRevision } from "../../../hooks/useKnowledge";
import { api, type KnowledgeEntryDetail } from "../../../lib/api";
import { entryEditRedirect } from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/$entryId/edit")({
	beforeLoad: async ({ params, context }) => {
		let canWriteMain: boolean | null = null;
		try {
			const entry = await context.queryClient.ensureQueryData({
				queryKey: ["knowledge", "entry", params.entryId],
				queryFn: () => api.getKnowledgeEntry(params.entryId),
			});
			canWriteMain = entry.viewerCaps.canWriteMain;
		} catch {
			canWriteMain = null;
		}
		const dest = entryEditRedirect(params.entryId, canWriteMain);
		if (dest) throw redirect({ ...dest, replace: true });
	},
	component: EntryEditPage,
});

function EntryEditPage() {
	const { t } = useTranslation("knowledge");
	const { entryId } = Route.useParams();
	return (
		<EntryPageFrame entryId={entryId} section={t("edit")}>
			{(entry) => <EntryEditor entry={entry} />}
		</EntryPageFrame>
	);
}

function EntryEditor({ entry }: { entry: KnowledgeEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const addRev = useAddKnowledgeRevision();
	const main = entry.currentContent ?? "";
	// Seeded once: a background refetch of main must not overwrite the edit in progress (the
	// diff baseline below does follow main, so a concurrent change shows up there).
	const [draft, setDraft] = useState(main);
	const [changeNote, setChangeNote] = useState("");
	const [done, setDone] = useState(false);

	const back = () => navigate({ to: "/knowledge/$entryId", params: { entryId: entry.id } });
	const save = () => {
		// Ctrl/Cmd+S with nothing changed must not append an identical revision.
		if (draft === main) return;
		addRev.mutate(
			{ entryId: entry.id, content: draft, changeNote: changeNote.trim() || undefined },
			{
				onSuccess: () => {
					// Release the unsaved-changes guard before leaving (main refetches afterwards).
					flushSync(() => setDone(true));
					back();
				},
			},
		);
	};

	return (
		<Stack gap="md">
			<div>
				<Title order={3}>{entry.title}</Title>
				<Text size="xs" c="dimmed">
					{t("directEditDesc")}
				</Text>
			</div>
			<KnowledgeEditor
				value={draft}
				onChange={setDraft}
				savedValue={main}
				baseline={main}
				baselineLabel={t("editVsMain")}
				onSave={save}
				saving={addRev.isPending}
				guardNavigation={!done}
				minRows={16}
				maxRows={40}
				defaultView="split"
			/>
			<TextInput
				label={t("changeNote")}
				placeholder={t("changeNotePlaceholder")}
				value={changeNote}
				onChange={(ev) => setChangeNote(ev.currentTarget.value)}
				maxLength={1000}
			/>
			{addRev.isError ? (
				<Text size="sm" c="red">
					{(addRev.error as Error).message}
				</Text>
			) : null}
			<Group justify="flex-end">
				<Button variant="subtle" onClick={back}>
					{t("cancel")}
				</Button>
				<Button loading={addRev.isPending} disabled={draft === main} onClick={save}>
					{t("save")}
				</Button>
			</Group>
		</Stack>
	);
}
