import { IconArrowBarToRight, IconFileText, IconQuote, IconX } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import classes from "./InlineComposerEditor.module.css";
import type { QuoteCandidate } from "./use-quote-candidate";

export interface InlineQuoteCandidateProps {
	candidate: QuoteCandidate | null;
	onAccept(candidate: QuoteCandidate): void;
	onDismiss(candidate: QuoteCandidate): void;
	showShortcut?: boolean;
	disabled?: boolean;
}

export function InlineQuoteCandidate({
	candidate,
	onAccept,
	onDismiss,
	showShortcut = true,
	disabled = false,
}: InlineQuoteCandidateProps) {
	const { t } = useTranslation("narrator");
	if (!candidate) return null;
	const unavailable = candidate.kind === "file" && !candidate.reference;
	const label = candidate.kind === "text" ? candidate.quote.label : candidate.label;
	const Icon = candidate.kind === "text" ? IconQuote : IconFileText;
	const keyboardShortcut = showShortcut && candidate.kind === "text";
	return (
		// biome-ignore lint/a11y/useSemanticElements: a paragraph widget requires phrasing content, not a block fieldset.
		<span
			className={classes.candidate}
			data-inline-quote-candidate
			role="group"
			aria-label={t("quote.add", { defaultValue: "Quote selection" })}
			onPointerDown={(event) => event.preventDefault()}
		>
			<button
				type="button"
				className={classes.candidateAccept}
				aria-keyshortcuts={keyboardShortcut ? "Tab" : undefined}
				disabled={disabled || unavailable}
				title={
					unavailable
						? t("quote.fileUnsaved", { defaultValue: "Save the file before quoting" })
						: candidate.kind === "text"
							? candidate.quote.text
							: label
				}
				onClick={() => onAccept(candidate)}
			>
				{keyboardShortcut ? (
					<kbd
						className={classes.candidateShortcut}
						aria-label={t("quote.tabKey")}
						title={t("quote.tabKey")}
					>
						<IconArrowBarToRight size={12} aria-hidden="true" />
						Tab
					</kbd>
				) : (
					<Icon size={14} />
				)}
				<span className={classes.candidateAction} data-quote-action>
					{t("quote.add", { defaultValue: "Quote selection" })}
				</span>
				<span className={classes.label}>· {label}</span>
			</button>
			<button
				type="button"
				className={classes.action}
				aria-label={t("quote.dismiss", { defaultValue: "Dismiss" })}
				onClick={() => onDismiss(candidate)}
			>
				<IconX size={14} />
			</button>
		</span>
	);
}
