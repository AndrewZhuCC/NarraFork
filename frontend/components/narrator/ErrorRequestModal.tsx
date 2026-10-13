import {
	Alert,
	Badge,
	Button,
	Group,
	Loader,
	Modal,
	Paper,
	ScrollArea,
	Stack,
	Text,
} from "@mantine/core";
import type { ApiRequestDiagnostics } from "@shared/agent-protocol/types";
import type { RequestRecordingDetail } from "@shared/request-recording";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, api } from "../../lib/api";
import { copyTextToClipboard } from "../../lib/clipboard";

export interface ErrorRequestTarget {
	requestId: string;
	errorText: string;
	diagnostics?: ApiRequestDiagnostics;
}

export interface ErrorRequestModalProps {
	narratorId: string;
	target: ErrorRequestTarget | null;
	onClose: () => void;
	onDownload: () => void;
	downloading: boolean;
	/** A different request's transfer is active; this modal must not claim it as its own. */
	downloadBlocked?: boolean;
}

const MAX_PREVIEW_CHARS = 32 * 1024;

function Field({ label, value }: { label: string; value?: string | number | null }) {
	if (value === undefined || value === null || value === "") return null;
	return (
		<Text size="sm" style={{ overflowWrap: "anywhere" }}>
			<Text span fw={500}>
				{label}:{" "}
			</Text>
			{String(value).slice(0, MAX_PREVIEW_CHARS)}
		</Text>
	);
}

function Preview({
	label,
	value,
	truncated = false,
}: {
	label: string;
	value: string;
	truncated?: boolean;
}) {
	const { t } = useTranslation("narrator");
	return (
		<Paper withBorder p="sm">
			<Stack gap="xs">
				<Text size="sm" fw={500}>
					{label}
				</Text>
				{(truncated || value.length > MAX_PREVIEW_CHARS) && (
					<Text size="xs" c="orange">
						{t("errorRequest.previewTruncated")}
					</Text>
				)}
				<Text
					component="pre"
					size="xs"
					m={0}
					style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
				>
					{value.slice(0, MAX_PREVIEW_CHARS) || "—"}
				</Text>
			</Stack>
		</Paper>
	);
}

interface DetailState {
	key: string;
	pending: boolean;
	detail?: RequestRecordingDetail;
	error?: string;
	unavailable?: boolean;
}

/** Bounded previews are fetched only for the selected request and never enter a shared cache. */
export function ErrorRequestModal({
	narratorId,
	target,
	onClose,
	onDownload,
	downloading,
	downloadBlocked = false,
}: ErrorRequestModalProps) {
	const { t } = useTranslation("narrator");
	const requestId = target?.requestId;
	const [state, setState] = useState<DetailState | null>(null);
	const [retry, setRetry] = useState(0);
	const key = JSON.stringify([narratorId, requestId, retry]);
	const [copied, setCopied] = useState(false);
	const [copyError, setCopyError] = useState<string | null>(null);
	const generation = useRef(0);
	const controllerRef = useRef<AbortController | null>(null);

	useEffect(() => {
		const session = ++generation.current;
		setCopied(false);
		setCopyError(null);
		if (!requestId) {
			setState(null);
			return;
		}
		const controller = new AbortController();
		controllerRef.current = controller;
		setState({ key, pending: true });
		const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(30_000)]);
		void api.getRequestRecordingDetail(narratorId, requestId, signal).then(
			(detail) => {
				if (!controller.signal.aborted && generation.current === session) {
					setState({ key, pending: false, detail });
				}
			},
			(error: unknown) => {
				if (controller.signal.aborted || generation.current !== session) return;
				setState({
					key,
					pending: false,
					unavailable: error instanceof ApiError && error.status === 404,
					error: error instanceof Error ? error.message : String(error),
				});
			},
		);
		return () => {
			generation.current++;
			controller.abort();
			if (controllerRef.current === controller) controllerRef.current = null;
		};
	}, [narratorId, requestId, key]);

	const current = state?.key === key ? state : null;
	const detail = current?.detail;
	const diagnostics = target?.diagnostics;
	const apiRequestId = detail?.apiRequestId ?? detail?.apiRequest?.id ?? diagnostics?.apiRequestId;
	const providerRequestId = diagnostics?.providerRequestId ?? diagnostics?.requestId;
	const close = () => {
		// Invalidate before notifying the parent: onClose can synchronously settle old work.
		generation.current++;
		controllerRef.current?.abort();
		setState(null);
		onClose();
	};
	const copy = async () => {
		if (!target) return;
		const session = generation.current;
		try {
			await copyTextToClipboard(
				[
					`narratorId: ${narratorId}`,
					`requestId: ${target.requestId}`,
					...(apiRequestId ? [`apiRequestId: ${apiRequestId}`] : []),
					...(providerRequestId ? [`providerRequestId: ${providerRequestId}`] : []),
					...(detail?.savedRecording ? [`saveId: ${detail.savedRecording.id}`] : []),
				].join("\n"),
			);
			if (session === generation.current) {
				setCopied(true);
				setCopyError(null);
			}
		} catch (error) {
			if (session === generation.current) {
				setCopyError(error instanceof Error ? error.message : String(error));
			}
		}
	};

	return (
		<Modal
			opened={target !== null}
			onClose={close}
			title={t("errorRequest.title")}
			size="lg"
			centered
		>
			{target && (
				<Stack>
					<Alert color="orange">{t("saveDump.privacy")}</Alert>
					<Group>
						<Button variant="default" onClick={() => void copy()}>
							{t(copied ? "errorRequest.copied" : "errorRequest.copy")}
						</Button>
						<Button loading={downloading} disabled={downloadBlocked} onClick={onDownload}>
							{t("errorRequest.download")}
						</Button>
					</Group>
					{downloadBlocked && (
						<Text size="sm" c="dimmed">
							{t("errorRequest.otherDownload")}
						</Text>
					)}
					{copyError && <Alert color="red">{copyError}</Alert>}
					{(!current || current.pending) && (
						<Loader size="sm" aria-label={t("errorRequest.loading")} />
					)}
					{current?.error && (
						<Alert
							color={current.unavailable ? "orange" : "red"}
							title={t(current.unavailable ? "errorRequest.unavailable" : "errorRequest.failed")}
						>
							{!current.unavailable && <Text size="sm">{current.error}</Text>}
							<Button variant="subtle" onClick={() => setRetry((value) => value + 1)}>
								{t("errorRequest.retry")}
							</Button>
						</Alert>
					)}
					<ScrollArea.Autosize mah="55vh">
						<Stack gap="sm">
							<Stack gap={4}>
								<Field label={t("errorRequest.requestId")} value={target.requestId} />
								<Field label={t("errorRequest.apiRequestId")} value={apiRequestId} />
								<Field label={t("errorRequest.providerRequestId")} value={providerRequestId} />
								<Field
									label={t("errorRequest.provider")}
									value={detail?.provider ?? diagnostics?.provider}
								/>
								<Field
									label={t("errorRequest.model")}
									value={detail?.model ?? diagnostics?.model}
								/>
								<Field label={t("errorRequest.startedAt")} value={detail?.startedAt} />
								<Field label={t("errorRequest.endedAt")} value={detail?.endedAt} />
								<Field label={t("errorRequest.status")} value={detail?.status} />
								<Field
									label={t("errorRequest.duration")}
									value={
										detail?.apiRequest?.durationMs != null
											? `${detail.apiRequest.durationMs} ms`
											: undefined
									}
								/>
								<Field
									label={t("errorRequest.ttft")}
									value={
										detail?.apiRequest?.ttftMs != null
											? `${detail.apiRequest.ttftMs} ms`
											: undefined
									}
								/>
							</Stack>
							<Group gap="xs">
								{detail?.partial && <Badge color="orange">{t("saveDump.partial")}</Badge>}
								{detail?.truncated && <Badge color="orange">{t("saveDump.truncated")}</Badge>}
							</Group>
							<Preview
								label={t("errorRequest.error")}
								value={[target.errorText, detail?.error, detail?.apiRequest?.errorMessage]
									.filter((value, index, all) => value && all.indexOf(value) === index)
									.join("\n")}
							/>
							{diagnostics && (
								<Paper withBorder p="sm">
									<Stack gap="xs">
										<Text fw={500} size="sm">
											{t("errorRequest.diagnostics")}
										</Text>
										<Field label={t("errorRequest.status")} value={diagnostics.statusCode} />
										<Field label={t("errorRequest.code")} value={diagnostics.code} />
										<Field label={t("errorRequest.source")} value={diagnostics.source} />
										<Field label={t("errorRequest.phase")} value={diagnostics.phase} />
										<Field label={t("errorRequest.reason")} value={diagnostics.reason} />
										<Field label={t("errorRequest.errorType")} value={diagnostics.errorType} />
										<Field label={t("errorRequest.error")} value={diagnostics.message} />
										<Field label={t("errorRequest.cause")} value={diagnostics.cause} />
										{diagnostics.responseHeaders && (
											<Preview
												label={t("errorRequest.responseHeaders")}
												value={JSON.stringify(diagnostics.responseHeaders, null, 2)}
											/>
										)}
										{diagnostics.responseSnippet && (
											<Preview
												label={t("errorRequest.responseSnippet")}
												value={diagnostics.responseSnippet}
											/>
										)}
									</Stack>
								</Paper>
							)}
							{detail && (
								<>
									<Preview
										label={t("errorRequest.requestPreview")}
										value={detail.requestPreview}
										truncated={detail.previewTruncated.request}
									/>
									<Preview
										label={t("errorRequest.responsePreview")}
										value={detail.responsePreview}
										truncated={detail.previewTruncated.response}
									/>
								</>
							)}
						</Stack>
					</ScrollArea.Autosize>
				</Stack>
			)}
		</Modal>
	);
}
