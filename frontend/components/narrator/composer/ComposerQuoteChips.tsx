import { Z } from "@frontend/lib/z-index";
import {
	ActionIcon,
	Box,
	CloseButton,
	Group,
	Popover,
	Text,
	Textarea,
	UnstyledButton,
} from "@mantine/core";
import { IconCheck, IconFileText, IconMessageCircle, IconQuote } from "@tabler/icons-react";
import {
	type ComponentPropsWithoutRef,
	type CSSProperties,
	type MouseEvent,
	type ReactNode,
	type Ref,
	useEffect,
	useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { type ComposerQuote, MAX_COMPOSER_QUOTE_CHARS } from "./composer-quotes";

const chipStyle: CSSProperties = {
	display: "inline-flex",
	alignItems: "center",
	gap: 4,
	height: 24,
	maxWidth: 280,
	paddingInline: 6,
	borderRadius: "var(--mantine-radius-sm)",
	fontSize: "var(--mantine-font-size-xs)",
	userSelect: "none",
};

const solidChipStyle: CSSProperties = {
	...chipStyle,
	border: "1px solid var(--mantine-color-default-border)",
	background: "var(--mantine-color-default-hover)",
};

// Chips must not take focus or collapse the document selection they quote.
const keepSelection = (event: MouseEvent) => event.preventDefault();

function ChipLabel({ children }: { children: ReactNode }) {
	return (
		<Text component="span" size="xs" truncate style={{ minWidth: 0 }}>
			{children}
		</Text>
	);
}

/** Shared appearance for draft quotes and read-only quotes in sent messages. */
export function QuoteChip({
	label,
	comment,
	compactComment = false,
	file = false,
	onOpen,
	onComment,
	onRemove,
	commentLabel,
	style,
	ref,
	...others
}: {
	label: string;
	comment?: string;
	compactComment?: boolean;
	file?: boolean;
	onOpen?: (event: MouseEvent<HTMLButtonElement>) => void;
	onComment?: (event: MouseEvent<HTMLButtonElement>) => void;
	onRemove?: () => void;
	commentLabel?: string;
	style?: CSSProperties;
	ref?: Ref<HTMLSpanElement>;
} & Omit<ComponentPropsWithoutRef<"span">, "children">) {
	const { t } = useTranslation("narrator");
	return (
		<span
			{...others}
			ref={ref}
			style={{
				...solidChipStyle,
				maxWidth: comment ? 380 : 280,
				boxSizing: "border-box",
				...style,
			}}
			data-quote-chip
			data-composer-quote={!file && onRemove ? true : undefined}
		>
			<UnstyledButton
				aria-label={t("quote.open")}
				disabled={!onOpen}
				onClick={onOpen}
				style={{
					display: "inline-flex",
					alignItems: "center",
					gap: 4,
					minWidth: 0,
					color: "inherit",
					fontSize: "inherit",
					cursor: onOpen ? "pointer" : "default",
				}}
			>
				{file ? (
					<IconFileText size={12} style={{ flexShrink: 0 }} />
				) : (
					<IconQuote size={12} style={{ flexShrink: 0 }} />
				)}
				<ChipLabel>{label}</ChipLabel>
			</UnstyledButton>
			{onComment && (
				<UnstyledButton
					aria-label={commentLabel ?? t("quote.editComment")}
					title={comment || commentLabel || t("quote.editComment")}
					data-quote-comment
					onMouseDown={keepSelection}
					onClick={onComment}
					style={{
						display: "inline-flex",
						alignItems: "center",
						gap: 4,
						minWidth: 16,
						maxWidth: 140,
						flexShrink: 0,
						color: comment ? "var(--mantine-primary-color-filled)" : "var(--mantine-color-dimmed)",
					}}
				>
					<IconMessageCircle size={13} style={{ flexShrink: 0 }} />
					{comment && !compactComment && <ChipLabel>{comment}</ChipLabel>}
				</UnstyledButton>
			)}
			{onRemove && (
				<CloseButton
					size="xs"
					aria-label={t(file ? "fileReferences.remove" : "quote.remove")}
					onMouseDown={keepSelection}
					onClick={onRemove}
				/>
			)}
		</span>
	);
}
export interface ComposerQuoteCommentProps {
	quoteId: string | null;
	quote: ComposerQuote | null;
	onChange: (comment: string) => void;
	onClose: () => void;
	onDone: () => void;
	children: ReactNode;
}

/** The comment stays above the unified editor, outside its editable document. */
export function ComposerQuoteComment({
	quoteId,
	quote,
	onChange,
	onClose,
	onDone,
	children,
}: ComposerQuoteCommentProps) {
	const { t } = useTranslation("narrator");
	const commentRef = useRef<HTMLTextAreaElement>(null);
	useEffect(() => {
		if (!quoteId) return;
		const frame = requestAnimationFrame(() => {
			const textarea = commentRef.current;
			textarea?.focus({ preventScroll: true });
			textarea?.setSelectionRange(textarea.value.length, textarea.value.length);
		});
		return () => cancelAnimationFrame(frame);
	}, [quoteId]);
	return (
		<Popover
			opened={!!quote}
			onDismiss={onClose}
			position="top-start"
			floatingStrategy="fixed"
			hideDetached={false}
			middlewares={{ flip: false, shift: { padding: 12, crossAxis: true, limiter: undefined } }}
			width="min(420px, calc(100vw - 24px))"
			zIndex={Z.dropdown}
			withinPortal
			withArrow
			shadow="md"
			returnFocus={false}
		>
			<Popover.Target>
				<Box
					style={{ minWidth: 0 }}
					onPointerDownCapture={(event) => {
						const editable = (event.target as Element).closest("[contenteditable]");
						if (quote && editable?.getAttribute("contenteditable") === "true") onClose();
					}}
				>
					{children}
				</Box>
			</Popover.Target>
			{quote && (
				<Popover.Dropdown p="xs" role="dialog" aria-label={t("quote.editComment")}>
					<Group gap={6} wrap="nowrap" mb={6}>
						<IconQuote size={14} style={{ flexShrink: 0 }} />
						<Text size="xs" truncate style={{ flex: 1, minWidth: 0 }}>
							{quote.label}
						</Text>
						<ActionIcon
							variant="subtle"
							size="sm"
							aria-label={t("quote.commentDone")}
							title={t("quote.commentDone")}
							onClick={onDone}
						>
							<IconCheck size={16} />
						</ActionIcon>
					</Group>
					<Group gap={6} align="flex-start" wrap="nowrap">
						<IconMessageCircle
							size={15}
							style={{ marginTop: 8, flexShrink: 0, color: "var(--mantine-primary-color-filled)" }}
						/>
						<Textarea
							ref={commentRef}
							data-quote-comment-editor
							aria-label={t("quote.editComment")}
							placeholder={t("quote.commentPlaceholder")}
							value={quote.comment ?? ""}
							onChange={(event) => onChange(event.currentTarget.value)}
							onKeyDown={(event) => {
								event.stopPropagation();
								if (event.nativeEvent.isComposing) return;
								if (
									event.key === "Escape" ||
									(event.key === "Enter" && (event.metaKey || event.ctrlKey))
								) {
									event.preventDefault();
									onDone();
								}
							}}
							autosize
							minRows={2}
							maxRows={5}
							maxLength={MAX_COMPOSER_QUOTE_CHARS}
							size="xs"
							style={{ flex: 1 }}
						/>
					</Group>
				</Popover.Dropdown>
			)}
		</Popover>
	);
}
