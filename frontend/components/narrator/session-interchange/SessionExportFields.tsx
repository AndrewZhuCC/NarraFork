/**
 * The Claude Code / Codex half of the narrator's export section.
 *
 * Shown in place of the transcript options when one of the two resumable formats
 * is picked. A resumable session is always the full history, so the transcript's
 * "include tool IO" and "full history" switches do not apply here.
 *
 * Destinations: a browser download (anyone who can read the narrator), the
 * server's CLI config dir (admins; hidden otherwise), or an online device the
 * caller manages (hidden when there is none).
 */

import {
	ActionIcon,
	Alert,
	Button,
	Checkbox,
	Code,
	Group,
	Select,
	Stack,
	Text,
	TextInput,
	Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
	IconAlertTriangle,
	IconCheck,
	IconCopy,
	IconDownload,
	IconUpload,
} from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../../lib/api";
import type { SessionExportOptions, SessionFormat } from "../../../lib/api/session-interchange";
import { describeApiError } from "../../../lib/api-error";
import { CopyButton } from "../../common/CopyButton";
import { SessionWarningsList } from "./SessionWarningsList";
import {
	availableExportDestinations,
	downloadOutcome,
	type ExportDestination,
	type ExportOutcome,
	selectedSessionDevice,
	sessionConfigDir,
	writeOutcome,
} from "./session-interchange-logic";
import { useSessionTargets } from "./useSessionTargets";

const DESTINATION_LABEL_KEYS: Record<ExportDestination, string> = {
	download: "details.sessionExport.destinationDownload",
	local: "details.sessionExport.destinationServer",
	device: "details.sessionExport.destinationDevice",
};

function saveBlob(blob: Blob, fileName: string) {
	const url = URL.createObjectURL(blob);
	try {
		const link = document.createElement("a");
		link.href = url;
		link.download = fileName;
		document.body.append(link);
		link.click();
		link.remove();
	} finally {
		URL.revokeObjectURL(url);
	}
}

export function SessionExportFields({
	narratorId,
	format,
	narratorCwd,
	projectId,
}: {
	narratorId: string;
	format: SessionFormat;
	narratorCwd: string | null;
	/** The narrator's project; admits that project's devices as destinations. */
	projectId: string | null;
}) {
	const { t } = useTranslation("narrator");
	const { t: te } = useTranslation("errors");
	const targets = useSessionTargets(true, projectId);
	const [requestedDestination, setDestination] = useState<ExportDestination>("download");
	const [deviceId, setDeviceId] = useState<string | null>(null);
	const [root, setRoot] = useState("");
	const [cwd, setCwd] = useState(narratorCwd ?? "");
	const [modelProvider, setModelProvider] = useState("openai");
	const [dropAllReasoning, setDropAllReasoning] = useState(false);
	const [outcome, setOutcome] = useState<ExportOutcome | null>(null);

	const available = availableExportDestinations(targets.isAdmin, targets.devices.length);
	// A destination that disappeared (devices went offline) falls back to download.
	const destination = available.includes(requestedDestination) ? requestedDestination : "download";
	const device = selectedSessionDevice(targets.devices, deviceId);
	const destinations = available.map((value) => ({
		value,
		label: t(DESTINATION_LABEL_KEYS[value]),
	}));
	const configDir = sessionConfigDir(format);

	const mutation = useMutation({
		mutationFn: async (): Promise<ExportOutcome> => {
			const options: SessionExportOptions = {
				format,
				cwd: cwd.trim() || undefined,
				...(format === "codex" ? { codexModelProvider: modelProvider.trim() || undefined } : {}),
				...(format === "claude-code" ? { dropAllReasoning } : {}),
			};
			if (destination === "download") {
				const download = await api.downloadSessionExport(narratorId, options);
				saveBlob(download.blob, download.fileName ?? `${download.sessionId ?? narratorId}.jsonl`);
				return downloadOutcome(format, download);
			}
			// The button is disabled without a usable device; this guards a list refresh
			// between render and click instead of sending an empty device id.
			if (destination === "device" && !device) {
				throw new Error(t("details.sessionExport.deviceUnavailable"));
			}
			const location =
				destination === "local"
					? { target: "local" as const, ...(root.trim() ? { root: root.trim() } : {}) }
					: {
							target: "device" as const,
							deviceId: device?.id ?? "",
							...(root.trim() ? { root: root.trim() } : {}),
						};
			return writeOutcome(
				await api.writeSessionExport(narratorId, {
					...options,
					location,
				}),
			);
		},
		onSuccess: setOutcome,
		onError: (error) => {
			notifications.show({
				title: t("details.exportErrorTitle"),
				message: describeApiError(error, te, t("details.exportError")).message,
				color: "red",
			});
		},
	});

	const disabled = mutation.isPending;

	return (
		<Stack gap="xs">
			<Text size="xs" c="dimmed">
				{t(
					format === "claude-code"
						? "details.sessionExport.descriptionClaudeCode"
						: "details.sessionExport.descriptionCodex",
				)}
			</Text>
			<Select
				label={t("details.sessionExport.destination")}
				data={destinations}
				value={destination}
				onChange={(value) => {
					setDestination((value as ExportDestination) ?? "download");
					setOutcome(null);
				}}
				allowDeselect={false}
				disabled={disabled}
			/>
			{destination === "device" ? (
				<Select
					label={t("details.sessionExport.device")}
					placeholder={t("details.sessionExport.selectDevice")}
					data={targets.devices.map((d) => ({ value: d.id, label: d.name }))}
					value={device?.id ?? null}
					onChange={(value) => {
						setDeviceId(value);
						setOutcome(null);
					}}
					allowDeselect={false}
					disabled={disabled}
					error={deviceId && !device ? t("details.sessionExport.deviceUnavailable") : undefined}
				/>
			) : null}
			{destination !== "download" ? (
				<TextInput
					label={t("details.sessionExport.root")}
					description={t("details.sessionExport.rootHint", { dir: configDir })}
					placeholder={configDir}
					value={root}
					onChange={(event) => setRoot(event.currentTarget.value)}
					disabled={disabled}
				/>
			) : null}
			<TextInput
				label={t("details.sessionExport.cwd")}
				description={t("details.sessionExport.cwdHint")}
				value={cwd}
				onChange={(event) => setCwd(event.currentTarget.value)}
				disabled={disabled}
			/>
			{format === "codex" ? (
				<TextInput
					label={t("details.sessionExport.modelProvider")}
					description={t("details.sessionExport.modelProviderHint")}
					value={modelProvider}
					onChange={(event) => setModelProvider(event.currentTarget.value)}
					disabled={disabled}
				/>
			) : (
				<Checkbox
					label={t("details.sessionExport.dropAllReasoning")}
					description={t("details.sessionExport.dropAllReasoningHint")}
					checked={dropAllReasoning}
					onChange={(event) => setDropAllReasoning(event.currentTarget.checked)}
					disabled={disabled}
				/>
			)}
			<Group justify="flex-end">
				<Button
					size="xs"
					leftSection={
						destination === "download" ? <IconDownload size={14} /> : <IconUpload size={14} />
					}
					loading={mutation.isPending}
					disabled={destination === "device" && !device}
					onClick={() => mutation.mutate()}
				>
					{t(
						destination === "download"
							? "details.sessionExport.actionDownload"
							: "details.sessionExport.actionWrite",
					)}
				</Button>
			</Group>

			{outcome ? (
				<Stack gap="xs">
					{!outcome.cwdExists ? (
						<Alert
							color="yellow"
							variant="light"
							icon={<IconAlertTriangle size={16} />}
							title={t("details.sessionExport.cwdMissingTitle")}
						>
							<Text size="xs">
								{t("details.sessionExport.cwdMissing", { cwd: outcome.cwd ?? "" })}
							</Text>
						</Alert>
					) : null}
					{outcome.placement ? (
						<Stack gap={2}>
							<Text size="xs" fw={500}>
								{t(
									outcome.placement.isArchive
										? "details.sessionExport.placeArchive"
										: "details.sessionExport.placeFile",
									{ dir: outcome.placement.configDir },
								)}
							</Text>
							{outcome.placement.relativePath ? (
								<Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
									{`${outcome.placement.configDir}/${outcome.placement.relativePath}`}
								</Code>
							) : null}
						</Stack>
					) : null}
					{outcome.paths.length > 0 ? (
						<Stack gap={2}>
							<Text size="xs" fw={500}>
								{t("details.sessionExport.writtenTo", { count: outcome.paths.length })}
							</Text>
							{outcome.paths.map((path) => (
								<Text key={path} size="xs" ff="monospace" style={{ wordBreak: "break-all" }}>
									{path}
								</Text>
							))}
						</Stack>
					) : null}
					{outcome.resumeCommand ? (
						<Stack gap={2}>
							<Text size="xs" fw={500}>
								{t("details.sessionExport.resumeCommand")}
							</Text>
							<Group gap="xs" wrap="nowrap" align="flex-start">
								<Code
									block
									style={{ flex: 1, minWidth: 0, whiteSpace: "pre-wrap", wordBreak: "break-all" }}
								>
									{outcome.resumeCommand}
								</Code>
								<CopyButton value={outcome.resumeCommand}>
									{({ copied, copy }) => (
										<Tooltip
											label={
												copied ? t("details.sessionExport.copied") : t("details.sessionExport.copy")
											}
										>
											<ActionIcon
												variant="subtle"
												onClick={copy}
												aria-label={t("details.sessionExport.copy")}
											>
												{copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
											</ActionIcon>
										</Tooltip>
									)}
								</CopyButton>
							</Group>
						</Stack>
					) : null}
					<SessionWarningsList warnings={outcome.warnings} direction="export" />
				</Stack>
			) : null}
		</Stack>
	);
}
