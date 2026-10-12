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
import { notifications } from "@mantine/notifications";
import type { SavedRecording } from "@shared/request-recording";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, isAbortError } from "../../lib/api";
import { saveBlobAsFile } from "../../lib/file-download";

export interface SaveDumpModalProps {
	narratorId: string;
	opened: boolean;
	onClose: () => void;
	saved: SavedRecording | null;
	onSaved: (recording: SavedRecording) => void;
	onDeleted: (saveId: string) => void;
}

/** Fetches only bounded summary pages while open; ZIP payloads never enter the query cache. */
export function SaveDumpModal({
	narratorId,
	opened,
	onClose,
	saved,
	onSaved,
	onDeleted,
}: SaveDumpModalProps) {
	const { t } = useTranslation("narrator");
	const qc = useQueryClient();
	const [deleteId, setDeleteId] = useState<string | null>(null);
	const [downloadingId, setDownloadingId] = useState<string | null>(null);
	const downloadRef = useRef<AbortController | null>(null);
	const openedRef = useRef(opened);
	const sessionRef = useRef(0);
	const saveGenerationRef = useRef(0);
	const savedIdRef = useRef(saved?.id);
	const ownerRef = useRef(narratorId);
	ownerRef.current = narratorId;
	const queryKey = ["request-recordings", narratorId];
	// Do not enable a cached cursor chain until this opening has reset it to page one.
	const [listOwner, setListOwner] = useState<string | null>(null);
	const resetLatest = useCallback(() => {
		// resetQueries synchronously cancels without reverting to an evicted page and
		// clears all pageParams before refetching. No awaited cancellation may later
		// overwrite a newer opening or externally published save.
		void qc.resetQueries({ queryKey: ["request-recordings", narratorId], exact: true });
	}, [qc, narratorId]);
	const records = useInfiniteQuery({
		queryKey,
		initialPageParam: undefined as string | undefined,
		queryFn: ({ pageParam, signal }) => api.listRequestRecordings(narratorId, pageParam, signal),
		getNextPageParam: (page) => page.nextCursor ?? undefined,
		maxPages: 4,
		enabled: opened && listOwner === narratorId,
	});
	const fail = (error: unknown) => {
		if (isAbortError(error)) return;
		notifications.show({
			title: t("saveDump.failed"),
			message: error instanceof Error ? error.message : String(error),
			color: "red",
		});
	};
	const save = useMutation({
		mutationFn: ({ narratorId: owner }: { generation: number; narratorId: string }) =>
			api.saveRequestRecording(owner, AbortSignal.timeout(30_000)),
		onSuccess: (recording, { generation, narratorId: owner }) => {
			if (
				openedRef.current &&
				ownerRef.current === owner &&
				recording.narratorId === owner &&
				generation === saveGenerationRef.current
			) {
				savedIdRef.current = recording.id;
				resetLatest();
				onSaved(recording);
			} else {
				// Published evidence stays valid, but an old completion must not cancel
				// a new session's list fetch or reset its current browsing position.
				void qc.invalidateQueries({ queryKey: ["request-recordings", owner], refetchType: "none" });
			}
		},
		onError: (error, { generation, narratorId: owner }) => {
			if (
				openedRef.current &&
				ownerRef.current === owner &&
				generation === saveGenerationRef.current
			)
				fail(error);
		},
	});
	const remove = useMutation({
		mutationFn: ({ id, narratorId: owner }: { id: string; session: number; narratorId: string }) =>
			api.deleteRequestRecording(owner, id),
		onSuccess: (_result, { id, session, narratorId: owner }) => {
			const current =
				openedRef.current && ownerRef.current === owner && session === sessionRef.current;
			if (current) setDeleteId(null);
			// Reconcile deletion by ID even after closing; it cannot replace a newer save result.
			onDeleted(id);
			qc.setQueryData<typeof records.data>(
				["request-recordings", owner],
				(previous) =>
					previous && {
						...previous,
						pages: previous.pages.map((page) => ({
							...page,
							items: page.items.filter((item) => item.id !== id),
						})),
					},
			);
			void qc.invalidateQueries({
				queryKey: ["request-recordings", owner],
				refetchType: current ? "active" : "none",
			});
		},
		onError: (error, { session, narratorId: owner }) => {
			if (openedRef.current && ownerRef.current === owner && session === sessionRef.current)
				fail(error);
		},
	});
	const resetSave = save.reset;
	const resetRemove = remove.reset;
	useEffect(() => {
		openedRef.current = opened;
		if (opened) {
			resetLatest();
			setListOwner(narratorId);
		} else {
			setListOwner(null);
		}
		setDeleteId(null);
		setDownloadingId(null);
		resetSave();
		resetRemove();
		return () => {
			openedRef.current = false;
			sessionRef.current++;
			saveGenerationRef.current++;
			downloadRef.current?.abort();
			void qc.cancelQueries(
				{ queryKey: ["request-recordings", narratorId], exact: true },
				{ revert: false, silent: true },
			);
		};
	}, [opened, resetSave, resetRemove, resetLatest, narratorId, qc]);
	useEffect(() => {
		// A command can publish a newer result while an in-modal save is still pending.
		if (savedIdRef.current !== saved?.id) {
			savedIdRef.current = saved?.id;
			saveGenerationRef.current++;
			if (saved?.narratorId === narratorId && openedRef.current) resetLatest();
		}
	}, [saved?.id, saved?.narratorId, narratorId, resetLatest]);
	const close = () => {
		// Invalidate immediately, even before the parent commits closed/unmounted state.
		openedRef.current = false;
		sessionRef.current++;
		saveGenerationRef.current++;
		downloadRef.current?.abort();
		void qc.cancelQueries({ queryKey, exact: true }, { revert: false, silent: true });
		onClose();
	};
	const download = async (recording: SavedRecording) => {
		if (
			downloadingId ||
			(downloadRef.current && !downloadRef.current.signal.aborted) ||
			ownerRef.current !== narratorId ||
			!openedRef.current
		)
			return;
		const controller = new AbortController();
		downloadRef.current = controller;
		setDownloadingId(recording.id);
		try {
			const { blob, fileName } = await api.downloadRequestRecording(
				narratorId,
				recording.id,
				AbortSignal.any([controller.signal, AbortSignal.timeout(120_000)]),
			);
			if (!controller.signal.aborted && openedRef.current && ownerRef.current === narratorId) {
				saveBlobAsFile(blob, fileName ?? `request-recording-${recording.id}.zip`);
			}
		} catch (error) {
			if (!controller.signal.aborted && openedRef.current && ownerRef.current === narratorId)
				fail(error);
		} finally {
			if (downloadRef.current === controller) {
				downloadRef.current = null;
				if (openedRef.current) setDownloadingId(null);
			}
		}
	};
	const copy = async (recording: SavedRecording) => {
		try {
			await navigator.clipboard.writeText(
				[
					`saveId: ${recording.id}`,
					`narratorId: ${recording.narratorId}`,
					`${t("saveDump.requestIds")}:`,
					...recording.requestIds,
					`${t("saveDump.apiRequestIds")}:`,
					...recording.apiRequestIds,
				].join("\n"),
			);
			notifications.show({ message: t("saveDump.copied"), color: "green" });
		} catch (error) {
			fail(error);
		}
	};
	const items = records.data?.pages.flatMap((page) => page.items) ?? [];
	const currentSaved = saved?.narratorId === narratorId ? saved : null;
	const display =
		currentSaved && !items.some((item) => item.id === currentSaved.id)
			? [currentSaved, ...items]
			: items;
	return (
		<Modal opened={opened} onClose={close} title={t("saveDump.title")} size="lg" centered>
			<Stack>
				<Alert color="orange">{t("saveDump.privacy")}</Alert>
				<Button
					loading={save.isPending}
					onClick={() => save.mutate({ generation: ++saveGenerationRef.current, narratorId })}
				>
					{t("saveDump.save")}
				</Button>
				<Button variant="default" onClick={resetLatest}>
					{t("saveDump.latest")}
				</Button>
				{currentSaved && (
					<Text c="green" size="sm">
						{t("saveDump.saved")} · {currentSaved.id}
					</Text>
				)}
				{records.isPending && <Loader size="sm" />}
				{records.isError && (
					<Alert color="red">
						<Text>{records.error.message}</Text>
						<Button variant="subtle" onClick={() => void records.refetch()}>
							{t("saveDump.retry")}
						</Button>
					</Alert>
				)}
				<ScrollArea.Autosize mah="55vh">
					<Stack gap="sm">
						{!records.isPending && !display.length && <Text c="dimmed">{t("saveDump.empty")}</Text>}
						{display.map((recording) => (
							<Paper key={recording.id} withBorder p="sm">
								<Stack gap={6}>
									<Text size="xs" c="dimmed">
										{recording.createdAt} · {recording.id}
									</Text>
									<Text size="sm">
										{t("saveDump.requestCount", {
											count: recording.requestCount,
											size: (recording.bytes / (1024 * 1024)).toFixed(2),
										})}
									</Text>
									<Group gap={4}>
										{recording.partial && <Badge color="orange">{t("saveDump.partial")}</Badge>}
										{recording.truncated && <Badge color="orange">{t("saveDump.truncated")}</Badge>}
										{recording.continuous && <Badge>{t("saveDump.continuous")}</Badge>}
									</Group>
									<Text size="xs" style={{ overflowWrap: "anywhere" }}>
										{t("saveDump.requestIds")}: {recording.requestIds.join(", ") || "—"}
									</Text>
									<Text size="xs" style={{ overflowWrap: "anywhere" }}>
										{t("saveDump.apiRequestIds")}: {recording.apiRequestIds.join(", ") || "—"}
									</Text>
									<Group>
										<Button size="xs" variant="default" onClick={() => void copy(recording)}>
											{t("saveDump.copy")}
										</Button>
										<Button
											size="xs"
											loading={downloadingId === recording.id}
											disabled={downloadingId !== null && downloadingId !== recording.id}
											onClick={() => void download(recording)}
										>
											{t("saveDump.download")}
										</Button>
										<Button
											size="xs"
											color="red"
											variant="subtle"
											onClick={() => setDeleteId(recording.id)}
										>
											{t("saveDump.delete")}
										</Button>
									</Group>
									{deleteId === recording.id && (
										<Alert color="red">
											<Text size="sm">{t("saveDump.confirmDelete")}</Text>
											<Group mt="xs">
												<Button size="xs" variant="default" onClick={() => setDeleteId(null)}>
													{t("saveDump.cancel")}
												</Button>
												<Button
													size="xs"
													color="red"
													loading={remove.isPending}
													onClick={() =>
														remove.mutate({
															id: recording.id,
															session: sessionRef.current,
															narratorId,
														})
													}
												>
													{t("saveDump.confirm")}
												</Button>
											</Group>
										</Alert>
									)}
								</Stack>
							</Paper>
						))}
					</Stack>
				</ScrollArea.Autosize>
				{records.hasNextPage && (
					<Button
						variant="default"
						loading={records.isFetchingNextPage}
						onClick={() => void records.fetchNextPage()}
					>
						{t("saveDump.more")}
					</Button>
				)}
			</Stack>
		</Modal>
	);
}
