import { OFFICIAL_GITHUB_REPOSITORY } from "@shared/github-repository";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ApiError, api } from "../lib/api";
import {
	DEFAULT_GITHUB_REPOSITORY,
	type UpdateSourceSettings,
	updateSettingsKey,
} from "../lib/update-source";
import { useCurrentUser } from "./useAuth";
import type { useUpdateCheck } from "./useUpdateCheck";

export const OFFICIAL_UPDATE_SERVER_URL = "https://narrafork-update.b.domexie.cn";
const CONNECTION_ERRORS = new Set([
	"NETWORK_ERROR",
	"TIMEOUT",
	"GITHUB_RATE_LIMIT",
	"RATE_LIMITED",
]);

function isOfficialGithub(update?: UpdateSourceSettings): boolean {
	return (
		(update?.source ?? "github") === "github" &&
		(update?.githubRepository ?? DEFAULT_GITHUB_REPOSITORY).trim().toLowerCase() ===
			OFFICIAL_GITHUB_REPOSITORY.toLowerCase()
	);
}

type Check = Pick<ReturnType<typeof useUpdateCheck>, "settingsKey" | "source" | "errorCode">;

/** One completed GitHub check per browser/user, not one prompt per polling interval. */
export function useUpdateSourcePrompt(check: Check, language: string) {
	const queryClient = useQueryClient();
	const { data: user } = useCurrentUser();
	const consumed = useRef(new Set<string>());
	const savingRef = useRef(false);
	const [prompt, setPrompt] = useState<{ userId: string; settingsKey: string } | null>(null);
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const chinese = language.toLowerCase().startsWith("zh");

	useEffect(() => {
		if (user?.role !== "admin" || !check.settingsKey || check.source !== "github") return;
		const settings = queryClient.getQueryData<Awaited<ReturnType<typeof api.getSettings>>>([
			"settings",
		]);
		if (
			!isOfficialGithub(settings?.update) ||
			updateSettingsKey(settings?.update) !== check.settingsKey
		)
			return;
		if (check.errorCode === "UPDATE_SOURCE_CHANGED") return;
		const key = `narrafork:first-github-update-check:${user.id}`;
		if (consumed.current.has(key)) return;
		consumed.current.add(key);
		try {
			if (localStorage.getItem(key)) return;
			localStorage.setItem(key, "1");
		} catch {
			// Restricted storage must not break update checks; deduplicate within this mount.
		}
		if (chinese && CONNECTION_ERRORS.has(check.errorCode ?? "")) {
			setPrompt({ userId: user.id, settingsKey: check.settingsKey });
		}
	}, [check.settingsKey, check.source, check.errorCode, chinese, user, queryClient]);

	const dismiss = () => {
		if (!savingRef.current) {
			setPrompt(null);
			setError(null);
		}
	};
	const confirm = async () => {
		if (
			!prompt ||
			savingRef.current ||
			user?.role !== "admin" ||
			user.id !== prompt.userId ||
			!chinese
		)
			return;
		savingRef.current = true;
		setSaving(true);
		setError(null);
		try {
			// Re-read before writing: do not use the settings captured by the initial check.
			const settings = await api.getSettings();
			queryClient.setQueryData(["settings"], settings);
			if (
				!isOfficialGithub(settings.update) ||
				updateSettingsKey(settings.update) !== prompt.settingsKey
			) {
				setError("updateSourceChanged");
				return;
			}
			// PATCH only these fields; the server merges the other update preferences.
			const saved = await api.updateSettings({
				expectedUpdateSettingsKey: prompt.settingsKey,
				update: {
					source: "update-server",
					serverUrl: OFFICIAL_UPDATE_SERVER_URL,
					product: "narrafork",
				},
			});
			queryClient.setQueryData(["settings"], saved);
			setPrompt(null);
			await queryClient.resetQueries({ queryKey: ["update-check"] });
			await queryClient.invalidateQueries({ queryKey: ["update-check"], refetchType: "none" });
		} catch (cause) {
			if (
				cause instanceof ApiError &&
				cause.status === 409 &&
				cause.data?.code === "UPDATE_SOURCE_CHANGED"
			) {
				setError("updateSourceChanged");
				await queryClient
					.fetchQuery({ queryKey: ["settings"], queryFn: api.getSettings, staleTime: 0 })
					.catch(() => undefined);
				await queryClient.resetQueries({ queryKey: ["update-check"] });
			} else {
				setError("updateMirrorSwitchFailed");
			}
		} finally {
			savingRef.current = false;
			setSaving(false);
		}
	};

	return {
		opened: !!prompt && chinese && user?.role === "admin" && user.id === prompt.userId,
		saving,
		error,
		dismiss,
		confirm,
	};
}
