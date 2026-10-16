import { ActionIcon, Group, Portal, Tooltip } from "@mantine/core";
import { IconCopy, IconSend } from "@tabler/icons-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { copyTextToClipboard } from "../../lib/clipboard";
import { collectSelectionTextPreview } from "../../lib/dom-text";
import { Z } from "../../lib/z-index";

interface SelectionPopoverProps {
	containerRef: React.RefObject<HTMLElement | null>;
	/** Send selected text (e.g. to a terminal). When omitted, the send button is hidden. */
	onAction?: (text: string) => void;
	label?: string;
	/** External selection text (e.g. from xterm). When provided, shows popover based on this text instead of DOM selection. */
	externalSelection?: string;
	/** Anchor position for external selection (e.g. touch point). When provided, popover appears near this point. */
	externalAnchor?: { x: number; y: number } | null;
}

export const MAX_SELECTION_POPOVER_TEXT_CHARS = 200_000;

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
}: SelectionPopoverProps) {
	const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
	const selectedTextRef = useRef("");
	const popoverRef = useRef<HTMLDivElement>(null);
	const [copied, setCopied] = useState(false);
	const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const epochRef = useRef(0);
	const externalMode = externalSelection !== undefined;
	const { t } = useTranslation("common");

	const clearSelection = useCallback(() => {
		epochRef.current++;
		if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
		copyTimerRef.current = null;
		setCopied(false);
		selectedTextRef.current = "";
		setPosition(null);
	}, []);

	const placeAbove = useCallback((rect: { top: number; left: number; width: number }) => {
		setPosition({
			top: Math.max(4, rect.top - 36),
			left: clampPopoverLeft(rect.left + rect.width / 2 - 16, 80),
		});
	}, []);

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
		if (externalSelection) {
			selectedTextRef.current = clampSelectionPopoverText(externalSelection);
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

	// Cleanup copy timer on unmount
	useEffect(() => {
		return () => {
			epochRef.current++;
			if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
			selectedTextRef.current = "";
		};
	}, []);

	if (!position) return null;

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
			</div>
		</Portal>
	);
}
