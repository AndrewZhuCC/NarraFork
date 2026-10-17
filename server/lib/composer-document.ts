import {
	type ComposerDocument,
	composerDocumentText,
	MAX_COMPOSER_DOCUMENT_BYTES,
	parseComposerDocument,
} from "@shared/composer-document";
import type { FileReference } from "@shared/file-reference";
import { ValidationError } from "./errors";

export function parseOptionalComposerDocument(value: unknown): ComposerDocument | undefined {
	if (value === undefined || value === null) return undefined;
	if (
		typeof value === "string" &&
		(value.length > MAX_COMPOSER_DOCUMENT_BYTES ||
			Buffer.byteLength(value, "utf8") > MAX_COMPOSER_DOCUMENT_BYTES)
	)
		throw new ValidationError("Composer document exceeds the 2 MiB limit");
	try {
		return parseComposerDocument(typeof value === "string" ? JSON.parse(value) : value);
	} catch {
		throw new ValidationError("Invalid composer document");
	}
}
export function validateComposerProjection(
	document: ComposerDocument | undefined,
	text: string,
	references: readonly FileReference[] = [],
): void {
	if (!document) return;
	const projected = composerDocumentText(document);
	const canonical = (ref: FileReference) => [
		ref.id,
		ref.deviceId,
		ref.path,
		ref.label,
		ref.selection
			? [
					ref.selection.startLineNumber,
					ref.selection.startColumn,
					ref.selection.endLineNumber,
					ref.selection.endColumn,
				]
			: null,
		ref.inputRange ?? null,
		ref.expectedHash ?? null,
	];
	if (
		projected.text !== text ||
		JSON.stringify(projected.fileReferences.map(canonical)) !==
			JSON.stringify(references.map(canonical))
	)
		throw new ValidationError("Composer document does not match text or file references");
}
// biome-ignore lint/suspicious/noExplicitAny: persisted content blocks are dynamic JSON
export function validatePersistedComposerDocument(value: unknown, blocks: readonly any[]) {
	const document = parseOptionalComposerDocument(value);
	if (!document) return undefined;
	validateComposerProjection(
		document,
		blocks
			.filter((block) => block.type === "text")
			.map((block) => block.text)
			.join(""),
		blocks.filter((block) => block.type === "file_reference").map((block) => block.reference),
	);
	validateComposerAssets(
		document,
		blocks.filter((block) => block.type === "image"),
		blocks.filter((block) => block.type === "text_file"),
	);
	return document;
}

export interface ComposerAttachmentMetadata {
	name?: string;
	filename?: string;
	size?: number;
	type?: string;
	mimeType?: string;
	mediaType?: string;
	sourceMimeType?: string;
}
export function validateComposerAssets(
	document: ComposerDocument | undefined,
	images: readonly ComposerAttachmentMetadata[],
	textFiles: readonly ComposerAttachmentMetadata[],
): void {
	if (!document) return;
	for (const kind of ["image", "textFile"] as const) {
		const assets = document.assets.filter((asset) => asset.kind === kind);
		const files = kind === "image" ? images : textFiles;
		if (assets.length !== files.length)
			throw new ValidationError("Composer attachment count does not match uploads");
		assets.forEach((asset, index) => {
			const file = files[index];
			const name = file.name ?? file.filename;
			// File.type is a MIME, but persisted content blocks use type as a discriminator.
			const mime =
				file.sourceMimeType ??
				file.mimeType ??
				file.mediaType ??
				(file.type?.includes("/") ? file.type : undefined);
			if (
				(name !== undefined && asset.name !== name) ||
				(file.size !== undefined && asset.size !== file.size) ||
				(mime !== undefined &&
					asset.mimeType.split(";")[0].trim().toLowerCase() !==
						mime.split(";")[0].trim().toLowerCase())
			)
				throw new ValidationError("Composer attachment metadata does not match uploads");
		});
	}
}
