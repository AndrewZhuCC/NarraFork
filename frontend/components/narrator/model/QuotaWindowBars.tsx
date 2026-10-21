import { Box, Group } from "@mantine/core";
import { QUOTA_LEVEL_COLORS, type QuotaLevel } from "./quota-panel-view";

/** One vertical bar: what is LEFT of a single limit window (5-hour, weekly, monthly …). */
export interface QuotaBarSpec {
	id: string;
	/** Full window name, used as the bar's accessible label. */
	label: string;
	remainingPercent: number | null;
	level: QuotaLevel;
}

const SIZES = {
	sm: { width: 4, height: 18, gap: 2 },
	xs: { width: 3, height: 12, gap: 1 },
} as const;

const TRACK_BACKGROUND = "light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-5))";

/**
 * Codex-style quota glyph: one thin vertical bar per limit window, filled from
 * the bottom to the share still left and coloured by that window's level. An
 * unknown window is a faint full-height grey bar; an exhausted one is an empty
 * track with a red outline, so a spent window reads at a glance.
 */
export function QuotaWindowBars({
	bars,
	size = "sm",
	forceLevel,
	stale = false,
}: {
	bars: QuotaBarSpec[];
	size?: keyof typeof SIZES;
	/** Overrides every bar's colour (e.g. critical while parked on a quota wait). */
	forceLevel?: QuotaLevel;
	/** Figures are from an earlier fetch; drawn dimmed. */
	stale?: boolean;
}) {
	const { width, height, gap } = SIZES[size];
	return (
		<Group
			gap={gap}
			align="flex-end"
			wrap="nowrap"
			h={height + 2}
			data-quota-bars=""
			style={{ opacity: stale ? 0.55 : 1, flexShrink: 0 }}
		>
			{bars.map((bar) => {
				const level = forceLevel && bar.level !== "exhausted" ? forceLevel : bar.level;
				const color = `var(--mantine-color-${QUOTA_LEVEL_COLORS[level]}-6)`;
				const unknown = bar.remainingPercent == null;
				const ratio = unknown ? 1 : Math.max(0, Math.min(100, bar.remainingPercent ?? 0)) / 100;
				const exhausted = level === "exhausted";
				return (
					<Box
						key={bar.id}
						data-quota-bar={bar.id}
						data-level={level}
						w={width}
						h={height}
						style={{
							position: "relative",
							overflow: "hidden",
							borderRadius: 3,
							background: TRACK_BACKGROUND,
							border: `1px solid ${exhausted ? color : "var(--mantine-color-default-border)"}`,
						}}
					>
						<Box
							style={{
								position: "absolute",
								left: 0,
								right: 0,
								bottom: 0,
								height: exhausted ? 0 : `${Math.round(ratio * 100)}%`,
								minHeight: !exhausted && ratio > 0 ? 2 : 0,
								background: unknown ? "var(--mantine-color-gray-5)" : color,
								opacity: unknown ? 0.45 : 1,
								transition: "height 160ms ease",
							}}
						/>
					</Box>
				);
			})}
		</Group>
	);
}
