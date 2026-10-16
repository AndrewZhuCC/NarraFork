import { notifications } from "@mantine/notifications";
import type React from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { unstable_batchedUpdates } from "react-dom";
import { inheritComposerFileOrder, markComposerFileOrder } from "../composer/composer-file-order";
import {
	ACCEPTED_TYPES,
	isTextFile,
	MAX_IMAGE_LONG_EDGE,
	MAX_IMAGE_SIZE,
	MAX_TEXT_FILE_SIZE,
	resizeImageIfNeeded,
} from "../narrator-panel-types";

export const COMPOSER_FILE_INGEST_TIMEOUT_MS = 5000;
/** Structural contract: ingestion must not depend on the editor implementation. */
export type AttachmentInsertionBookmark = { restore(): boolean; cancel(): void };
export type AttachmentInsertionOrigin = "picker" | "paste" | "drop" | "direct";
export interface UseComposerFileIngestOptions {
	updateAttachedImages: React.Dispatch<React.SetStateAction<File[]>>;
	updateAttachedTextFiles: React.Dispatch<React.SetStateAction<File[]>>;
	setIsDragging: (dragging: boolean) => void;
	dragCounterRef: React.RefObject<number>;
	t: (key: string, options?: Record<string, unknown>) => string;
	/** Optional only for legacy callers. New callers must supply the current owner scope. */
	narratorId?: string;
	/** Shared send lock; never mutated by ingestion. */
	sendingRef?: React.RefObject<boolean>;
	captureInsertion?: (origin: AttachmentInsertionOrigin) => AttachmentInsertionBookmark | undefined;
	/** False rejects the batch; never fall back to the current editor selection. */
	onAcceptedFiles?: (files: File[], bookmark?: AttachmentInsertionBookmark) => boolean;
}
type ClassifiedFile = { file: File; kind: "image" | "textFile" };
type IngestBatch = {
	generation: number;
	files: ClassifiedFile[];
	controller: AbortController;
	bookmark?: AttachmentInsertionBookmark;
	resolve: (files: File[]) => void;
};

/** Bound waiting on the legacy decoder; late results are observed but never applied. */
function resizeWithinBatch(file: File, signal: AbortSignal): Promise<File> {
	if (signal.aborted || file.type === "image/gif") return Promise.resolve(file);
	return new Promise((resolve) => {
		let settled = false;
		const finish = (result: File) => {
			if (settled) return;
			settled = true;
			signal.removeEventListener("abort", cancel);
			resolve(inheritComposerFileOrder(result, file));
		};
		const cancel = () => finish(file);
		signal.addEventListener("abort", cancel, { once: true });
		try {
			Promise.resolve(resizeImageIfNeeded(file, MAX_IMAGE_LONG_EDGE)).then(
				(result) => finish(result),
				() => finish(file),
			);
		} catch {
			finish(file);
		}
	});
}

/** All entry points share one validated, ordered batch and one atomic array commit. */
export function useComposerFileIngest(options: UseComposerFileIngestOptions) {
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const mountedRef = useRef(true);
	const queueRef = useRef<IngestBatch[]>([]);
	const activeRef = useRef<IngestBatch | null>(null);
	const [isIngesting, setIsIngesting] = useState(false);
	const ingestingRef = useRef(false);
	const sending = !!options.sendingRef?.current;
	const scopeRef = useRef({ narratorId: options.narratorId, generation: 0, sending });
	// Invalidate during render, before effects or an old promise can write to the new owner.
	if (
		scopeRef.current.narratorId !== options.narratorId ||
		(sending && !scopeRef.current.sending)
	) {
		scopeRef.current.generation++;
		scopeRef.current.narratorId = options.narratorId;
		activeRef.current?.bookmark?.cancel();
		activeRef.current?.controller.abort();
		for (const batch of queueRef.current) {
			batch.bookmark?.cancel();
			batch.controller.abort();
		}
	}
	scopeRef.current.sending = sending;
	const resetDrag = () => {
		optionsRef.current.dragCounterRef.current = 0;
		optionsRef.current.setIsDragging(false);
	};
	const alive = (batch: IngestBatch) =>
		mountedRef.current &&
		batch.generation === scopeRef.current.generation &&
		!optionsRef.current.sendingRef?.current;
	const publishBusy = () => {
		const busy = !!activeRef.current || queueRef.current.length > 0;
		ingestingRef.current = busy;
		if (mountedRef.current) setIsIngesting(busy);
	};
	const pumpRef = useRef<() => void>(() => {});
	pumpRef.current = () => {
		if (activeRef.current) return;
		const batch = queueRef.current.shift();
		if (!batch) {
			publishBusy();
			return;
		}
		if (!alive(batch)) {
			batch.bookmark?.cancel();
			batch.resolve([]);
			pumpRef.current();
			return;
		}
		activeRef.current = batch;
		const finish = (processed: ClassifiedFile[]) => {
			let accepted = alive(batch) ? processed : [];
			if (accepted.length && optionsRef.current.onAcceptedFiles) {
				if (
					!optionsRef.current.onAcceptedFiles(
						accepted.map((item) => item.file),
						batch.bookmark,
					)
				)
					accepted = [];
			} else if (accepted.length) {
				const images = accepted.filter((item) => item.kind === "image").map((item) => item.file);
				const textFiles = accepted
					.filter((item) => item.kind === "textFile")
					.map((item) => item.file);
				unstable_batchedUpdates(() => {
					if (images.length)
						optionsRef.current.updateAttachedImages((previous) =>
							alive(batch) ? [...previous, ...images] : previous,
						);
					if (textFiles.length)
						optionsRef.current.updateAttachedTextFiles((previous) =>
							alive(batch) ? [...previous, ...textFiles] : previous,
						);
				});
			}
			batch.bookmark?.cancel();
			batch.resolve(accepted.map((item) => item.file));
			activeRef.current = null;
			pumpRef.current();
		};
		if (batch.files.every((item) => item.kind !== "image" || item.file.type === "image/gif")) {
			finish(batch.files);
			return;
		}
		// A whole batch takes at most five seconds, not five seconds multiplied by file count.
		const timer = setTimeout(() => batch.controller.abort(), COMPOSER_FILE_INGEST_TIMEOUT_MS);
		const processed = batch.files.map((item) => ({ ...item }));
		let cursor = 0;
		const worker = async () => {
			while (cursor < processed.length) {
				const item = processed[cursor++];
				if (item.kind === "image")
					item.file = await resizeWithinBatch(item.file, batch.controller.signal);
			}
		};
		void Promise.all([worker(), worker()]).then(() => {
			clearTimeout(timer);
			finish(processed);
		});
	};
	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
			scopeRef.current.generation++;
			activeRef.current?.bookmark?.cancel();
			activeRef.current?.controller.abort();
			for (const batch of queueRef.current.splice(0)) {
				batch.bookmark?.cancel();
				batch.controller.abort();
				batch.resolve([]);
			}
		};
	}, []);
	const ingestRef = useRef<
		(
			files: File[],
			origin: AttachmentInsertionOrigin,
			only?: ClassifiedFile["kind"],
		) => Promise<File[]>
	>(() => Promise.resolve([]));
	ingestRef.current = (files, origin, only) => {
		resetDrag();
		// Capture now, not after resize or after earlier FIFO batches finish. Even an
		// empty picker event consumes and cancels the pre-picker bookmark.
		const bookmark = optionsRef.current.captureInsertion?.(origin);
		if (!mountedRef.current || optionsRef.current.sendingRef?.current) {
			bookmark?.cancel();
			return Promise.resolve([]);
		}
		const accepted: ClassifiedFile[] = [];
		const unsupported: string[] = [];
		for (const file of files) {
			const kind =
				only === "textFile"
					? isTextFile(file.name)
						? "textFile"
						: null
					: ACCEPTED_TYPES.includes(file.type)
						? "image"
						: only === "image"
							? null
							: isTextFile(file.name)
								? "textFile"
								: null;
			if (!kind) {
				if (only !== "image") unsupported.push(file.name);
				continue;
			}
			const max = kind === "image" ? MAX_IMAGE_SIZE : MAX_TEXT_FILE_SIZE;
			if (file.size > max) {
				notifications.show({
					title: optionsRef.current.t(kind === "image" ? "imageTooLarge" : "textFileTooLarge"),
					message: `${file.name} (${(file.size / 1024 / 1024).toFixed(1)} MB)`,
					color: "yellow",
				});
				continue;
			}
			markComposerFileOrder(file);
			accepted.push({ file, kind });
		}
		if (unsupported.length)
			notifications.show({
				title: optionsRef.current.t("unsupportedFileType"),
				message: unsupported.join(", "),
				color: "yellow",
			});
		if (!accepted.length) {
			bookmark?.cancel();
			return Promise.resolve([]);
		}
		const result = new Promise<File[]>((resolve) =>
			queueRef.current.push({
				generation: scopeRef.current.generation,
				files: accepted,
				controller: new AbortController(),
				bookmark,
				resolve,
			}),
		);
		ingestingRef.current = true;
		setIsIngesting(true);
		pumpRef.current();
		return result;
	};
	const ingestFiles = useCallback(
		(files: File[], origin: AttachmentInsertionOrigin = "direct") =>
			ingestRef.current(files, origin),
		[],
	);
	const addImages = useCallback((files: File[]) => ingestRef.current(files, "direct", "image"), []);
	const addTextFiles = useCallback(
		(files: File[]) => ingestRef.current(files, "direct", "textFile"),
		[],
	);
	// Legacy name, intentionally accepts both images and text files now.
	const handleComposerPasteImages = useCallback(
		(files: File[]) => ingestRef.current(files, "paste"),
		[],
	);
	const handleFileInputChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
		const files = Array.from(event.target.files ?? []);
		event.target.value = "";
		void ingestRef.current(files, "picker");
	}, []);
	const isFileDrag = (event: React.DragEvent) => event.dataTransfer.types.includes("Files");
	return {
		ingestFiles,
		addImages,
		addTextFiles,
		isIngesting,
		ingestingRef,
		handleFileInputChange,
		handleComposerPasteImages,
		dropZoneProps: {
			onDragEnter: (event: React.DragEvent) => {
				if (!isFileDrag(event)) return;
				event.preventDefault();
				event.stopPropagation();
				if (optionsRef.current.sendingRef?.current) return;
				optionsRef.current.dragCounterRef.current++;
				optionsRef.current.setIsDragging(true);
			},
			onDragLeave: (event: React.DragEvent) => {
				if (!isFileDrag(event)) return;
				event.preventDefault();
				event.stopPropagation();
				optionsRef.current.dragCounterRef.current = Math.max(
					0,
					optionsRef.current.dragCounterRef.current - 1,
				);
				if (!optionsRef.current.dragCounterRef.current) optionsRef.current.setIsDragging(false);
			},
			onDragOver: (event: React.DragEvent) => {
				if (!isFileDrag(event)) return;
				event.preventDefault();
				event.stopPropagation();
				event.dataTransfer.dropEffect = optionsRef.current.sendingRef?.current ? "none" : "copy";
			},
			onDrop: (event: React.DragEvent) => {
				if (!isFileDrag(event)) return;
				const handledByChild = event.defaultPrevented;
				event.preventDefault();
				event.stopPropagation();
				resetDrag();
				if (!handledByChild) void ingestRef.current(Array.from(event.dataTransfer.files), "drop");
			},
		},
	};
}
