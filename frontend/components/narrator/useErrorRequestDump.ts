import { api, isAbortError } from "@frontend/lib/api";
import { ApiError } from "@frontend/lib/api/client";
import { saveBlobAsFile } from "@frontend/lib/file-download";
import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/** GET never writes; create only a snapshot of this exact request if needed. */
export async function downloadErrorRequestDump(
	narratorId: string,
	requestId: string,
	signal: AbortSignal,
) {
	const detail = await api.getRequestRecordingDetail(narratorId, requestId, signal);
	signal.throwIfAborted();
	if (detail.narratorId !== narratorId || detail.requestId !== requestId) {
		throw new Error("Request recording identity mismatch");
	}
	const existing = detail.savedRecording;
	const saved =
		existing?.requestCount === 1 &&
		existing.requestIds.length === 1 &&
		existing.requestIds[0] === requestId
			? existing
			: await api.saveSpecificRequestRecording(narratorId, requestId, signal);
	signal.throwIfAborted();
	// Do not silently download an entire narrator window or another request.
	if (
		saved.narratorId !== narratorId ||
		saved.requestCount !== 1 ||
		saved.requestIds.length !== 1 ||
		saved.requestIds[0] !== requestId
	) {
		throw new Error("Request recording identity mismatch");
	}
	const result = await api.downloadRequestRecording(narratorId, saved.id, signal);
	signal.throwIfAborted();
	return result;
}

/** One active transfer per narrator; other request controls must not look actionable. */
export function errorRequestDownloadState(requestId: string | undefined, activeId: string | null) {
	return {
		downloadingDump: requestId !== undefined && activeId === requestId,
		downloadDumpDisabled: activeId !== null && activeId !== requestId,
	};
}
export function useErrorRequestDump(narratorId: string) {
	const { t } = useTranslation("narrator");
	const qc = useQueryClient();
	const [downloadingRequestId, setDownloadingRequestId] = useState<string | null>(null);
	const active = useRef<AbortController | null>(null);
	const owner = useRef(narratorId);
	const cancel = useCallback(() => {
		active.current?.abort();
		active.current = null;
		setDownloadingRequestId(null);
	}, []);
	useEffect(() => {
		owner.current = narratorId;
		setDownloadingRequestId(null);
		return () => {
			active.current?.abort();
			active.current = null;
		};
	}, [narratorId]);
	const download = useCallback(
		async (requestId: string) => {
			if (active.current || owner.current !== narratorId) return;
			const controller = new AbortController();
			active.current = controller;
			setDownloadingRequestId(requestId);
			try {
				const { blob, fileName } = await downloadErrorRequestDump(
					narratorId,
					requestId,
					AbortSignal.any([controller.signal, AbortSignal.timeout(120_000)]),
				);
				if (active.current !== controller || controller.signal.aborted) return;
				saveBlobAsFile(blob, fileName ?? `request-recording-${requestId}.zip`);
				void qc.invalidateQueries({ queryKey: ["request-recordings", narratorId] });
			} catch (error) {
				if (active.current !== controller || controller.signal.aborted || isAbortError(error))
					return;
				notifications.show({
					title: t("errorRequest.failed"),
					message:
						error instanceof ApiError && error.status === 404
							? t("errorRequest.unavailable")
							: error instanceof Error
								? error.message
								: String(error),
					color: "red",
				});
			} finally {
				if (active.current === controller) {
					active.current = null;
					setDownloadingRequestId(null);
				}
			}
		},
		[narratorId, qc, t],
	);
	return { download, cancel, downloadingRequestId };
}
