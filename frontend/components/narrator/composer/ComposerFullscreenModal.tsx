import { type UsePromptOptimizeResult, usePromptOptimize } from "@frontend/hooks/usePromptOptimize";
import { isMobileViewportWidth } from "@frontend/lib/responsive";
import { Box, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { useHotkeys } from "@mantine/hooks";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	TextareaOptimizeControls,
	textareaOptimizeControlsWidth,
} from "./TextareaOptimizeControls";

export interface ComposerFullscreenModalProps {
	opened: boolean;
	onClose: () => void;
	initialText: string;
	onSubmit: (text: string) => void;
	/** Cached suggestion only; accepting stays local until confirmation. */
	composerSuggestion?: string | null;
	/** Show character/line count */
	showStats?: boolean;
	/** Optional optimize hook for prompt optimization */
	optimizeHook?: UsePromptOptimizeResult;
	/** Narrator ID for optimization API calls */
	narratorId?: string;
	/** Message ID for context (when editing) */
	messageId?: string;
}

export function ComposerFullscreenModal({
	opened,
	onClose,
	initialText,
	onSubmit,
	composerSuggestion,
	showStats = true,
	optimizeHook,
	narratorId,
	messageId,
}: ComposerFullscreenModalProps) {
	const { t } = useTranslation("narrator");
	const { t: tc } = useTranslation("common");
	const [text, setText] = useState(initialText);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const composingRef = useRef(false);
	const visibleSuggestion = text === "" ? composerSuggestion : null;
	const acceptSuggestion = () => {
		if (!visibleSuggestion || text !== "" || composingRef.current) return;
		setText(visibleSuggestion);
		textareaRef.current?.focus();
	};

	// Sync text with initialText when modal opens
	useEffect(() => {
		if (opened) {
			composingRef.current = false;
			setText(initialText);
		}
	}, [opened, initialText]);

	const handleSubmit = () => {
		cancelModalOptimize();
		onSubmit(text);
		onClose();
	};

	// Stats
	const charCount = text.length;
	const lineCount = text.split("\n").length;
	const wordCount = text.trim() ? text.trim().split(/\s+/).length : 0;

	const modalOptimize = usePromptOptimize({
		narratorId: narratorId ?? "",
		messageId,
		withContext: optimizeHook?.withContext,
		textareaRef,
		onOptimized: setText,
	});
	const cancelModalOptimize = modalOptimize.cancelOptimize;
	useEffect(() => {
		if (!opened) cancelModalOptimize();
	}, [opened, cancelModalOptimize]);
	const handleClose = () => {
		cancelModalOptimize();
		onClose();
	};

	useHotkeys([
		["mod+Enter", handleSubmit],
		["Escape", handleClose],
	]);

	return (
		<Modal
			opened={opened}
			onClose={handleClose}
			title={t("fullscreenComposerTitle")}
			size="xl"
			centered
			styles={{
				content: {
					maxHeight: "85vh",
					display: "flex",
					flexDirection: "column",
				},
				body: {
					flex: 1,
					display: "flex",
					flexDirection: "column",
					padding: 0,
				},
			}}
		>
			<Box p="md" style={{ flex: 1, display: "flex", flexDirection: "column" }}>
				<Textarea
					ref={textareaRef}
					value={text}
					onChange={(e) => setText(e.currentTarget.value)}
					placeholder={visibleSuggestion || t("fullscreenComposerPlaceholder")}
					onCompositionStart={() => {
						composingRef.current = true;
					}}
					onCompositionEnd={() => {
						composingRef.current = false;
					}}
					onKeyDown={(e) => {
						if (
							e.key !== "Tab" ||
							!visibleSuggestion ||
							e.defaultPrevented ||
							e.repeat ||
							e.shiftKey ||
							e.ctrlKey ||
							e.metaKey ||
							e.altKey ||
							e.nativeEvent.isComposing ||
							e.nativeEvent.keyCode === 229 ||
							composingRef.current ||
							e.currentTarget.ownerDocument.activeElement !== e.currentTarget ||
							isMobileViewportWidth(e.currentTarget.ownerDocument.defaultView?.innerWidth ?? 0)
						)
							return;
						e.preventDefault();
						acceptSuggestion();
					}}
					autoFocus
					minRows={20}
					maxRows={30}
					autosize
					styles={{
						input: {
							fontFamily: "var(--mantine-font-family-monospace)",
							fontSize: "14px",
							lineHeight: 1.6,
							overflowWrap: "anywhere",
						},
					}}
					style={{ flex: 1 }}
					rightSection={
						optimizeHook ? (
							<TextareaOptimizeControls
								disabled={!text.trim() || modalOptimize.loading}
								loading={modalOptimize.loading}
								withContext={optimizeHook.withContext}
								onToggleContext={optimizeHook.toggleContext}
								onOptimize={modalOptimize.handleOptimize}
								onCancelOptimize={modalOptimize.cancelOptimize}
								contextMessageCount={modalOptimize.contextMessageCount}
								onContextMessageCountChange={modalOptimize.setContextMessageCount}
							/>
						) : undefined
					}
					// No `onExpand` is passed here, so only the optimize button is present and the reserved
					// strip is correspondingly narrower. `top` keeps the buttons beside the FIRST line of a
					// tall textarea rather than centred in it.
					rightSectionWidth={optimizeHook ? textareaOptimizeControlsWidth(false) : undefined}
					rightSectionProps={optimizeHook ? { style: { top: 8 } } : undefined}
				/>
				<Stack gap="sm" mt="md">
					{visibleSuggestion && (
						<Group gap="xs">
							<Button size="compact-xs" variant="subtle" onClick={acceptSuggestion}>
								{t("composerAcceptSuggestion")}
							</Button>
						</Group>
					)}
					{showStats && text.length > 0 && (
						<Group gap="md">
							<Text size="xs" c="dimmed">
								{t("fullscreenStats", { chars: charCount, lines: lineCount, words: wordCount })}
							</Text>
						</Group>
					)}
					<Group justify="space-between">
						<Text size="xs" c="dimmed">
							{t("fullscreenHint")}
						</Text>
						<Group gap="sm">
							<Button variant="subtle" onClick={handleClose}>
								{tc("cancel")}
							</Button>
							<Button onClick={handleSubmit} disabled={!text.trim()}>
								{tc("confirm")}
							</Button>
						</Group>
					</Group>
				</Stack>
			</Box>
		</Modal>
	);
}
