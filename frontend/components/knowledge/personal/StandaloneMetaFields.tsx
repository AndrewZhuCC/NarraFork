/**
 * Title / publish target / keywords of a STANDALONE personal entry — used by
 * `/knowledge/mine/new` and the standalone side of `/knowledge/mine/$personalEntryId`. Linked
 * versions have no such fields: their metadata belongs to the shared entry.
 *
 * Target options are every collection the caller can see. Submitting needs only *a* target
 * (`submitForReview` checks presence, not the author's write access — the reviewer's
 * write access to the target is what approval requires, §6.7.5), so limiting the list to
 * `viewerCaps.canCreateEntry` would hide valid targets from authors who publish via review.
 */
import { Select, Stack, TagsInput, TextInput } from "@mantine/core";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useKnowledgeCollections } from "../../../hooks/useKnowledge";

export interface StandaloneMetaValue {
	title: string;
	targetCollectionId: string | null;
	keywords: string[];
}

export function StandaloneMetaFields({
	value,
	onChange,
	disabled,
	currentTargetName,
}: {
	value: StandaloneMetaValue;
	onChange: (next: StandaloneMetaValue) => void;
	disabled?: boolean;
	/** Display name of a saved target the collection list no longer contains. */
	currentTargetName?: string | null;
}) {
	const { t } = useTranslation("knowledge");
	const collections = useKnowledgeCollections();
	const options = useMemo(() => {
		const opts = (collections.data ?? []).map((c) => ({ value: c.id, label: c.name }));
		const target = value.targetCollectionId;
		// Keep a saved but no-longer-listed target selectable (and named only if readable).
		if (target && !opts.some((o) => o.value === target)) {
			opts.unshift({ value: target, label: currentTargetName ?? t("mineTargetUnreadable") });
		}
		return opts;
	}, [collections.data, value.targetCollectionId, currentTargetName, t]);

	return (
		<Stack gap="sm">
			<TextInput
				label={t("title_field")}
				value={value.title}
				onChange={(e) => onChange({ ...value, title: e.currentTarget.value })}
				disabled={disabled}
				required
			/>
			<Select
				label={t("publishTarget")}
				placeholder={t("noTargetCollection")}
				description={t("publishTargetHint")}
				data={options}
				value={value.targetCollectionId}
				onChange={(v) => onChange({ ...value, targetCollectionId: v })}
				clearable
				searchable
				disabled={disabled}
			/>
			<TagsInput
				label={t("keywords")}
				description={t("keywordsHint")}
				placeholder={t("keywordsPlaceholder")}
				value={value.keywords}
				onChange={(v) => onChange({ ...value, keywords: v })}
				clearable
				disabled={disabled}
			/>
		</Stack>
	);
}
