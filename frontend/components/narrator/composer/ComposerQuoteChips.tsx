import { CloseButton, Group, Kbd, Text, Tooltip, UnstyledButton } from "@mantine/core";
import type { FileReference } from "@shared/file-reference";
import { IconFileText, IconQuote } from "@tabler/icons-react";
import type { CSSProperties, MouseEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ComposerQuote } from "./composer-quotes";
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
	onAccept: (candidate: QuoteCandidate) => void;
	onDismiss: (candidate: QuoteCandidate) => void;
	onRemoveQuote: (index: number) => void;
	onRemoveFileReference: (id: string) => void;
}

export function ComposerQuoteChips({
	quotes,
	fileReferences,
	candidate,
	onAccept,
	onDismiss,
	onRemoveQuote,
	onRemoveFileReference,
}: ComposerQuoteChipsProps) {
	const { t } = useTranslation("narrator");
	if (!quotes.length && !fileReferences.length && !candidate) return null;
	const unavailable = candidate?.kind === "file" && !candidate.reference;
	return (
		<Group gap={6} mb={6} wrap="wrap" data-composer-quote-chips>
			{quotes.map((quote, index) => (
				<Tooltip
					// biome-ignore lint/suspicious/noArrayIndexKey: quotes are an ordered prefix of the text
					key={index}
					label={<QuotePreview quote={quote} />}
					multiline
					w={360}
					openDelay={300}
					withinPortal
				>
					<span style={solidChipStyle} data-composer-quote>
						<IconQuote size={12} style={{ flexShrink: 0 }} />
						<ChipLabel>{quote.label}</ChipLabel>
						<CloseButton
							size="xs"
							aria-label={t("quote.remove")}
							onMouseDown={keepSelection}
							onClick={() => onRemoveQuote(index)}
						/>
					</span>
				</Tooltip>
			))}
			{fileReferences.map((reference) => (
				<span
					key={reference.id}
					style={solidChipStyle}
					title={`${reference.deviceId} · ${reference.path}`}
				>
					<IconFileText size={12} style={{ flexShrink: 0 }} />
					<ChipLabel>
						{reference.deviceId === "local" ? "" : `${reference.deviceId} · `}
						{fileReferenceToken(reference).replace(/^#file:/, "")}
					</ChipLabel>
					<CloseButton
						size="xs"
						aria-label={t("fileReferences.remove")}
						onMouseDown={keepSelection}
						onClick={() => onRemoveFileReference(reference.id)}
					/>
				</span>
			))}
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
								<IconFileText size={12} style={{ flexShrink: 0 }} />
							) : (
								<IconQuote size={12} style={{ flexShrink: 0 }} />
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
	);
}
