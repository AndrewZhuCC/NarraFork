/**
 * Version history of an entry (§4.1): revision list + any-two diff. Readable by anyone who can
 * read the entry (the revisions endpoint runs the same read gate).
 */
import { Stack, Title } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { EntryHistory } from "../../../components/knowledge/entry/EntryHistory";
import { EntryPageFrame } from "../../../components/knowledge/entry/EntryPageFrame";

export const Route = createFileRoute("/knowledge/$entryId/history")({
	component: EntryHistoryPage,
});

function EntryHistoryPage() {
	const { t } = useTranslation("knowledge");
	const { entryId } = Route.useParams();
	return (
		<EntryPageFrame entryId={entryId} section={t("entryHistory")}>
			{(entry) => (
				<Stack gap="md">
					<Title order={3}>{t("entryHistoryTitle", { title: entry.title })}</Title>
					<EntryHistory entryId={entryId} />
				</Stack>
			)}
		</EntryPageFrame>
	);
}
