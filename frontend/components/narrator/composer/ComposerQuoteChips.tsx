import {
	ActionIcon,
	Box,
	CloseButton,
	Group,
	Kbd,
	Popover,
	Text,
	Textarea,
	Tooltip,
	UnstyledButton,
} from "@mantine/core";
import type { FileReference } from "@shared/file-reference";
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
import {
	type ComposerQuote,
	fileReferenceQuote,
	MAX_COMPOSER_QUOTE_CHARS,
	sameComposerQuote,
} from "./composer-quotes";
import { fileReferenceToken } from "./file-reference-input";
import type { QuoteCandidate } from "./use-quote-candidate";

const PREVIEW_CHARS = 600;

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

const pendingChipStyle: CSSProperties = {
	...chipStyle,
	border: "1px dashed var(--mantine-primary-color-filled)",
	color: "var(--mantine-color-dimmed)",
	cursor: "pointer",
};

// Chips must not take focus or collapse the document selection they quote.
const keepSelection = (event: MouseEvent) => event.preventDefault();

function preview(text: string): string {
	return text.length > PREVIEW_CHARS ? `${text.slice(0, PREVIEW_CHARS)}…` : text;
}

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
					{comment && <ChipLabel>{comment}</ChipLabel>}
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
function QuotePreview({ quote }: { quote: ComposerQuote }) {
	return (
		<Text size="xs" style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
			{preview(quote.text)}
		</Text>
	);
}

export interface ComposerQuoteChipsProps {
	quotes: readonly ComposerQuote[];
	/** References without an inline `#file:` token in the text. */
	fileReferences: readonly FileReference[];
	candidate: QuoteCandidate | null;
	editingQuote: ComposerQuote | null;
	onEditQuote: (quote: ComposerQuote | null) => void;
	onChangeComment: (quote: ComposerQuote, comment: string) => void;
	onCommentDone: () => void;
	onAccept: (candidate: QuoteCandidate) => void;
	onDismiss: (candidate: QuoteCandidate) => void;
	onRemoveQuote: (index: number) => void;
	onRemoveFileReference: (id: string) => void;
	/** Scroll to the quoted source and highlight it. */
	onOpenQuote?: (quote: ComposerQuote) => void;
	/** Open the quoted file on its line range. */
	onOpenFile?: (reference: FileReference) => void;
}

export function ComposerQuoteChips({
	quotes,
	fileReferences,
	candidate,
	editingQuote,
	onEditQuote,
	onChangeComment,
	onCommentDone,
	onAccept,
	onDismiss,
	onRemoveQuote,
	onRemoveFileReference,
	onOpenQuote,
	onOpenFile,
}: ComposerQuoteChipsProps) {
	const { t } = useTranslation("narrator");
	const commentRef = useRef<HTMLTextAreaElement>(null);
	const editedFile = fileReferences.find((ref) => editingQuote?.anchor === `f:${ref.id}`);
	const activeQuote = editingQuote
		? (quotes.find((quote) => sameComposerQuote(quote, editingQuote)) ??
			(editedFile ? fileReferenceQuote(editedFile) : null))
		: null;
	useEffect(() => {
		if (!editingQuote) return;
		const frame = requestAnimationFrame(() => {
			const textarea = commentRef.current;
			textarea?.focus({ preventScroll: true });
			textarea?.setSelectionRange(textarea.value.length, textarea.value.length);
		});
		return () => cancelAnimationFrame(frame);
	}, [editingQuote]);
	useEffect(() => {
		if (editingQuote && !activeQuote) onEditQuote(null);
	}, [editingQuote, activeQuote, onEditQuote]);
	if (!quotes.length && !fileReferences.length && !candidate) return null;
	const unavailable = candidate?.kind === "file" && !candidate.reference;
	return (
		<Popover
			opened={!!activeQuote}
			onDismiss={() => onEditQuote(null)}
			position="top-start"
			floatingStrategy="fixed"
			// Keyboard viewport transitions can briefly clip the anchor. Hiding the
			// dropdown would blur its textarea and dismiss the keyboard in turn.
			hideDetached={false}
			middlewares={{ flip: false, shift: { padding: 12, crossAxis: true, limiter: undefined } }}
			width="min(420px, calc(100vw - 24px))"
			withinPortal
			withArrow
			shadow="md"
			returnFocus={false}
		>
			<Popover.Target>
				<Box>
					<Group gap={6} mb={6} wrap="wrap" data-composer-quote-chips>
						{quotes.map((quote, index) =>
							quote.anchor?.startsWith("f:") ? null : (
								<Tooltip
									// biome-ignore lint/suspicious/noArrayIndexKey: quotes are an ordered prefix of the text
									key={index}
									label={<QuotePreview quote={quote} />}
									multiline
									w={360}
									openDelay={300}
									withinPortal
								>
									<QuoteChip
										label={quote.label}
										comment={quote.comment}
										onOpen={quote.anchor && onOpenQuote ? () => onOpenQuote(quote) : undefined}
										onComment={() => onEditQuote(quote)}
										onRemove={() => onRemoveQuote(index)}
									/>
								</Tooltip>
							),
						)}
						{fileReferences.map((reference) => {
							const quote =
								quotes.find((item) => item.anchor === `f:${reference.id}`) ??
								fileReferenceQuote(reference);
							return (
								<QuoteChip
									key={reference.id}
									file
									label={`${reference.deviceId === "local" ? "" : `${reference.deviceId} · `}${fileReferenceToken(reference).replace(/^#file:/, "")}`}
									comment={quote.comment}
									onOpen={onOpenFile ? () => onOpenFile(reference) : undefined}
									onComment={() => onEditQuote(quote)}
									onRemove={() => onRemoveFileReference(reference.id)}
								/>
							);
						})}
						{candidate && (
							<Tooltip
								label={
									unavailable ? (
										t("quote.fileUnsaved")
									) : candidate.kind === "text" ? (
										<QuotePreview quote={candidate.quote} />
									) : (
										t("quote.addHint")
									)
								}
								multiline
								w={candidate.kind === "text" ? 360 : undefined}
								openDelay={300}
								withinPortal
							>
								<span
									style={{ ...pendingChipStyle, ...(unavailable ? { opacity: 0.6 } : null) }}
									data-composer-quote-candidate
								>
									<UnstyledButton
										onMouseDown={keepSelection}
										onClick={() => onAccept(candidate)}
										disabled={unavailable}
										aria-label={t("quote.addHint")}
										style={{
											display: "inline-flex",
											alignItems: "center",
											gap: 4,
											minWidth: 0,
											color: "inherit",
											fontSize: "inherit",
										}}
									>
										{candidate.kind === "file" ? (
											<IconFileText size={12} />
										) : (
											<IconQuote size={12} />
										)}
										<ChipLabel>
											{candidate.kind === "file" ? candidate.label : candidate.quote.label}
										</ChipLabel>
										{!unavailable && (
											<>
												<Text
													component="span"
													size="xs"
													fw={500}
													c="var(--mantine-primary-color-filled)"
													style={{ flexShrink: 0 }}
												>
													{t("quote.add")}
												</Text>
												<Kbd size="xs" style={{ flexShrink: 0 }}>
													Alt Q
												</Kbd>
											</>
										)}
									</UnstyledButton>
									<CloseButton
										size="xs"
										aria-label={t("quote.dismiss")}
										onMouseDown={keepSelection}
										onClick={() => onDismiss(candidate)}
									/>
								</span>
							</Tooltip>
						)}
					</Group>
				</Box>
			</Popover.Target>
			{activeQuote && (
				<Popover.Dropdown p="xs" role="dialog" aria-label={t("quote.editComment")}>
					<Group gap={6} wrap="nowrap" mb={6}>
						<IconQuote size={14} style={{ flexShrink: 0 }} />
						<Text size="xs" truncate style={{ flex: 1, minWidth: 0 }}>
							{activeQuote.label}
						</Text>
						<ActionIcon
							variant="subtle"
							size="sm"
							aria-label={t("quote.commentDone")}
							title={t("quote.commentDone")}
							onClick={onCommentDone}
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
							value={activeQuote.comment ?? ""}
							onChange={(event) => onChangeComment(activeQuote, event.currentTarget.value)}
							onKeyDown={(event) => {
								event.stopPropagation();
								if (event.nativeEvent.isComposing) return;
								if (
									event.key === "Escape" ||
									(event.key === "Enter" && (event.metaKey || event.ctrlKey))
								) {
									event.preventDefault();
									onCommentDone();
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
