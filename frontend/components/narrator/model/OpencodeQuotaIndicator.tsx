import {
	Badge,
	Group,
	Popover,
	Progress,
	Stack,
	Text,
	Tooltip,
	UnstyledButton,
} from "@mantine/core";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import { formatFullLocaleDateTime } from "../../../lib/format";
import {
	buildOpencodeQuotaView,
	formatOpencodeAmount,
	formatOpencodeBadgeText,
	formatOpencodeCountdown,
	formatOpencodeSummaryLines,
	OPENCODE_LEVEL_COLORS,
	type OpencodeQuotaLevel,
	type OpencodeQuotaView,
	type OpencodeWindowView,
	opencodeWindowLabel,
} from "./opencode-usage-view";

/** Countdown resolution; minutes are the smallest unit shown. */
const TICK_MS = 30_000;
const CLOSE_DELAY_MS = 150;

/** Re-render periodically while mounted so countdowns stay current. */
function useNow(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		setNow(Date.now());
		const id = window.setInterval(() => setNow(Date.now()), TICK_MS);
		return () => window.clearInterval(id);
	}, [active]);
	return now;
}

function WindowRow({
	window,
	now,
	locale,
}: {
	window: OpencodeWindowView;
	now: number;
	locale: string;
}) {
	const { t } = useTranslation("narrator");
	const color = OPENCODE_LEVEL_COLORS[window.level];
	const remaining =
		window.remainingPercent == null
			? t("opencode.remainingUnknown")
			: t("opencode.remainingPercent", { percent: window.remainingPercent });
	const amounts =
		window.used != null && window.limit != null
			? t("opencode.usedOfLimit", {
					used: formatOpencodeAmount(window.used),
					limit: formatOpencodeAmount(window.limit),
				})
			: null;
	return (
		<Stack gap={2}>
			<Group justify="space-between" gap="xs" wrap="nowrap">
				<Text size="xs" fw={500}>
					{opencodeWindowLabel(window.key, t)}
				</Text>
				<Text size="xs" c={window.level === "ok" ? "dimmed" : color}>
					{window.level === "exhausted" ? t("opencode.exhaustedShort") : remaining}
				</Text>
			</Group>
			<Progress
				value={window.usedPercent ?? 0}
				color={color}
				size="sm"
				radius="xl"
				aria-label={`${opencodeWindowLabel(window.key, t)}: ${remaining}`}
				striped={window.usedPercent == null}
			/>
			<Group justify="space-between" gap="xs" wrap="nowrap">
				<Text size="xs" c="dimmed">
					{amounts ??
						(window.usedPercent == null
							? t("opencode.usedUnknown")
							: t("opencode.usedPercent", { percent: Math.round(window.usedPercent) }))}
				</Text>
				{window.resetAt != null && (
					<Tooltip
						label={
							window.resetDerived
								? t("opencode.resetDerivedHint", {
										time: formatFullLocaleDateTime(new Date(window.resetAt), locale),
									})
								: formatFullLocaleDateTime(new Date(window.resetAt), locale)
						}
						withinPortal
					>
						<Text size="xs" c="dimmed">
							{t(window.key === "weekly" ? "opencode.resetsInWeekly" : "opencode.resetsIn", {
								time: formatOpencodeCountdown(window.resetAt - now, t),
							})}
						</Text>
					</Tooltip>
				)}
			</Group>
		</Stack>
	);
}

function QuotaDetails({
	view,
	now,
	waitingText,
}: {
	view: OpencodeQuotaView;
	now: number;
	waitingText: string | null;
}) {
	const { t, i18n } = useTranslation("narrator");
	return (
		<Stack gap="sm" w={260}>
			<Text size="xs" fw={600}>
				{t("opencode.title")}
			</Text>
			{waitingText && (
				<Text size="xs" c="red">
					{waitingText}
				</Text>
			)}
			{view.windows.length === 0 && (
				<Text size="xs" c="dimmed">
					{t("opencode.noWindows")}
				</Text>
			)}
			{view.windows.map((window) => (
				<WindowRow key={window.key} window={window} now={now} locale={i18n.language} />
			))}
			{view.balance != null && (
				<Group justify="space-between" gap="xs">
					<Text size="xs" fw={500}>
						{t("opencode.balance")}
					</Text>
					<Text size="xs">{formatOpencodeAmount(view.balance)}</Text>
				</Group>
			)}
			{(view.failed || view.fetchedAt > 0) && (
				<Text size="xs" c={view.failed ? "yellow" : "dimmed"}>
					{view.failed
						? t("opencode.staleHint")
						: t("opencode.updatedAt", {
								time: formatFullLocaleDateTime(new Date(view.fetchedAt), i18n.language),
							})}
				</Text>
			)}
		</Stack>
	);
}

export interface OpencodeQuotaIndicatorProps {
	usage: OpencodeUsageCache | null | undefined;
	/**
	 * The narrator is parked on an OpenCode quota wait. Forces the critical
	 * colour so the indicator agrees with the "waiting for quota" status even
	 * before the next `/usage` refresh reports the window as spent.
	 */
	waitingForQuota?: boolean;
	/** Status text of that wait, e.g. "Waiting for quota · <time>". */
	waitingText?: string | null;
	isMobile?: boolean;
	/** Popover open/close animation in ms; tests pass 0 so no transition is scheduled. */
	transitionDuration?: number;
}

/**
 * OpenCode Go plan allowance in the status bar: a badge with the limiting
 * window's remaining share, opening a panel with the 5-hour, weekly and
 * monthly windows (progress, used/total, reset countdown) and the balance.
 *
 * Renders nothing until there is something to show, and never blocks the
 * composer: a failed or slow `/usage` only affects this widget.
 */
export function OpencodeQuotaIndicator({
	usage,
	waitingForQuota = false,
	waitingText = null,
	isMobile = false,
	transitionDuration,
}: OpencodeQuotaIndicatorProps) {
	const { t } = useTranslation("narrator");
	const [opened, setOpened] = useState(false);
	const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const now = useNow(usage != null);
	const cancelClose = useCallback(() => {
		if (closeTimer.current) {
			clearTimeout(closeTimer.current);
			closeTimer.current = null;
		}
	}, []);
	const scheduleClose = useCallback(() => {
		cancelClose();
		closeTimer.current = setTimeout(() => {
			setOpened(false);
			closeTimer.current = null;
		}, CLOSE_DELAY_MS);
	}, [cancelClose]);
	useEffect(() => () => cancelClose(), [cancelClose]);

	const view = buildOpencodeQuotaView(usage, now);
	if (!view) return null;
	const level: OpencodeQuotaLevel =
		waitingForQuota && view.level !== "exhausted" ? "critical" : view.level;
	const color = view.failed && level === "unknown" ? "gray" : OPENCODE_LEVEL_COLORS[level];
	const text = formatOpencodeBadgeText(view, t);
	const ariaLabel = [t("opencode.title"), ...formatOpencodeSummaryLines(view, now, t)].join(". ");

	return (
		<Popover
			opened={opened}
			onChange={setOpened}
			position="top"
			withArrow
			withinPortal
			shadow="md"
			{...(transitionDuration === undefined
				? {}
				: { transitionProps: { duration: transitionDuration } })}
		>
			<Popover.Target>
				<UnstyledButton
					aria-label={ariaLabel}
					aria-expanded={opened}
					onClick={(event) => {
						event.stopPropagation();
						cancelClose();
						setOpened((value) => !value);
					}}
					onPointerEnter={() => {
						if (isMobile) return;
						cancelClose();
						setOpened(true);
					}}
					onPointerLeave={() => {
						if (!isMobile) scheduleClose();
					}}
					style={{ flexShrink: 0 }}
				>
					<Badge
						size="sm"
						radius="sm"
						variant={level === "ok" || level === "unknown" ? "light" : "filled"}
						color={color}
						style={{ cursor: "pointer", textTransform: "none" }}
					>
						{text}
					</Badge>
				</UnstyledButton>
			</Popover.Target>
			<Popover.Dropdown
				onPointerEnter={() => {
					if (!isMobile) cancelClose();
				}}
				onPointerLeave={() => {
					if (!isMobile) scheduleClose();
				}}
			>
				<QuotaDetails view={view} now={now} waitingText={waitingForQuota ? waitingText : null} />
			</Popover.Dropdown>
		</Popover>
	);
}
