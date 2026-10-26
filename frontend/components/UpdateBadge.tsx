import { Alert, Badge, Button, Group, Loader, Modal, Stack, Text, Tooltip } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { preparedMatchesRelease } from "@shared/update-identity";
import { IconClock, IconRocket } from "@tabler/icons-react";
import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../hooks/useAuth";
import { useUpdateCheck } from "../hooks/useUpdateCheck";
import { useUpdateScheduleStatus } from "../hooks/useUpdateSchedule";
import { OFFICIAL_UPDATE_SERVER_URL, useUpdateSourcePrompt } from "../hooks/useUpdateSourcePrompt";
import { resolveScheduledUpdatePill } from "../lib/update-state";
import type { UpdateModalData } from "./UpdateModal";

const UpdateModal = lazy(() =>
	import("./UpdateModal").then((m) => ({
		default: m.UpdateModal,
	})),
);

/**
 * Inline update badge — shown next to the app title in the header.
 *
 * Two states share one pill:
 * - An available update invites the user to open the detail modal.
 * - A scheduled update reports which phase the restart is waiting in. The coordination waits
 *   have no deadline, so this is the only ambient signal that the switch is pending; without
 *   it a user who closed the dialog would see nothing at all. It therefore survives a failed
 *   update re-check, which would otherwise flip `updateAvailable` back to false mid-drain.
 *
 * Both update endpoints are admin-only. Prepared artifacts are discovered independently of
 * release recommendations, including same-version builds and offline update sources.
 */
export function UpdateBadge() {
	const { t, i18n } = useTranslation("common");
	const [opened, { open, close }] = useDisclosure(false);
	const { data: user } = useCurrentUser();
	const isAdmin = user?.role === "admin";
	const check = useUpdateCheck();
	const sourcePrompt = useUpdateSourcePrompt(check, i18n.resolvedLanguage ?? i18n.language);
	const {
		updateAvailable,
		latestVersion,
		currentVersion,
		releaseInfo,
		releaseNotes,
		releaseNotesPerVersion,
		releaseDate,
		downloadSize,
		totalSize,
		strategy,
		settingsKey,
	} = check;
	const [modalData, setModalData] = useState<UpdateModalData>({});
	const checkedTarget = releaseInfo?.version ?? latestVersion;
	const { data: scheduleStatus } = useUpdateScheduleStatus({ enabled: isAdmin });
	const pill = resolveScheduledUpdatePill(scheduleStatus);
	const prepared = scheduleStatus?.ready === true;
	const targetVersion =
		(prepared ? scheduleStatus.version : undefined) ??
		(pill ? scheduleStatus?.targetVersion : undefined) ??
		checkedTarget;
	const recommendationMatchesTarget =
		checkedTarget === targetVersion &&
		(!(prepared || pill) || preparedMatchesRelease(scheduleStatus?.preparedIdentity, releaseInfo));
	const showBadge = isAdmin && (updateAvailable || !!pill || prepared);

	return (
		<>
			<Modal
				opened={sourcePrompt.opened}
				onClose={sourcePrompt.dismiss}
				title={t("updateMirrorPromptTitle")}
				closeOnClickOutside={!sourcePrompt.saving}
				closeOnEscape={!sourcePrompt.saving}
				withCloseButton={!sourcePrompt.saving}
				centered
			>
				<Stack>
					<Text size="sm">{t("updateMirrorPromptDescription")}</Text>
					<Text size="xs" c="dimmed">
						{OFFICIAL_UPDATE_SERVER_URL}
					</Text>
					{sourcePrompt.error && <Alert color="red">{t(sourcePrompt.error)}</Alert>}
					<Group justify="flex-end">
						<Button variant="default" onClick={sourcePrompt.dismiss} disabled={sourcePrompt.saving}>
							{t("updateMirrorKeepGithub")}
						</Button>
						<Button onClick={() => void sourcePrompt.confirm()} loading={sourcePrompt.saving}>
							{t("updateMirrorSwitch")}
						</Button>
					</Group>
				</Stack>
			</Modal>
			{showBadge && (
				<>
					<Tooltip
						label={
							pill
								? t(pill.tooltipKey, {
										version: scheduleStatus?.targetVersion ?? targetVersion,
									})
								: t("updateViewDetails")
						}
						position="bottom"
						withArrow
					>
						<Badge
							size="sm"
							variant="light"
							color={pill ? "orange" : "indigo"}
							leftSection={
								pill ? (
									pill.busy ? (
										<Loader size={10} color="orange" />
									) : (
										<IconClock size={12} />
									)
								) : (
									<IconRocket size={12} />
								)
							}
							style={{ cursor: "pointer" }}
							onClick={() => {
								setModalData({
									latestVersion: targetVersion,
									currentVersion,
									preparedStatus: prepared || pill ? scheduleStatus : undefined,
									...(recommendationMatchesTarget && {
										releaseInfo,
										releaseNotes,
										releaseNotesPerVersion,
										releaseDate,
										downloadSize,
										totalSize,
										strategy,
										settingsKey,
									}),
								});
								open();
							}}
						>
							{pill
								? t(pill.labelKey)
								: (!updateAvailable || !recommendationMatchesTarget) && prepared
									? t("updateReadyBadge", { version: targetVersion })
									: t("updateBadge", { from: currentVersion, to: latestVersion })}
						</Badge>
					</Tooltip>

					<Suspense fallback={null}>
						<UpdateModal opened={opened} onClose={close} data={modalData} />
					</Suspense>
				</>
			)}
		</>
	);
}
