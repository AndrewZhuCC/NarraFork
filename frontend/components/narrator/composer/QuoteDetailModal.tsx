import { Box, Button, Group, Modal, Text } from "@mantine/core";
import { IconArrowUpRight, IconQuote } from "@tabler/icons-react";
import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { getShikiLang } from "../../../lib/shiki-lang";
import { Z } from "../../../lib/z-index";
import { MarkdownContent } from "../markdown/MarkdownContent";
import type { ComposerQuote } from "./composer-quotes";

/** Separator `quotePreviewLabel` puts between a file name and the excerpt. */
const FILE_LABEL_SEPARATOR = " · ";

function fenceFor(body: string): string {
	let longest = 0;
	for (const run of body.match(/`+/gu) ?? []) longest = Math.max(longest, run.length);
	return "`".repeat(Math.max(3, longest + 1));
}

/**
 * Markdown for the detail view. A message excerpt is prose and renders as-is; a
 * tool excerpt is output or file content, so it renders as one code block, in the
 * quoted file's language when the quote names one (its first line, see
 * `readPanelQuoteSelection`).
 */
export function quoteDetailMarkdown(quote: Pick<ComposerQuote, "label" | "text" | "anchor">) {
	if (!quote.anchor?.startsWith("t:")) return quote.text;
	const separator = quote.label.indexOf(FILE_LABEL_SEPARATOR);
	const fileName = separator > 0 ? quote.label.slice(0, separator) : "";
	const newline = quote.text.indexOf("\n");
	const named = !!fileName && newline >= 0 && quote.text.slice(0, newline) === fileName;
	const body = named ? quote.text.slice(newline + 1) : quote.text;
	const lang = named ? getShikiLang(fileName) : "text";
	const fence = fenceFor(body);
	return `${fence}${lang === "text" ? "" : lang}\n${body}\n${fence}`;
}

export interface QuoteDetailModalProps {
	/** The quote to show; `null` closes the modal. */
	quote: ComposerQuote | null;
	onClose: () => void;
	/** Scroll to the quoted source. Disabled when the quote has no anchor. */
	onJump?: (quote: ComposerQuote) => void;
}

/** Full text of a composer quote chip, with an explicit jump to its source. */
export function QuoteDetailModal({ quote, onClose, onJump }: QuoteDetailModalProps) {
	const { t } = useTranslation("narrator");
	const { t: tc } = useTranslation("common");
	// Keep the last quote on screen while the close transition plays.
	const shownRef = useRef<ComposerQuote | null>(null);
	if (quote) shownRef.current = quote;
	const shown = quote ?? shownRef.current;
	const canJump = !!shown?.anchor && !!onJump;
	return (
		<Modal
			opened={!!quote}
			onClose={onClose}
			size="lg"
			zIndex={Z.modal}
			title={
				<Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
					<IconQuote size={16} style={{ flexShrink: 0 }} />
					<Text fw={600} size="sm" truncate>
						{shown?.label || t("quote.detailTitle")}
					</Text>
				</Group>
			}
			styles={{ title: { minWidth: 0, flex: 1 } }}
			data-quote-detail
		>
			{shown && (
				<Box
					mah="60vh"
					style={{ overflow: "auto", overflowWrap: "anywhere" }}
					data-quote-detail-body
				>
					<MarkdownContent text={quoteDetailMarkdown(shown)} />
				</Box>
			)}
			<Group justify="flex-end" gap="xs" mt="md">
				<Button variant="default" size="xs" onClick={onClose}>
					{tc("close")}
				</Button>
				<Button
					size="xs"
					leftSection={<IconArrowUpRight size={14} />}
					disabled={!canJump}
					title={shown?.anchor ? undefined : t("quote.jumpUnavailable")}
					onClick={() => {
						if (!shown || !canJump) return;
						onClose();
						onJump?.(shown);
					}}
					data-quote-detail-jump
				>
					{t("quote.jump")}
				</Button>
			</Group>
		</Modal>
	);
}
