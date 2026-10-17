import {
	Badge,
	Box,
	Button,
	Group,
	Modal,
	Paper,
	SegmentedControl,
	SimpleGrid,
	Stack,
	Text,
	Textarea,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { type KeyboardEvent, type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { DiffView } from "../../narrator/diff/DiffView";
import { MarkdownContent } from "../../narrator/markdown/MarkdownContent";
import { useUnsavedChangesGuard } from "./useUnsavedChangesGuard";

export type KnowledgeEditorView = "edit" | "preview" | "diff" | "split";

export interface KnowledgeEditorProps {
	/** The body being edited (controlled). */
	value: string;
	onChange: (value: string) => void;
	/**
	 * The last persisted body; `value !== savedValue` is the dirty state that arms the
	 * unsaved-changes guard. Defaults to `baseline ?? ""` (a new document is clean while empty).
	 */
	savedValue?: string;
	/** The diff baseline (e.g. current main). `null`/omitted hides the diff view. */
	baseline?: string | null;
	/** Caption above the diff, e.g. "Draft vs main". */
	baselineLabel?: string;
	/** Called on Ctrl/Cmd+S. Validation (title present, etc.) stays with the caller. */
	onSave?: () => void;
	saving?: boolean;
	readOnly?: boolean;
	label?: ReactNode;
	placeholder?: string;
	minRows?: number;
	maxRows?: number;
	defaultView?: KnowledgeEditorView;
	/** Extra controls rendered at the right of the view switch (save/cancel buttons, …). */
	toolbar?: ReactNode;
	/** Arm the route-leave / beforeunload guard while dirty. Default true. */
	guardNavigation?: boolean;
}

/** Ctrl+S / Cmd+S (no Alt). Exported for tests. */
export function isKnowledgeEditorSaveShortcut(e: {
	key: string;
	ctrlKey: boolean;
	metaKey: boolean;
	altKey: boolean;
}): boolean {
	return (e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "s";
}

/** The views offered for the current props/viewport. Exported for tests. */
export function knowledgeEditorViews(opts: {
	hasBaseline: boolean;
	wide: boolean;
}): KnowledgeEditorView[] {
	const views: KnowledgeEditorView[] = ["edit", "preview"];
	if (opts.wide) views.push("split");
	if (opts.hasBaseline) views.push("diff");
	return views;
}

/**
 * The one editor for knowledge body text (docs/KNOWLEDGE_UI_REDESIGN.md §5.3).
 *
 * Markdown edit / rendered preview / diff against a baseline (and edit + preview side by side
 * on wide screens), dirty tracking with a route-leave + beforeunload guard, and Ctrl/Cmd+S to
 * save. It is controlled and owns only the body: title, tags, target collection and the save
 * buttons themselves belong to the caller (pass buttons through `toolbar`).
 */
export function KnowledgeEditor({
	value,
	onChange,
	savedValue,
	baseline,
	baselineLabel,
	onSave,
	saving = false,
	readOnly = false,
	label,
	placeholder,
	minRows = 10,
	maxRows = 28,
	defaultView = "edit",
	toolbar,
	guardNavigation = true,
}: KnowledgeEditorProps) {
	const { t } = useTranslation("knowledge");
	const wide = useMediaQuery("(min-width: 62em)") ?? false;
	const hasBaseline = baseline !== null && baseline !== undefined;
	const views = knowledgeEditorViews({ hasBaseline, wide });
	const [view, setView] = useState<KnowledgeEditorView>(defaultView);
	// Fall back to "edit" when the chosen view disappears (baseline removed, viewport narrowed).
	const activeView = views.includes(view) ? view : "edit";

	const dirty = !readOnly && value !== (savedValue ?? baseline ?? "");
	const blocker = useUnsavedChangesGuard(guardNavigation && dirty);

	const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
		if (!isKnowledgeEditorSaveShortcut(e)) return;
		// Always swallow the browser's "Save page" inside the editor, even when saving is not
		// possible right now — the user meant to save the document, not the HTML.
		e.preventDefault();
		if (readOnly || saving || !onSave) return;
		onSave();
	};

	const textarea = (
		<Textarea
			aria-label={typeof label === "string" ? label : t("content")}
			value={value}
			onChange={(e) => onChange(e.currentTarget.value)}
			placeholder={placeholder}
			readOnly={readOnly}
			autosize
			minRows={minRows}
			maxRows={maxRows}
			styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)", fontSize: 13 } }}
		/>
	);
	const preview = (
		<Paper withBorder p="md" mih={120}>
			{value.trim() ? (
				<MarkdownContent text={value} />
			) : (
				<Text c="dimmed" size="sm">
					{t("noContent")}
				</Text>
			)}
		</Paper>
	);

	return (
		<Box onKeyDown={onKeyDown} data-knowledge-editor="">
			<Stack gap="xs">
				<Group justify="space-between" align="center" gap="xs" wrap="wrap">
					<Group gap="xs" align="center">
						{label ? (
							<Text size="sm" fw={500}>
								{label}
							</Text>
						) : null}
						<SegmentedControl
							size="xs"
							value={activeView}
							onChange={(v) => setView(v as KnowledgeEditorView)}
							data={views.map((v) => ({ value: v, label: t(`editorView_${v}`) }))}
						/>
						{dirty ? (
							<Badge size="xs" variant="light" color="yellow" data-testid="knowledge-editor-dirty">
								{t("editorUnsaved")}
							</Badge>
						) : null}
					</Group>
					{toolbar ? <Group gap="xs">{toolbar}</Group> : null}
				</Group>

				{activeView === "edit" ? (
					textarea
				) : activeView === "preview" ? (
					preview
				) : activeView === "split" ? (
					<SimpleGrid cols={2} spacing="sm">
						{textarea}
						{preview}
					</SimpleGrid>
				) : (
					<div>
						{baselineLabel ? (
							<Text size="xs" c="dimmed" mb={4}>
								{baselineLabel}
							</Text>
						) : null}
						<DiffView
							oldStr={baseline ?? ""}
							newStr={value}
							language="markdown"
							maxHeight={480}
							wordWrap
						/>
					</div>
				)}

				{!readOnly && onSave ? (
					<Text size="xs" c="dimmed">
						{t("editorSaveShortcut")}
					</Text>
				) : null}
			</Stack>

			<Modal
				opened={blocker.status === "blocked"}
				onClose={() => blocker.reset?.()}
				title={t("editorLeaveTitle")}
			>
				<Stack gap="md">
					<Text size="sm">{t("editorLeaveDesc")}</Text>
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => blocker.reset?.()}>
							{t("editorStay")}
						</Button>
						<Button color="red" size="xs" onClick={() => blocker.proceed?.()}>
							{t("editorLeave")}
						</Button>
					</Group>
				</Stack>
			</Modal>
		</Box>
	);
}
