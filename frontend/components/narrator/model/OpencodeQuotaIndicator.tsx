import { useTranslation } from "react-i18next";
import type { OpencodeUsageCache } from "../../../lib/api/types";
import { formatFullLocaleDateTime } from "../../../lib/format";
import {
	buildOpencodeQuotaView,
	formatOpencodeAmount,
	type OpencodeQuotaView,
	type OpencodeViewT,
	opencodeWindowLabel,
	opencodeWindowShortLabel,
} from "./opencode-usage-view";
import { QuotaPanelIndicator, useQuotaNow } from "./QuotaPanelIndicator";
import type { QuotaPanelView } from "./quota-panel-view";

/** Map the OpenCode Go display model onto the shared quota panel. */
export function opencodeQuotaPanelView(
	view: OpencodeQuotaView,
	t: OpencodeViewT,
	locale: string,
): QuotaPanelView {
	const windows = view.windows.map((window) => ({
		id: window.key,
		label: opencodeWindowLabel(window.key, t),
		shortLabel: opencodeWindowShortLabel(window.key, t),
		usedPercent: window.usedPercent,
		remainingPercent: window.remainingPercent,
		used: window.used,
		limit: window.limit,
		resetAt: window.resetAt,
		resetCaptionKey: window.key === "weekly" ? "opencode.resetsInWeekly" : undefined,
		resetHint:
			window.resetDerived && window.resetAt != null
				? t("opencode.resetDerivedHint", {
						time: formatFullLocaleDateTime(new Date(window.resetAt), locale),
					})
				: undefined,
		level: window.level,
	}));
	return {
		title: t("opencode.title"),
		unavailableText: t("opencode.unavailable"),
		windows,
		limiting: windows.find((window) => window.id === view.limiting?.key) ?? null,
		level: view.level,
		rows:
			view.balance == null
				? []
				: [{ label: t("opencode.balance"), value: formatOpencodeAmount(view.balance) }],
		failed: view.failed,
		failureDetail: null,
		fetchedAt: view.fetchedAt,
	};
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
export function OpencodeQuotaIndicator({ usage, ...rest }: OpencodeQuotaIndicatorProps) {
	const { t, i18n } = useTranslation("narrator");
	const now = useQuotaNow(usage != null);
	const view = buildOpencodeQuotaView(usage, now);
	return (
		<QuotaPanelIndicator
			view={view ? opencodeQuotaPanelView(view, t, i18n.language) : null}
			now={now}
			{...rest}
		/>
	);
}
