import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useProviderModelRefreshCapability,
	useProviderRuntimeCapability,
} from "../../../hooks/usePlatform";
import { api } from "../../../lib/api";
import { requestNugModelRefreshOnPickerOpen } from "../../../lib/nug-model-refresh";

/**
 * NUG catalog-refresh props for {@link ModelMenuItems}, shared by every model
 * picker so they refresh the same way. Empty when the platform cannot refresh.
 */
export function useModelMenuRefreshProps(nugProviderIdByPrefix: Record<string, string>) {
	const { t } = useTranslation("narrator");
	const qc = useQueryClient();
	// Re-fetch one NUG gateway's model catalog straight from the model menu. This
	// is also how a stale "temporarily unavailable" flag gets cleared, since a
	// refresh replaces the cached list wholesale.
	const nugModelRefreshCapability = useProviderModelRefreshCapability("nug");
	const nugRuntimeCapability = useProviderRuntimeCapability("nug");
	const canRefreshNugModels =
		nugModelRefreshCapability.supported &&
		nugRuntimeCapability?.routes?.supported !== false &&
		nugRuntimeCapability?.routes?.perProviderModelsRefresh !== false;
	const [refreshingNugProviderId, setRefreshingNugProviderId] = useState<string | null>(null);
	const handleRefreshNugModels = useCallback(
		async (providerId: string) => {
			setRefreshingNugProviderId(providerId);
			try {
				await api.nugRefreshProviderModels(providerId);
				await qc.invalidateQueries({ queryKey: ["settings"] });
			} catch (error) {
				notifications.show({
					color: "red",
					title: t("refreshProviderModelsErrorTitle"),
					message: error instanceof Error ? error.message : String(error),
				});
			} finally {
				setRefreshingNugProviderId(null);
			}
		},
		[qc, t],
	);
	// Opening the picker also kicks off an opportunistic catalog refresh, so a
	// model that recovered upstream stops showing as "temporarily unavailable"
	// without the user having to find the per-provider refresh button. The real
	// rate limit is a process-wide cooldown on the server; `requestNugModelRefresh…`
	// only avoids redundant round-trips from this tab.
	const handleModelPickerOpened = useCallback(() => {
		void requestNugModelRefreshOnPickerOpen().then((refreshed) => {
			if (refreshed) qc.invalidateQueries({ queryKey: ["settings"] });
		});
	}, [qc]);
	const hasNugModelGroups = Object.keys(nugProviderIdByPrefix).length > 0;
	return canRefreshNugModels
		? {
				nugProviderIdByPrefix,
				onRefreshProviderModels: (providerId: string) => {
					void handleRefreshNugModels(providerId);
				},
				refreshingProviderId: refreshingNugProviderId,
				// No NUG gateway in the picker → nothing to refresh, so don't even ask.
				...(hasNugModelGroups ? { onPickerOpened: handleModelPickerOpened } : {}),
			}
		: {};
}
