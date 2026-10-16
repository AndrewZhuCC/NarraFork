import { notifications } from "@mantine/notifications";
import type { ComposerAsset } from "@shared/composer-document";
import type React from "react";
import { type SetStateAction, useCallback, useEffect, useRef, useState } from "react";
import {
	clearDraftImageAttachments,
	getComposerAssetId,
	getDraftImageAttachmentKey,
	loadDraftImageAttachments,
	saveDraftImageAttachments,
} from "../composer/draft-image-attachments";

export interface UseComposerAttachmentsOptions {
	narratorId: string;
	/** Current user id (draft records are keyed per user+narrator); null when unknown. */
	currentUserId: string | null;
	/** Shared send-in-flight flag — the autosave effect skips while a send is running. */
	sendingRef: React.RefObject<boolean>;
	/** narrator translation fn (namespace "narrator"). */
	t: (key: string, opts?: Record<string, unknown>) => string;
}

export interface UseComposerAttachmentsResult {
	/** Stable manifest for legacy array consumers adopting inline resource bindings. */
	composerAssets: ComposerAsset[];
	attachedImages: File[];
	attachedTextFiles: File[];
	/** Live mirror of `attachedImages` for callbacks registered once (user-chat bridge). */
	attachedImagesRef: React.RefObject<File[]>;
	/** Live mirror of `attachedTextFiles`. */
	attachedTextFilesRef: React.RefObject<File[]>;
	isDragging: boolean;
	setIsDragging: React.Dispatch<React.SetStateAction<boolean>>;
	/** Drag-enter/leave depth counter so nested dragenter/leave don't flicker the overlay. */
	dragCounterRef: React.RefObject<number>;
	/** Mutate images through this (bumps the shared draft version counter). */
	updateAttachedImages: (next: SetStateAction<File[]>) => void;
	/** Text-file counterpart of `updateAttachedImages`. */
	updateAttachedTextFiles: (next: SetStateAction<File[]>) => void;
	/** Declare document-owned assets and remove unreferenced local blobs, including late hydrates. */
	reconcileDocumentAssets: (ids: readonly string[]) => void;
	/** Clear on-screen attachments for an in-flight send WITHOUT touching the stored draft. */
	hideAttachedFilesForSend: () => void;
	/** Clear attachments AND the persisted draft record. */
	clearAttachedFilesAndDraft: () => void;
}

/**
 * Pending composer attachments (images + text files) and their IndexedDB draft
 * persistence. Extracted verbatim from NarratorPanel.
 *
 * Both kinds share ONE set of bookkeeping refs (local-version / save-seq /
 * hydrated-key) because they are stored in a SINGLE record per (user, narrator);
 * two independent version counters would race on that one record. All mutations
 * must go through `updateAttachedImages` / `updateAttachedTextFiles` so the
 * shared version counter bumps — otherwise an in-flight hydrate could overwrite a
 * file the user just attached. The send flow (hide/clear/restore) and drag
 * handlers live in NarratorPanel and consume the exports here.
 */
export function useComposerAttachments(
	options: UseComposerAttachmentsOptions,
): UseComposerAttachmentsResult {
	const { narratorId, currentUserId, sendingRef, t } = options;
	const documentAssetsRef = useRef({
		narratorId,
		currentUserId,
		allowedIds: null as Set<string> | null,
	});
	// Reset ownership during render, before a child can declare the new document
	// and before an old IndexedDB promise can apply to the new owner.
	if (
		documentAssetsRef.current.narratorId !== narratorId ||
		documentAssetsRef.current.currentUserId !== currentUserId
	) {
		documentAssetsRef.current = { narratorId, currentUserId, allowedIds: null };
	}

	const [attachedImages, setAttachedImages] = useState<File[]>([]);
	const attachedImagesRef = useRef<File[]>(attachedImages);
	attachedImagesRef.current = attachedImages;
	const attachmentDraftHydratedKeyRef = useRef<string | null>(null);
	const attachmentDraftSaveSeqRef = useRef(0);
	const attachmentDraftLocalVersionRef = useRef(0);
	const [attachedTextFiles, setAttachedTextFiles] = useState<File[]>([]);
	// Mirrors `attachedTextFiles` for the same reason `attachedImagesRef` exists:
	// callers registered once (the user-chat forward bridge) must read the CURRENT
	// attachments without re-registering on every change.
	const attachedTextFilesRef = useRef<File[]>(attachedTextFiles);
	attachedTextFilesRef.current = attachedTextFiles;
	const [isDragging, setIsDragging] = useState(false);
	const dragCounterRef = useRef(0);
	const warnDraftAttachmentsPersistenceFailure = useCallback((action: string, err: unknown) => {
		if (import.meta.env.DEV) {
			console.warn(`[NarratorPanel] Failed to ${action} draft attachments:`, err);
		}
	}, []);
	const updateAttachedImages = useCallback((next: SetStateAction<File[]>) => {
		attachmentDraftLocalVersionRef.current++;
		const resolved = typeof next === "function" ? next(attachedImagesRef.current) : next;
		for (const file of resolved) {
			const id = getComposerAssetId(file);
			// New local uploads are allowed before the document adopts their ids.
			documentAssetsRef.current.allowedIds?.add(id);
		}
		attachedImagesRef.current = resolved;
		setAttachedImages(resolved);
	}, []);
	/**
	 * Text-file counterpart of `updateAttachedImages`.
	 *
	 * Every mutation of `attachedTextFiles` must go through this rather than the
	 * raw setter: it is what bumps the shared local-version counter, without which
	 * an in-flight hydrate would overwrite a file the user just attached.
	 */
	const updateAttachedTextFiles = useCallback((next: SetStateAction<File[]>) => {
		attachmentDraftLocalVersionRef.current++;
		const resolved = typeof next === "function" ? next(attachedTextFilesRef.current) : next;
		for (const file of resolved) {
			const id = getComposerAssetId(file);
			documentAssetsRef.current.allowedIds?.add(id);
		}
		attachedTextFilesRef.current = resolved;
		setAttachedTextFiles(resolved);
	}, []);
	const reconcileDocumentAssets = useCallback((ids: readonly string[]) => {
		const allowed = new Set(ids);
		documentAssetsRef.current.allowedIds = allowed;
		// This is a document ownership declaration, NOT a local edit. Bumping the
		// local version here would discard every valid blob in an in-flight hydrate.
		const images = attachedImagesRef.current.filter((file) =>
			allowed.has(getComposerAssetId(file)),
		);
		const textFiles = attachedTextFilesRef.current.filter((file) =>
			allowed.has(getComposerAssetId(file)),
		);
		if (images.length !== attachedImagesRef.current.length) {
			attachedImagesRef.current = images;
			setAttachedImages(images);
		}
		if (textFiles.length !== attachedTextFilesRef.current.length) {
			attachedTextFilesRef.current = textFiles;
			setAttachedTextFiles(textFiles);
		}
	}, []);
	const persistCurrentDraftAttachments = useCallback(
		(targetUserId: string, targetNarratorId: string) => {
			const seq = ++attachmentDraftSaveSeqRef.current;
			void saveDraftImageAttachments(
				targetUserId,
				targetNarratorId,
				attachedImagesRef.current,
				attachedTextFilesRef.current,
			).catch((err) => {
				if (seq === attachmentDraftSaveSeqRef.current) {
					warnDraftAttachmentsPersistenceFailure("save", err);
				}
			});
		},
		[warnDraftAttachmentsPersistenceFailure],
	);
	/**
	 * Clear the on-screen attachments for an in-flight send, WITHOUT touching the
	 * stored draft — a failed send restores them, and the record has to still be
	 * there for that to mean anything.
	 */
	const hideAttachedFilesForSend = useCallback(() => {
		attachmentDraftLocalVersionRef.current++;
		attachedImagesRef.current = [];
		attachedTextFilesRef.current = [];
		setAttachedImages([]);
		setAttachedTextFiles([]);
	}, []);
	const clearAttachedFilesAndDraft = useCallback(() => {
		attachmentDraftLocalVersionRef.current++;
		attachedImagesRef.current = [];
		attachedTextFilesRef.current = [];
		setAttachedImages([]);
		setAttachedTextFiles([]);
		if (!currentUserId) return;
		const seq = ++attachmentDraftSaveSeqRef.current;
		void clearDraftImageAttachments(currentUserId, narratorId).catch((err) => {
			if (seq === attachmentDraftSaveSeqRef.current) {
				warnDraftAttachmentsPersistenceFailure("clear", err);
			}
		});
	}, [currentUserId, narratorId, warnDraftAttachmentsPersistenceFailure]);

	useEffect(() => {
		let cancelled = false;
		const scope = documentAssetsRef.current;
		const localVersionAtRequest = attachmentDraftLocalVersionRef.current;
		const draftKey = currentUserId ? getDraftImageAttachmentKey(currentUserId, narratorId) : null;
		attachmentDraftHydratedKeyRef.current = null;
		attachedImagesRef.current = [];
		attachedTextFilesRef.current = [];
		setAttachedImages([]);
		setAttachedTextFiles([]);
		if (!currentUserId || !draftKey) return;

		const persistLocalChanges = () => {
			if (attachmentDraftLocalVersionRef.current !== localVersionAtRequest) {
				persistCurrentDraftAttachments(currentUserId, narratorId);
			}
		};

		void loadDraftImageAttachments(currentUserId, narratorId)
			.then((loaded) => {
				if (cancelled || scope !== documentAssetsRef.current) return;
				attachmentDraftHydratedKeyRef.current = draftKey;
				if (attachmentDraftLocalVersionRef.current === localVersionAtRequest) {
					const allowed = documentAssetsRef.current.allowedIds;
					const images = loaded.images.filter(
						(file) => !allowed || allowed.has(getComposerAssetId(file)),
					);
					const textFiles = loaded.textFiles.filter(
						(file) => !allowed || allowed.has(getComposerAssetId(file)),
					);
					attachedImagesRef.current = images;
					attachedTextFilesRef.current = textFiles;
					setAttachedImages(images);
					setAttachedTextFiles(textFiles);
					// An entry that was stored but cannot be rebuilt (blob evicted by the
					// browser, unreadable record) must be reported: silently restoring
					// two of three attachments looks like the user misremembered.
					if (loaded.droppedCount > 0) {
						notifications.show({
							color: "yellow",
							title: t("draftAttachmentsRestoreFailedTitle"),
							message: t("draftAttachmentsRestoreFailed", { count: loaded.droppedCount }),
						});
					}
				} else {
					persistLocalChanges();
				}
			})
			.catch((err) => {
				if (cancelled || scope !== documentAssetsRef.current) return;
				warnDraftAttachmentsPersistenceFailure("load", err);
				attachmentDraftHydratedKeyRef.current = draftKey;
				persistLocalChanges();
			});

		return () => {
			cancelled = true;
		};
	}, [
		currentUserId,
		narratorId,
		persistCurrentDraftAttachments,
		warnDraftAttachmentsPersistenceFailure,
		t,
	]);

	useEffect(() => {
		if (
			!currentUserId ||
			sendingRef.current ||
			attachmentDraftHydratedKeyRef.current !==
				getDraftImageAttachmentKey(currentUserId, narratorId)
		)
			return;
		const seq = ++attachmentDraftSaveSeqRef.current;
		void saveDraftImageAttachments(
			currentUserId,
			narratorId,
			attachedImages,
			attachedTextFiles,
		).catch((err) => {
			if (seq === attachmentDraftSaveSeqRef.current) {
				warnDraftAttachmentsPersistenceFailure("save", err);
			}
		});
	}, [
		attachedImages,
		attachedTextFiles,
		currentUserId,
		narratorId,
		sendingRef,
		warnDraftAttachmentsPersistenceFailure,
	]);

	const composerAssets: ComposerAsset[] = [
		...attachedImages.map((file) => ({
			id: getComposerAssetId(file),
			kind: "image" as const,
			name: file.name,
			size: file.size,
			mimeType: file.type,
		})),
		...attachedTextFiles.map((file) => ({
			id: getComposerAssetId(file),
			kind: "textFile" as const,
			name: file.name,
			size: file.size,
			mimeType: file.type,
		})),
	];
	return {
		composerAssets,
		attachedImages,
		attachedTextFiles,
		attachedImagesRef,
		attachedTextFilesRef,
		isDragging,
		setIsDragging,
		dragCounterRef,
		updateAttachedImages,
		updateAttachedTextFiles,
		reconcileDocumentAssets,
		hideAttachedFilesForSend,
		clearAttachedFilesAndDraft,
	};
}
