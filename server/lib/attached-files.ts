import { formatFileSize } from "@shared/text-file-types";
import type { TextFileRef } from "./uploads";

export interface FileAttachmentLocation {
	filename: string;
	size: number;
	filePath?: string;
}

export interface ImageAttachmentLocation {
	filename: string;
	imageId: string;
	file?: TextFileRef;
}

/** Kept only to recognize the exact hint on previously persisted messages. */
export function buildLegacyAttachedFilesHint(textFiles: readonly TextFileRef[]): string {
	if (textFiles.length === 0) return "";
	return (
		"\n\n<attached_files>\n" +
		"The user has attached the following files for your reference. " +
		"Use the Read tool to access their contents when needed.\n" +
		`${textFiles.map((file) => `- ${file.filePath} (${file.filename}, ${formatFileSize(file.size)})`).join("\n")}\n` +
		"</attached_files>"
	);
}

/** One provider-visible locator list for files and image worktree copies. */
export function buildAttachedFilesHint(
	textFiles: readonly FileAttachmentLocation[],
	images: readonly ImageAttachmentLocation[] = [],
): string {
	if (textFiles.length === 0 && images.length === 0) return "";
	const lines = textFiles.map(
		(file) =>
			`- file: ${JSON.stringify(file.filename)}; ` +
			(file.filePath
				? `device: local; path: ${JSON.stringify(file.filePath)}; size: ${formatFileSize(file.size)}`
				: "worktree copy unavailable; no usable path"),
	);
	for (const image of images) {
		lines.push(
			`- image: ${JSON.stringify(image.filename)}; ` +
				(image.file
					? `device: local; path: ${JSON.stringify(image.file.filePath)}; size: ${formatFileSize(image.file.size)}`
					: "worktree copy unavailable; no usable path"),
		);
	}
	return `\n\n<attached_files>\n${lines.join("\n")}\n</attached_files>`;
}
