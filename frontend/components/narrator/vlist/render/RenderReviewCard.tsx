/**
 * RenderReviewCard.tsx — render copy of the review card measured by
 * measure-review-card.ts.
 *
 * Shaped like a tool card on purpose: a header row over a maxHeight-capped scroll box
 * holding a REAL markdown body. That combination is what the three earlier shapes each
 * lacked — a clamped one-liner could not show the findings, a plain pre-wrap card could
 * not render markdown or highlight code, and neither could scroll a long conclusion.
 *
 * Every offset comes from the measured element; nothing here decides geometry. The body
 * is painted at `measured.contentWidth` (the width the line breaking used) inside a box
 * of exactly `measured.bodyHeight`, and the overflow scrolls — so a conclusion of any
 * length occupies the height the measure pass committed to.
 */

import { Badge, Group, Paper, Text } from "@mantine/core";
import { IconEyeCheck } from "@tabler/icons-react";
import {
	type MeasuredReviewCard,
	REVIEW_CARD_BORDER,
	REVIEW_CARD_PADDING,
	REVIEW_HEADER_GAP,
	reviewCardHeaderHeight,
} from "../measure/measure-review-card";
import { DETAIL_BOX_PADDING_X, DETAIL_BOX_PADDING_Y } from "../measure/measure-tool-call";
import { RenderMarkdown } from "./RenderMarkdown";

export interface RenderReviewCardProps {
	measured: MeasuredReviewCard;
	/**
	 * Header chrome, forwarded by the adapter as height-neutral data.
	 * Read off the measured element's `data` slot by the integration layer.
	 */
	data?: {
		verdictLabel?: string;
		revisedLabel?: string;
		color?: string;
	};
	/** Localized note for a body cut at the parse ceiling. */
	truncatedLabel?: string;
}

function cssColor(color: string, shade: number): string {
	return `var(--mantine-color-${color}-${shade})`;
}

export function RenderReviewCard({ measured, data, truncatedLabel }: RenderReviewCardProps) {
	const color = data?.color ?? "gray";
	return (
		<Paper
			p="sm"
			radius="md"
			withBorder
			style={{
				height: measured.height,
				boxSizing: "border-box",
				// The accent lives in the border rather than a filled panel: the body inside is
				// a scrolling document, and tinting the whole card would fight the code blocks
				// and quote rails the markdown renderer draws.
				borderColor: cssColor(color, 6),
			}}
		>
			<Group
				gap={6}
				wrap="nowrap"
				style={{ height: reviewCardHeaderHeight() }}
				// A single non-wrapping row, exactly as measured.
				align="center"
			>
				<IconEyeCheck size={16} style={{ flexShrink: 0, color: cssColor(color, 6) }} />
				<Badge size="xs" color={color} variant="light" style={{ flexShrink: 0 }}>
					{data?.verdictLabel ?? ""}
				</Badge>
				{data?.revisedLabel ? (
					<Badge size="xs" color="gray" variant="light" style={{ flexShrink: 0 }}>
						{data.revisedLabel}
					</Badge>
				) : null}
				{measured.isPrefix && truncatedLabel ? (
					<Text size="xs" c="dimmed" style={{ whiteSpace: "nowrap" }}>
						{truncatedLabel}
					</Text>
				) : null}
			</Group>
			<div
				data-vlist-review-body
				style={{
					marginTop: REVIEW_HEADER_GAP,
					// Fixed by the measure pass; the overflow scrolls rather than growing the row.
					height: measured.bodyHeight,
					maxHeight: measured.appliedCap,
					// A markdown body is always wrapped, so horizontal overflow is a paint
					// artifact (see the tool card's CappedBodyBox for the full rationale).
					overflowY: "auto",
					overflowX: "hidden",
					boxSizing: "border-box",
					padding: `${DETAIL_BOX_PADDING_Y}px ${DETAIL_BOX_PADDING_X}px`,
					// Scheme-aware, like every other detail panel: a fixed dark surface renders
					// near-black text on near-black in light mode (see vlist-markdown.css).
					background: "var(--vlist-detail-panel-bg)",
					borderRadius: 4,
					position: "relative",
				}}
			>
				<RenderMarkdown
					measured={{
						height: measured.frame.contentHeight,
						blocks: measured.blocks,
						frame: measured.frame,
						// The width the line breaking used — NOT the box's inner width, which can
						// differ and would re-wrap the text under a height predicted for the other.
						contentWidth: measured.contentWidth,
						usedWidth: measured.frame.usedWidth,
					}}
				/>
			</div>
		</Paper>
	);
}

export const RENDER_REVIEW_CARD_CHROME = {
	REVIEW_CARD_PADDING,
	REVIEW_CARD_BORDER,
	REVIEW_HEADER_GAP,
} as const;
