/**
 * Prefetch the bytes a projection needs (§7.1): uploaded images of inputs, images local
 * tools returned, and the saved result of provider image generations. Reading happens
 * here, asynchronously, so the projection itself is a pure function of the view and
 * these bytes.
 */

import type { CanonicalEnvelope } from "@shared/narrator-blocks/types";
import { logger } from "../../logger";
import { getImagePath, imageToBase64 } from "../../uploads";
import type { ProjectionAssets, ResolvedImage } from "./turns";

const FORMAT_BY_MEDIA_TYPE: Record<string, string> = {
	"image/png": "png",
	"image/jpeg": "jpeg",
	"image/gif": "gif",
	"image/webp": "webp",
};

async function readUpload(
	uploadNarratorId: string,
	imageId: string,
	storedMediaType: string | undefined,
	context: Record<string, unknown>,
): Promise<ResolvedImage | null> {
	const filePath = getImagePath(uploadNarratorId, imageId);
	if (!filePath) {
		logger.warn("History image missing on disk; skipping replay", {
			...context,
			uploadNarratorId,
			imageId,
		});
		return null;
	}
	try {
		const result = await imageToBase64(filePath);
		// The detected type wins over the stored one: bytes saved under the wrong
		// extension would otherwise be declared as a type the API rejects.
		const mediaType = result.detectedMediaType ?? storedMediaType ?? "image/png";
		return { mediaType, format: FORMAT_BY_MEDIA_TYPE[mediaType] ?? "png", base64: result.base64 };
	} catch (error) {
		logger.warn("Failed to read history image; skipping replay", {
			...context,
			uploadNarratorId,
			imageId,
			error: error instanceof Error ? error.message : String(error),
		});
		return null;
	}
}

/** Load the image bytes of every input, tool result and generated image in the view. */
export async function resolveProjectionAssets(
	envelopes: readonly CanonicalEnvelope[],
	narratorId?: string,
): Promise<ProjectionAssets> {
	const inputImages = new Map<string, Array<ResolvedImage | null>>();
	const generatedImages = new Map<string, string>();
	const toolResultImages = new Map<string, ResolvedImage[]>();
	for (const envelope of envelopes) {
		const owner = envelope.narratorId ?? narratorId;
		for (const block of envelope.blocks) {
			if (block.kind === "input") {
				const images = block.payload.parts.filter((part) => part.type === "image");
				if (!images.length) continue;
				if (!owner) {
					inputImages.set(
						block.id,
						images.map(() => null),
					);
					continue;
				}
				const slots: Array<ResolvedImage | null> = [];
				for (const image of images)
					slots.push(
						await readUpload(image.uploadNarratorId ?? owner, image.assetId, image.mediaType, {
							narratorId,
							messageId: envelope.id,
						}),
					);
				inputImages.set(block.id, slots);
				continue;
			}
			if (block.kind === "tool" && block.payload.executor === "local") {
				const parts = block.payload.result?.parts ?? [];
				if (!parts.length || block.phase !== "done") continue;
				const images: ResolvedImage[] = [];
				for (const part of parts) {
					const uploader = part.uploadNarratorId ?? owner;
					const image = uploader
						? await readUpload(uploader, part.assetId, part.mediaType, {
								narratorId,
								messageId: envelope.id,
								toolUseId: block.wireId,
							})
						: null;
					if (image) images.push(image);
				}
				if (images.length) toolResultImages.set(block.id, images);
				continue;
			}
			if (block.kind === "tool" && block.subtype === "image_generation") {
				const result = block.providerMeta?.result as Record<string, unknown> | undefined;
				const savedPath = typeof result?.savedPath === "string" ? result.savedPath : undefined;
				if (!savedPath || (typeof result?.result === "string" && result.result)) continue;
				try {
					const bytes = Buffer.from(await Bun.file(savedPath).arrayBuffer());
					generatedImages.set(block.id, bytes.toString("base64"));
				} catch {
					// The file may have been removed; the call is then replayed without its image.
				}
			}
		}
	}
	return { inputImages, generatedImages, toolResultImages };
}
