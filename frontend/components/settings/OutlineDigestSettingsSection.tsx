import { Group, Select, Stack, Switch, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { OUTLINE_DIGEST_DEFAULT_TIME, OUTLINE_DIGEST_TIME_PATTERN } from "@shared/outline-digest";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { useOutlineEnabled } from "../../hooks/useOutline";
import { useUpdateUserPreferences, useUserPreferences } from "../../hooks/useUserPreferences";
import { api } from "../../lib/api";

/** The Select's value for "the server's zone" (stored as null). */
const SERVER_ZONE = "__server__";

function timeZones(): string[] {
	const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
	try {
		return intl.supportedValuesOf?.("timeZone") ?? [];
	} catch {
		return [];
	}
}

/**
 * Instance-wide automatic report switch, alongside each person's day/time settings.
 * Null personal settings use the server's time zone and 07:00.
 * Shown only while the outline is switched on.
 */
export function OutlineDigestSettingsSection() {
	const { t } = useTranslation("settings");
	const { enabled } = useOutlineEnabled();
	const { data } = useUserPreferences();
	const update = useUpdateUserPreferences();
	const { data: user } = useCurrentUser();
	const { data: instance } = useQuery({
		queryKey: ["settings"],
		queryFn: api.getSettings,
		enabled,
		staleTime: 30_000,
	});
	const queryClient = useQueryClient();
	const updateReports = useMutation({
		mutationFn: (dailyDigestEnabled: boolean) =>
			api.updateSettings({ outline: { dailyDigestEnabled } }),
		onSuccess: () => queryClient.invalidateQueries({ queryKey: ["settings"] }),
		onError: (error) =>
			notifications.show({
				color: "red",
				title: t("outlineDigestUpdateError"),
				message: error.message,
			}),
	});
	const dailyEnabled =
		(instance as { outline?: { dailyDigestEnabled?: unknown } } | undefined)?.outline
			?.dailyDigestEnabled === true;
	const zones = useMemo(timeZones, []);
	const stored = data?.outlineDigestTime ?? null;
	const [time, setTime] = useState(stored ?? "");
	useEffect(() => setTime(stored ?? ""), [stored]);
	if (!enabled || !data) return null;

	const serverZone = data.outlineDigestServerTimeZone ?? "UTC";
	const zone = data.outlineDigestTimeZone ?? SERVER_ZONE;
	const options = [
		{ value: SERVER_ZONE, label: t("outlineDigestServerZone", { zone: serverZone }) },
		...zones.filter((z) => z !== data.outlineDigestTimeZone).map((z) => ({ value: z, label: z })),
		...(data.outlineDigestTimeZone
			? [{ value: data.outlineDigestTimeZone, label: data.outlineDigestTimeZone }]
			: []),
	];
	const saveTime = (value: string) => {
		const next = value === "" ? null : value;
		if (next === stored || (next !== null && !OUTLINE_DIGEST_TIME_PATTERN.test(next))) return;
		update.mutate({ outlineDigestTime: next });
	};

	return (
		<Stack gap="xs" data-testid="outline-digest-settings">
			<Text fw={500}>{t("outlineDigestTitle")}</Text>
			<Text size="sm" c="dimmed">
				{t("outlineDigestDescription")}
			</Text>
			<Switch
				label={t("outlineDigestEnabled")}
				description={t("outlineDigestEnabledDescription")}
				checked={dailyEnabled}
				disabled={!instance || user?.role !== "admin" || updateReports.isPending}
				onChange={(event) => updateReports.mutate(event.currentTarget.checked)}
				data-testid="outline-digest-enabled"
			/>
			<Group align="flex-end" wrap="wrap" gap="sm">
				<Select
					label={t("outlineDigestTimeZone")}
					data={options}
					value={zone}
					searchable
					limit={100}
					allowDeselect={false}
					nothingFoundMessage={t("outlineDigestNoZone")}
					onChange={(value) => {
						if (!value) return;
						update.mutate({ outlineDigestTimeZone: value === SERVER_ZONE ? null : value });
					}}
					style={{ flex: "1 1 240px", maxWidth: 360 }}
					data-testid="outline-digest-zone"
				/>
				<TextInput
					type="time"
					label={t("outlineDigestTime")}
					description={t("outlineDigestTimeDefault", { time: OUTLINE_DIGEST_DEFAULT_TIME })}
					value={time}
					onChange={(event) => setTime(event.currentTarget.value)}
					onBlur={() => saveTime(time)}
					style={{ flex: "0 1 160px" }}
					data-testid="outline-digest-time"
				/>
			</Group>
		</Stack>
	);
}
