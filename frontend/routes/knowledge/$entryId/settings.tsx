/**
 * Entry settings (§5.8): Basics, Access control, Ownership, Danger zone — each section editable
 * only with its own `viewerCaps` bit and read-only otherwise (Danger zone hidden without
 * `canDelete`). Everything the old sticky sidebar held lives here now.
 */
import { Stack, Title } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { EntryPageFrame } from "../../../components/knowledge/entry/EntryPageFrame";
import {
	AccessCard,
	BasicsCard,
	DangerZoneCard,
	OwnershipCard,
} from "../../../components/knowledge/entry/EntrySettingsCards";

export const Route = createFileRoute("/knowledge/$entryId/settings")({
	component: EntrySettingsPage,
});

function EntrySettingsPage() {
	const { t } = useTranslation("knowledge");
	const { entryId } = Route.useParams();
	return (
		<EntryPageFrame entryId={entryId} section={t("tabSettings")}>
			{(entry, collection) => (
				<Stack gap="md" maw={820}>
					<Title order={3}>{t("entrySettingsTitle", { title: entry.title })}</Title>
					<BasicsCard entry={entry} />
					<AccessCard entry={entry} collection={collection} />
					<OwnershipCard entry={entry} />
					<DangerZoneCard entry={entry} />
				</Stack>
			)}
		</EntryPageFrame>
	);
}
