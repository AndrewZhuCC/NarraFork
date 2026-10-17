import { isAbortError } from "@frontend/lib/api";
import { ApiError } from "@frontend/lib/api/client";
import { saveBlobAsFile } from "@frontend/lib/file-download";
import { Alert, Button, Code, Group, Modal, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconDownload, IconInfoCircle } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { downloadErrorRequestDump } from "../useErrorRequestDump";
import type { LeakedToolEvent } from "../useNarratorPanelWS";

interface LeakedToolCallModalProps {
	narratorId: string;
	event: LeakedToolEvent | null;
	onClose: () => void;
}

/**
 * Dialog shown when the streaming XML tool-call capture failed for this turn:
 * - `recovered`: the post-turn safety net extracted and executed the tool, but the raw
 *   data is worth downloading to investigate why streaming capture missed it.
 * - `unrecovered`: leaked `<invoke>` text remained that could not be parsed; a tool may
 *   not have executed.
 *
 * Either way the server pins the attempt in the request recorder, so the download below
 * is the same evidence ZIP as any other request recording.
 */
export function LeakedToolCallModal({ narratorId, event, onClose }: LeakedToolCallModalProps) {
	const { t } = useTranslation("narrator");
	const [downloading, setDownloading] = useState(false);

	const handleDownload = async () => {
		const requestId = event?.recordingRequestId;
		if (!requestId) return;
		setDownloading(true);
		try {
			const { blob, fileName } = await downloadErrorRequestDump(
				narratorId,
				requestId,
				AbortSignal.timeout(120_000),
			);
			saveBlobAsFile(blob, fileName ?? `request-recording-${requestId}.zip`);
		} catch (error) {
			if (isAbortError(error)) return;
			notifications.show({
				title: t("leakedToolDownloadFailedTitle"),
				message:
					error instanceof ApiError && error.status === 404
						? t("errorRequest.unavailable")
						: error instanceof Error
							? error.message
							: String(error),
				color: "red",
			});
		} finally {
			setDownloading(false);
		}
	};

	const unrecovered = event?.phase === "unrecovered";

	return (
		<Modal
			opened={!!event}
			onClose={onClose}
			title={unrecovered ? t("leakedToolUnrecoveredTitle") : t("leakedToolRecoveredTitle")}
			size="lg"
		>
			{event && (
				<Stack gap="md">
					<Alert
						variant="light"
						color={unrecovered ? "red" : "yellow"}
						icon={unrecovered ? <IconAlertTriangle size={18} /> : <IconInfoCircle size={18} />}
					>
						{unrecovered ? t("leakedToolUnrecoveredBody") : t("leakedToolRecoveredBody")}
					</Alert>

					{event.toolNames && event.toolNames.length > 0 && (
						<Text size="sm">
							{t("leakedToolRecoveredTools", { tools: event.toolNames.join(", ") })}
						</Text>
					)}

					{event.snippet && (
						<Stack gap={4}>
							<Text size="sm" c="dimmed">
								{t("leakedToolSnippetLabel")}
							</Text>
							<Code block style={{ maxHeight: 200, overflow: "auto", whiteSpace: "pre-wrap" }}>
								{event.snippet}
							</Code>
						</Stack>
					)}

					<Group justify="flex-end">
						<Button variant="default" onClick={onClose}>
							{t("leakedToolDismiss")}
						</Button>
						<Button
							leftSection={<IconDownload size={16} />}
							loading={downloading}
							disabled={!event.recordingRequestId}
							onClick={handleDownload}
						>
							{t("leakedToolDownload")}
						</Button>
					</Group>
				</Stack>
			)}
		</Modal>
	);
}
