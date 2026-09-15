/**
 * Composer chips for staged selection comments. Each draft is a removable
 * preview (quoted excerpt + comment). Drafts live only in the session store.
 */

import { Box, CloseButton, Group, Text, Tooltip } from "@mantine/core";
import { fileReferenceLabel } from "@shared/file-reference-display";
import { IconAlertCircle, IconMessage } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import type { CommentDraft } from "./comment-draft-store";

export function isSpecCommentStale(
	draft: CommentDraft,
	specRevisionByUri?: ReadonlyMap<string, string | null>,
): boolean {
	if (draft.source !== "spec" || !draft.uri || !draft.revisionId || !specRevisionByUri) {
		return false;
	}
	const current = specRevisionByUri.get(draft.uri);
	return current != null && current !== draft.revisionId;
}

function preview(text: string, max = 80): string {
	const compact = text.replace(/\s+/g, " ").trim();
	return compact.length > max ? `${compact.slice(0, max)}…` : compact;
}

export function CommentDraftChips({
	drafts,
	onRemove,
	specRevisionByUri,
	withTopBorder = false,
}: {
	drafts: readonly CommentDraft[];
	onRemove: (id: string) => void;
	specRevisionByUri?: ReadonlyMap<string, string | null>;
	withTopBorder?: boolean;
}) {
	const { t } = useTranslation("narrator");
	if (drafts.length === 0) return null;

	return (
		<Group
			pt="xs"
			px="md"
			pb={6}
			gap={6}
			wrap="wrap"
			style={{
				flexShrink: 0,
				borderTop: withTopBorder ? "1px solid var(--mantine-color-default-border)" : undefined,
			}}
		>
			{drafts.map((draft) => {
				const stale = isSpecCommentStale(draft, specRevisionByUri);
				const fileTarget = draft.source === "file" ? draft.fileTarget : undefined;
				const fileName = fileTarget?.path
					.split(/^[A-Za-z]:[\\/]/u.test(fileTarget.path) ? /[\\/]/u : "/")
					.pop();
				const sourceLabel =
					draft.source === "file"
						? `${fileName || t("commentFromFile")}${draft.unsaved ? ` · ${t("commentFileUnsaved")}` : ""}`
						: draft.source === "spec"
							? t("commentFromSpec")
							: t("commentFromChat");
				const fileTitle = fileTarget
					? fileReferenceLabel({ ...fileTarget, id: draft.id, label: fileTarget.path })
					: undefined;
				return (
					<Group
						key={draft.id}
						gap={6}
						px="xs"
						py={4}
						wrap="nowrap"
						style={{
							borderRadius: "var(--mantine-radius-sm)",
							backgroundColor:
								"light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-6))",
							maxWidth: 280,
						}}
					>
						<IconMessage size={14} style={{ flexShrink: 0, opacity: 0.7 }} />
						<Box style={{ minWidth: 0, flex: 1 }}>
							<Text size="xs" c="dimmed" truncate title={fileTitle}>
								{sourceLabel} · “{preview(draft.excerpt, 48)}”
							</Text>
							<Text size="xs" truncate>
								{preview(draft.comment)}
							</Text>
						</Box>
						{stale && (
							<Tooltip label={t("commentSourceChanged")} position="top" withArrow>
								<IconAlertCircle
									size={14}
									color="var(--mantine-color-yellow-6)"
									style={{ flexShrink: 0 }}
									aria-label={t("commentSourceChanged")}
								/>
							</Tooltip>
						)}
						<CloseButton
							size={16}
							iconSize={12}
							variant="transparent"
							c="dimmed"
							onClick={() => onRemove(draft.id)}
							title={t("removeComment")}
							aria-label={t("removeComment")}
						/>
					</Group>
				);
			})}
		</Group>
	);
}
