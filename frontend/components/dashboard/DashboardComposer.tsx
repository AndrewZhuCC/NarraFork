import { ActionIcon, Box, Button, Group, Text, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { composerDocumentText, parseComposerDocument } from "@shared/composer-document";
import { IconAdjustmentsHorizontal, IconFolder, IconPaperclip } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { lazy, Suspense, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { useCreateNarrator } from "../../hooks/useNarrator";
import { useFileSystemCapability } from "../../hooks/usePlatform";
import { addRecentTab } from "../../hooks/useRecentTabs";
import { api, isAbortError } from "../../lib/api";
import { FOLLOW_DEFAULT_MODEL } from "../../lib/constants";
import { DirectoryBrowserModal } from "../common/DirectoryPicker";
import type { CreateNarratorResult } from "../narrator/CreateNarratorModal";
import { hasSendableComposerContent } from "../narrator/composer/composer-send-gate";
import { trimFileReferenceInput } from "../narrator/composer/file-reference-input";
import {
	NarratorComposer,
	type NarratorComposerHandle,
} from "../narrator/composer/NarratorComposer";
import { UploadProgressBar } from "../narrator/interaction/UploadProgressBar";
import { useComposerAttachments } from "../narrator/interaction/use-composer-attachments";
import { useComposerFileIngest } from "../narrator/interaction/use-composer-file-ingest";
import {
	type NarratorSendingState,
	snapshotComposerFiles,
} from "../narrator/interaction/use-narrator-send";
import { ModelPickerMenu } from "../narrator/model/ModelPickerMenu";
import { directoryLabel } from "./dashboard-sessions";

const CreateNarratorModal = lazy(() =>
	import("../narrator/CreateNarratorModal").then((m) => ({ default: m.CreateNarratorModal })),
);

/**
 * Local key for the dashboard composer's draft, input history and staged
 * attachments. It names no narrator: the composer runs in draft mode, so nothing
 * is ever sent to the server under it.
 */
export const DASHBOARD_COMPOSER_KEY = "dashboard-composer";
const COMPOSER_PREFS_KEY = "narrafork_dashboard_composer";
const SETTINGS_QUERY_GC_TIME_MS = 60_000;

interface ComposerPrefs {
	cwd?: string;
	model?: string;
}

function readPrefs(): ComposerPrefs {
	try {
		const raw = localStorage.getItem(COMPOSER_PREFS_KEY);
		const parsed = raw ? JSON.parse(raw) : null;
		return parsed && typeof parsed === "object" ? parsed : {};
	} catch {
		return {};
	}
}

function writePrefs(prefs: ComposerPrefs) {
	try {
		localStorage.setItem(COMPOSER_PREFS_KEY, JSON.stringify(prefs));
	} catch {
		// Private mode / blocked storage: the selection just isn't remembered.
	}
}

/**
 * The dashboard's entry point: the narrator composer (slash commands, skills,
 * pasted images and files, input history) without a session behind it. Sending
 * creates a standalone session in the chosen directory with the chosen model,
 * delivers the message as its first turn, and opens it.
 */
export function DashboardComposer() {
	const { t } = useTranslation("dashboard");
	const { t: tn } = useTranslation("narrator");
	const { t: tns } = useTranslation("narrators");
	const navigate = useNavigate();
	const createNarrator = useCreateNarrator();
	const fsCapability = useFileSystemCapability();
	const { data: currentUser } = useCurrentUser();
	const currentUserId = currentUser?.id ? String(currentUser.id) : null;
	const { data: settings } = useQuery({
		queryKey: ["settings"],
		queryFn: api.getSettings,
		gcTime: SETTINGS_QUERY_GC_TIME_MS,
	});
	const defaultProjectDir: string = settings?.paths?.defaultProjectDir ?? "";

	const [prefs] = useState(readPrefs);
	// `null` = never chosen on this device: follow the instance default directory.
	const [chosenCwd, setChosenCwd] = useState<string | null>(prefs.cwd || null);
	const [model, setModel] = useState(prefs.model || FOLLOW_DEFAULT_MODEL);
	const cwd = chosenCwd ?? defaultProjectDir;
	const [browseOpen, setBrowseOpen] = useState(false);
	const [advancedOpen, setAdvancedOpen] = useState(false);
	const [hasText, setHasText] = useState(false);
	const [sendingState, setSendingState] = useState<NarratorSendingState | null>(null);
	const composerRef = useRef<NarratorComposerHandle>(null);
	const fileInputRef = useRef<HTMLInputElement>(null);
	const sendingRef = useRef(false);
	const sendAbortRef = useRef<AbortController | null>(null);
	// A session created by a send whose message then failed. Retrying reuses it
	// instead of leaving an empty session behind per attempt.
	const pendingSessionRef = useRef<CreateNarratorResult | null>(null);

	const {
		attachedImages,
		attachedTextFiles,
		attachmentsHydrating,
		attachmentsRestoreFailed,
		retryAttachmentRestore,
		restoredAttachmentResources,
		setIsDragging,
		dragCounterRef,
		updateAttachedImages,
		updateAttachedTextFiles,
		reconcileDocumentAssets,
		clearAttachedFilesAndDraft,
	} = useComposerAttachments({
		narratorId: DASHBOARD_COMPOSER_KEY,
		currentUserId,
		sendingRef,
		t: tn,
	});
	const { handleFileInputChange, handleComposerPasteImages, dropZoneProps, ingestingRef } =
		useComposerFileIngest({
			narratorId: DASHBOARD_COMPOSER_KEY,
			sendingRef,
			updateAttachedImages,
			updateAttachedTextFiles,
			setIsDragging,
			dragCounterRef,
			captureInsertion: (origin) => composerRef.current?.captureAttachmentInsertion(origin),
			onAcceptedFiles: (files, bookmark) =>
				composerRef.current?.insertAcceptedFiles(files, bookmark) ?? false,
			t: tn,
		});
	const hasAttachments = attachedImages.length + attachedTextFiles.length > 0;

	const selectCwd = (path: string) => {
		pendingSessionRef.current = null;
		setChosenCwd(path);
		writePrefs({ cwd: path, model });
	};
	const selectModel = (value: string) => {
		pendingSessionRef.current = null;
		setModel(value);
		writePrefs({ cwd: chosenCwd ?? undefined, model: value });
	};

	const openSession = (session: CreateNarratorResult) => {
		void addRecentTab({
			type: "narrator",
			id: session.id,
			title: session.title,
			subtitle: session.cwd,
			status: session.status,
		});
		navigate({ to: "/narrators/$narratorId", params: { narratorId: session.id } });
	};

	const send = async () => {
		const composer = composerRef.current;
		if (!composer || sendingRef.current || ingestingRef.current) return;
		const rawText = composer.getText();
		const liveDocument = composer.getDocument();
		const document = liveDocument ? parseComposerDocument(liveDocument) : undefined;
		const { text, fileReferences, composerDocument } = document
			? { ...composerDocumentText(document), composerDocument: document }
			: {
					...trimFileReferenceInput({
						text: rawText,
						fileReferences: composer.getFileReferences(),
					}),
					composerDocument: undefined,
				};
		const files = snapshotComposerFiles(composerDocument, attachedImages, attachedTextFiles);
		if (!files || composer.hasMissingAssets()) {
			notifications.show({
				color: "red",
				title: tn("sendFailed"),
				message: tn("composerMissingAssets"),
			});
			return;
		}
		if (
			!hasSendableComposerContent({
				text: rawText,
				imageCount: files.images.length,
				textFileCount: files.textFiles.length,
				fileReferenceCount: fileReferences.length,
			})
		)
			return;

		sendingRef.current = true;
		const attachmentCount = files.images.length + files.textFiles.length;
		const abort = new AbortController();
		sendAbortRef.current = abort;
		setSendingState({
			attachmentCount,
			progress: attachmentCount > 0 ? 0 : null,
			canCancel: attachmentCount > 0,
		});
		let lastPercent = -1;
		try {
			let session = pendingSessionRef.current;
			if (!session) {
				// Unchosen → let the server resolve (and create) the default project directory.
				const data = await createNarrator.mutateAsync({
					...(chosenCwd ? { cwd: chosenCwd } : {}),
					model,
				});
				session = {
					id: data.id,
					title: data.title || tns("newNarrator"),
					cwd: data.cwd || cwd,
					status: data.status || "idle",
				};
				pendingSessionRef.current = session;
			}
			const args: Parameters<typeof api.sendNarratorMessage> = [
				session.id,
				text,
				files.images.length > 0 ? files.images : undefined,
				files.textFiles.length > 0 ? files.textFiles : undefined,
				undefined,
				(fraction) => {
					// Same throttle as the narrator page: re-render on whole-percent steps only.
					const percent = Math.min(100, Math.max(0, Math.round(fraction * 100)));
					if (percent === lastPercent) return;
					lastPercent = percent;
					setSendingState((state) => (state ? { ...state, progress: fraction } : state));
				},
				abort.signal,
			];
			if (fileReferences.length > 0) args[7] = fileReferences;
			if (composerDocument) args[10] = { composerDocument };
			await api.sendNarratorMessage(...args);
			composer.noteSent(text, fileReferences, composerDocument);
			composer.clearTextAndDraft();
			clearAttachedFilesAndDraft();
			pendingSessionRef.current = null;
			openSession(session);
		} catch (error) {
			if (isAbortError(error)) {
				notifications.show({ message: tn("sendCancelled"), color: "gray", autoClose: 2000 });
			} else if (pendingSessionRef.current) {
				// Creation errors are toasted by the global mutation handler.
				notifications.show({
					color: "red",
					title: t("composer.sendFailed"),
					message: error instanceof Error ? error.message : String(error),
				});
			}
		} finally {
			sendingRef.current = false;
			sendAbortRef.current = null;
			setSendingState(null);
		}
	};

	const sending = sendingState != null;

	return (
		<Box {...dropZoneProps}>
			<UploadProgressBar
				sendingState={sendingState}
				cancelSending={() => sendAbortRef.current?.abort()}
			/>
			<Box>
				<input
					ref={fileInputRef}
					type="file"
					multiple
					style={{ display: "none" }}
					onChange={handleFileInputChange}
					disabled={sending}
				/>
				<NarratorComposer
					ref={composerRef}
					narratorId={DASHBOARD_COMPOSER_KEY}
					draft={{ cwd: cwd || null }}
					adaptive={false}
					sendingRef={sendingRef}
					sending={sending}
					permEnterActive={false}
					hasAttachments={hasAttachments}
					enterMode="turn"
					ctrlEnterMode="turn"
					onSendWithMode={() => send()}
					onTextFlagsChange={setHasText}
					onPasteImages={handleComposerPasteImages}
					attachedImages={attachedImages}
					attachedTextFiles={attachedTextFiles}
					attachmentsHydrating={attachmentsHydrating}
					attachmentsRestoreFailed={attachmentsRestoreFailed}
					onRetryAttachments={retryAttachmentRestore}
					restoredAttachmentResources={restoredAttachmentResources}
					onDocumentAssetsChange={reconcileDocumentAssets}
					onAttachmentsChange={(images, textFiles) => {
						updateAttachedImages(images);
						updateAttachedTextFiles(textFiles);
					}}
					controls={
						<Tooltip label={tn("attachFile")}>
							<ActionIcon
								aria-label={tn("attachFile")}
								variant="subtle"
								color="gray"
								disabled={sending}
								onMouseDown={(event) => event.preventDefault()}
								onClick={() => {
									composerRef.current?.captureAttachmentSelection();
									fileInputRef.current?.click();
								}}
							>
								<IconPaperclip size={18} />
							</ActionIcon>
						</Tooltip>
					}
					actions={
						<Button
							onClick={() => void send()}
							loading={sending}
							disabled={!hasText && !hasAttachments}
						>
							{t("composer.start")}
						</Button>
					}
				/>
			</Box>

			<Group gap="xs" mt="xs" wrap="wrap">
				<Tooltip
					label={fsCapability.browse.supported ? cwd || t("composer.homeDirectory") : undefined}
					disabled={!fsCapability.browse.supported && !cwd}
				>
					<Button
						variant="default"
						size="xs"
						leftSection={<IconFolder size={14} />}
						disabled={!fsCapability.browse.supported}
						onClick={() => setBrowseOpen(true)}
						styles={{ label: { maxWidth: 260 } }}
					>
						<Text span size="xs" truncate>
							{cwd ? directoryLabel(cwd) : t("composer.homeDirectory")}
						</Text>
					</Button>
				</Tooltip>
				<Box style={{ minWidth: 0, maxWidth: 280 }}>
					<ModelPickerMenu value={model} onChange={selectModel} />
				</Box>
				<Tooltip label={t("composer.advanced")}>
					<ActionIcon
						variant="subtle"
						color="gray"
						aria-label={t("composer.advanced")}
						onClick={() => setAdvancedOpen(true)}
					>
						<IconAdjustmentsHorizontal size={16} />
					</ActionIcon>
				</Tooltip>
			</Group>

			<DirectoryBrowserModal
				opened={browseOpen}
				onClose={() => setBrowseOpen(false)}
				initialPath={cwd || undefined}
				onSelect={(path) => {
					selectCwd(path);
					setBrowseOpen(false);
				}}
			/>

			{advancedOpen && (
				<Suspense fallback={null}>
					<CreateNarratorModal
						opened
						onClose={() => setAdvancedOpen(false)}
						initialCwd={cwd || undefined}
						onCreated={openSession}
					/>
				</Suspense>
			)}
		</Box>
	);
}
