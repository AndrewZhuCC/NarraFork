import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useUserPreferences } from "../../../hooks/useUserPreferences";
import { api } from "../../../lib/api";
import { readSpecTabLayout, type SpecTabLayout } from "./spec-tab-order";
import { createSpecTabLayoutSaver, patchNarratorSpecTabLayout } from "./spec-tab-save";

type Preferences = Awaited<ReturnType<typeof api.getUserPreferences>>;

const USER_PREFERENCES_KEY = ["user-preferences"] as const;

/**
 * 读取并保存某个叙述者的大纲标签排布。
 *
 * 排布持久化在 user_preferences.spec_tab_layout，跟随用户跨设备生效。
 * 连续拖拽或置顶会立刻反映到缓存，网络写入则串行提交最新排布；
 * 失败时只回滚这一位叙述者，不清掉缓存里的其他偏好。
 */
export function useSpecTabLayout(narratorId: string) {
	const { data: prefs } = useUserPreferences();
	const queryClient = useQueryClient();
	const { t } = useTranslation("narrator");
	const reportError = useRef<() => void>(() => {});
	reportError.current = () => {
		notifications.show({
			color: "red",
			title: t("spec.saveError"),
			message: t("spec.tabLayoutSaveError"),
		});
	};

	const layout = useMemo(
		() => readSpecTabLayout(prefs?.specTabLayout, narratorId),
		[prefs, narratorId],
	);

	const saver = useMemo(
		() =>
			createSpecTabLayoutSaver({
				readLoadedLayout: () => {
					const previous = queryClient.getQueryData<Preferences>(USER_PREFERENCES_KEY);
					if (!previous) return null;
					return readSpecTabLayout(previous.specTabLayout, narratorId);
				},
				writeLayout: (next) => {
					queryClient.setQueryData<Preferences>(USER_PREFERENCES_KEY, (current) => {
						if (!current) return current;
						return patchNarratorSpecTabLayout(current, narratorId, next);
					});
				},
				persist: (next) => api.saveSpecTabLayout(narratorId, next),
				onPersistError: () => reportError.current(),
			}),
		[narratorId, queryClient],
	);

	const save = useCallback(
		(mutate: (current: SpecTabLayout) => SpecTabLayout) => {
			saver.save(mutate);
		},
		[saver],
	);

	return { layout, save };
}
