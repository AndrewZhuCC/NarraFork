import { useTranslation } from "react-i18next";
import type { ProviderBalanceSnapshot } from "../../../../shared/provider-balance";
import { QuotaPanelIndicator, useQuotaNow } from "./QuotaPanelIndicator";
import {
	buildSub2ApiSubscriptionQuotaView,
	isSub2ApiSubscriptionSnapshot,
} from "./sub2api-subscription-quota-view";

export interface Sub2ApiSubscriptionQuotaIndicatorProps {
	snapshot: ProviderBalanceSnapshot | null | undefined;
	queryFailed?: boolean;
	isMobile?: boolean;
	transitionDuration?: number;
}

export function Sub2ApiSubscriptionQuotaIndicator({
	snapshot,
	queryFailed,
	...rest
}: Sub2ApiSubscriptionQuotaIndicatorProps) {
	const { t, i18n } = useTranslation("narrator");
	const now = useQuotaNow(isSub2ApiSubscriptionSnapshot(snapshot));
	const view = buildSub2ApiSubscriptionQuotaView(snapshot, t, i18n.language, { queryFailed, now });
	return <QuotaPanelIndicator view={view} now={now} {...rest} />;
}
