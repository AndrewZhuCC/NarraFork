import { ActionIcon, Box, Button, Group, Portal, Text, Textarea, Tooltip } from "@mantine/core";
import { IconCopy, IconMessage, IconSend } from "@tabler/icons-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { copyTextToClipboard } from "../../lib/clipboard";
import { collectSelectionTextPreview } from "../../lib/dom-text";
import { Z } from "../../lib/z-index";

export type SelectionCommentPayload = {
	excerpt: string;
	comment: string;
	/** Node the selection lived in when the popover opened. */
	anchorNode: Node | null;
};

interface SelectionPopoverProps {
	containerRef: React.RefObject<HTMLElement | null>;
	/** Send selected text (e.g. to a terminal). When omitted, the send button is hidden. */
	onAction?: (text: string) => void;
	label?: string;
	/** External selection text (e.g. from xterm). When provided, shows popover based on this text instead of DOM selection. */
	externalSelection?: string;
	/** Anchor position for external selection (e.g. touch point). When provided, popover appears near this point. */
	externalAnchor?: { x: number; y: number } | null;
	/** Comment on the current selection. The button remains available even without a handler. */
	onComment?: (payload: SelectionCommentPayload) => void;
	/** Real DOM anchor supplied by an editor selection, when available. */
	selectionAnchorNode?: Node | null;
	resetKey?: string;
	hideComment?: boolean;
}

export const MAX_SELECTION_POPOVER_TEXT_CHARS = 200_000;
const COMMENT_PANEL_WIDTH = 280;

function clampSelectionPopoverText(text: string): string {
	return text.length > MAX_SELECTION_POPOVER_TEXT_CHARS
		? text.slice(0, MAX_SELECTION_POPOVER_TEXT_CHARS)
		: text;
}

function clampPopoverLeft(left: number, width: number): number {
	return Math.min(window.innerWidth - width - 4, Math.max(4, left));
}

export function SelectionPopover({
	containerRef,
	onAction,
	label,
	externalSelection,
	externalAnchor,
	onComment,
	selectionAnchorNode,
	resetKey,
	hideComment = false,
}: SelectionPopoverProps) {
	const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
	const [commentOpen, setCommentOpen] = useState(false);
	const [commentText, setCommentText] = useState("");
	const selectedTextRef = useRef("");
	const anchorNodeRef = useRef<Node | null>(null);
	const selectionAnchorNodeRef = useRef<Node | null>(selectionAnchorNode ?? null);
	selectionAnchorNodeRef.current = selectionAnchorNode ?? null;
	const popoverRef = useRef<HTMLDivElement>(null);
	const commentInputRef = useRef<HTMLTextAreaElement>(null);
	const [copied, setCopied] = useState(false);
	const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const scopeRef = useRef(resetKey);
	const epochRef = useRef(0);
	const suppressedExternalRef = useRef<string | undefined>(undefined);
	if (scopeRef.current !== resetKey) {
		scopeRef.current = resetKey;
		epochRef.current++;
		suppressedExternalRef.current = externalSelection;
		selectedTextRef.current = "";
		anchorNodeRef.current = null;
	}
	const externalMode = externalSelection !== undefined;
	const { t } = useTranslation("common");
	const { t: tn } = useTranslation("narrator");

	const clearSelection = useCallback(() => {
		epochRef.current++;
		if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
		copyTimerRef.current = null;
		setCopied(false);
		selectedTextRef.current = "";
		anchorNodeRef.current = null;
		setCommentOpen(false);
		setCommentText("");
		setPosition(null);
	}, []);

	const placeAbove = useCallback((rect: { top: number; left: number; width: number }) => {
		setPosition({
			top: Math.max(4, rect.top - 36),
			left: clampPopoverLeft(rect.left + rect.width / 2 - 16, 80),
		});
	}, []);

	useEffect(() => {
		void resetKey;
		clearSelection();
	}, [resetKey, clearSelection]);

	const popoverVisible = position !== null;
	useEffect(() => {
		const container = containerRef.current;
		if (!popoverVisible || !container) return;
		const check = () => {
			if (
				!container.isConnected ||
				container.closest("[hidden], [inert]") ||
				container.checkVisibility?.({ checkVisibilityCSS: true }) === false
			)
				clearSelection();
		};
		const observer = new MutationObserver(check);
		let ancestor: HTMLElement | null = container;
		for (let depth = 0; ancestor && depth < 64; depth++, ancestor = ancestor.parentElement)
			observer.observe(ancestor, {
				attributes: true,
				attributeFilter: ["style", "class", "hidden", "inert"],
			});
		check();
		return () => observer.disconnect();
	}, [popoverVisible, containerRef, clearSelection]);

	const handleMouseUp = useCallback(() => {
		const epoch = epochRef.current;
		// Small delay to let selection finalize
		setTimeout(() => {
			if (epoch !== epochRef.current) return;
			if (containerRef.current?.checkVisibility?.({ checkVisibilityCSS: true }) === false) {
				clearSelection();
				return;
			}
			const selection = window.getSelection();
			const text = collectSelectionTextPreview(selection, MAX_SELECTION_POPOVER_TEXT_CHARS);
			if (!text || !containerRef.current) {
				clearSelection();
				return;
			}

			const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
			if (!range || !containerRef.current.contains(range.commonAncestorContainer)) {
				clearSelection();
				return;
			}

			const rect = range.getBoundingClientRect();
			selectedTextRef.current = text;
			anchorNodeRef.current =
				selectionAnchorNodeRef.current ?? range.startContainer ?? range.commonAncestorContainer;
			setCommentOpen(false);
			setCommentText("");
			setCopied(false);
			placeAbove(rect);
		}, 10);
	}, [clearSelection, containerRef, placeAbove]);

	const handleMouseDown = useCallback(
		(e: MouseEvent) => {
			// Don't dismiss if clicking the popover itself
			if (popoverRef.current?.contains(e.target as Node)) return;
			clearSelection();
		},
		[clearSelection],
	);

	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;

		const selectionEvents = ["mouseup", "touchend", "keyup"] as const;
		if (!externalMode)
			for (const event of selectionEvents) container.addEventListener(event, handleMouseUp);
		document.addEventListener("mousedown", handleMouseDown);

		return () => {
			for (const event of selectionEvents) container.removeEventListener(event, handleMouseUp);
			document.removeEventListener("mousedown", handleMouseDown);
		};
	}, [containerRef, handleMouseUp, handleMouseDown, externalMode]);

	// Handle external selection (e.g. from xterm canvas)
	useEffect(() => {
		if (externalSelection && externalSelection === suppressedExternalRef.current) return;
		suppressedExternalRef.current = undefined;
		if (externalSelection) {
			selectedTextRef.current = clampSelectionPopoverText(externalSelection);
			anchorNodeRef.current = null;
			setCommentOpen(false);
			setCommentText("");
			if (externalAnchor) {
				setPosition({
					top: Math.max(4, externalAnchor.y - 44),
					left: clampPopoverLeft(externalAnchor.x - 36, 80),
				});
			} else {
				const container = containerRef.current;
				if (container) {
					const rect = container.getBoundingClientRect();
					placeAbove(rect);
				}
			}
			setCopied(false);
		} else if (externalSelection === "") {
			clearSelection();
		}
	}, [externalSelection, externalAnchor, containerRef, clearSelection, placeAbove]);

	const handleClick = useCallback(() => {
		if (selectedTextRef.current && onAction) {
			onAction(selectedTextRef.current);
			clearSelection();
			window.getSelection()?.removeAllRanges();
		}
	}, [clearSelection, onAction]);

	const handleCopy = useCallback(() => {
		const text = selectedTextRef.current;
		if (!text) return;
		const epoch = epochRef.current;
		void copyTextToClipboard(text)
			.then(() => {
				if (epoch !== epochRef.current) return;
				selectedTextRef.current = "";
				setCopied(true);
				copyTimerRef.current = setTimeout(() => {
					clearSelection();
					setCopied(false);
				}, 600);
			})
			.catch(() => {});
	}, [clearSelection]);

	const handleOpenComment = useCallback(() => {
		if (!selectedTextRef.current) return;
		setCommentOpen(true);
		setPosition((prev) =>
			prev ? { ...prev, left: clampPopoverLeft(prev.left, COMMENT_PANEL_WIDTH) } : prev,
		);
	}, []);

	useEffect(() => {
		if (!commentOpen) return;
		commentInputRef.current?.focus();
	}, [commentOpen]);

	const handleConfirmComment = useCallback(() => {
		const excerpt = selectedTextRef.current;
		const comment = commentText.trim();
		if (!excerpt || !comment) return;
		onComment?.({ excerpt, comment, anchorNode: anchorNodeRef.current });
		clearSelection();
		window.getSelection()?.removeAllRanges();
	}, [clearSelection, commentText, onComment]);

	const handleCommentKeyDown = useCallback(
		(e: React.KeyboardEvent<HTMLTextAreaElement>) => {
			e.stopPropagation();
			if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
				e.preventDefault();
				handleConfirmComment();
			}
			if (e.key === "Escape") {
				e.preventDefault();
				setCommentOpen(false);
				setCommentText("");
			}
		},
		[handleConfirmComment],
	);

	// Cleanup copy timer on unmount
	useEffect(() => {
		return () => {
			epochRef.current++;
			if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
			selectedTextRef.current = "";
			anchorNodeRef.current = null;
		};
	}, []);

	if (!position) return null;

	const quotedPreview = selectedTextRef.current.replace(/\s+/g, " ").trim();

	return (
		<Portal>
			<div
				ref={popoverRef}
				style={{
					position: "fixed",
					top: position.top,
					left: position.left,
					zIndex: Z.popover,
				}}
			>
				<Group gap={4}>
					<Tooltip label={t("copy")} position="top" withArrow>
						<ActionIcon
							variant="filled"
							color={copied ? "green" : "gray"}
							size="sm"
							radius="xl"
							aria-label={t("copy")}
							onClick={handleCopy}
						>
							<IconCopy size={14} />
						</ActionIcon>
					</Tooltip>
					{!hideComment && (
						<Tooltip label={tn("addComment")} position="top" withArrow>
							<ActionIcon
								variant="filled"
								color={commentOpen ? "indigo" : "gray"}
								size="sm"
								radius="xl"
								aria-label={tn("addComment")}
								onClick={handleOpenComment}
							>
								<IconMessage size={14} />
							</ActionIcon>
						</Tooltip>
					)}
					{onAction && (
						<Tooltip label={label ?? t("send")} position="top" withArrow>
							<ActionIcon
								variant="filled"
								color="blue"
								size="sm"
								radius="xl"
								aria-label={label ?? t("send")}
								onClick={handleClick}
							>
								<IconSend size={14} />
							</ActionIcon>
						</Tooltip>
					)}
				</Group>
				{commentOpen && (
					<Box
						mt={6}
						p={8}
						style={{
							width: COMMENT_PANEL_WIDTH,
							borderRadius: "var(--mantine-radius-sm)",
							background: "var(--mantine-color-body)",
							border: "1px solid var(--mantine-color-default-border)",
							boxShadow: "var(--mantine-shadow-sm)",
						}}
					>
						<Text size="xs" c="dimmed" lineClamp={2} mb={6} fs="italic">
							{quotedPreview}
						</Text>
						<Textarea
							ref={commentInputRef}
							autosize
							minRows={2}
							maxRows={6}
							size="xs"
							placeholder={tn("commentPlaceholder")}
							value={commentText}
							onChange={(e) => setCommentText(e.currentTarget.value)}
							onInput={(e) => setCommentText(e.currentTarget.value)}
							onKeyDown={handleCommentKeyDown}
						/>
						<Group justify="flex-end" gap={6} mt={6}>
							<Button
								size="compact-xs"
								variant="subtle"
								onClick={() => {
									setCommentOpen(false);
									setCommentText("");
								}}
							>
								{t("cancel")}
							</Button>
							<Button
								size="compact-xs"
								onClick={handleConfirmComment}
								disabled={!commentText.trim()}
							>
								{t("confirm")}
							</Button>
						</Group>
					</Box>
				)}
			</div>
		</Portal>
	);
}
