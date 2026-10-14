import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useUserPreferences } from "../../../hooks/useUserPreferences";
import { api } from "../../../lib/api";
import { readSpecTabLayout, type SpecTabLayout } from "./spec-tab-order";

type Preferences = Awaited<ReturnType<typeof api.getUserPreferences>>;

/**
 * 读取并保存某个叙述者的大纲标签排布。
 *
 * 排布持久化在 user_preferences.spec_tab_layout，跟随用户跨设备生效。
 * 保存走乐观更新，失败时回滚并提示。
 */
export function useSpecTabLayout(narratorId: string) {
	const { data: prefs } = useUserPreferences();
	const queryClient = useQueryClient();
	const { t } = useTranslation("narrator");

	const layout = useMemo(
		() => readSpecTabLayout(prefs?.specTabLayout, narratorId),
		[prefs, narratorId],
	);

	const save = useCallback(
		(mutate: (current: SpecTabLayout) => SpecTabLayout) => {
			const key = ["user-preferences"] as const;
			const previous = queryClient.getQueryData<Preferences>(key);
			// 偏好尚未加载成功时不写入：空基准会把服务端已有的排布覆盖掉。
			if (!previous) return;
			// 以缓存中的最新值（含尚未落库的乐观更新）为基准，连续操作不会互相覆盖。
			const next = mutate(readSpecTabLayout(previous.specTabLayout, narratorId));
			queryClient.setQueryData<Preferences>(key, {
				...previous,
				specTabLayout: { ...previous.specTabLayout, [narratorId]: next },
			});
			api.saveSpecTabLayout(narratorId, next).catch(() => {
				queryClient.setQueryData(key, previous);
				notifications.show({
					color: "red",
					title: t("spec.saveError"),
					message: t("spec.tabLayoutSaveError"),
				});
			});
		},
		[narratorId, queryClient, t],
	);

	return { layout, save };
}
