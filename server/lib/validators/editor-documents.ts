import { z } from "zod";
import { EDITOR_FILE_MAX_BYTES } from "../../../shared/editor-document";

export const editorOperationMetadataSchema = z
	.object({
		version: z.literal(1),
		userId: z.string().min(1).max(128),
		narratorId: z.string().min(1).max(128),
		operationId: z.string().uuid(),
		snapshotRevision: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
		hash: z.string().regex(/^[a-f0-9]{64}$/),
		rawDigest: z.string().regex(/^[a-f0-9]{64}$/),
		bytes: z.number().int().min(0).max(EDITOR_FILE_MAX_BYTES),
		createdAt: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
	})
	.strict();
export type EditorOperationMetadata = z.infer<typeof editorOperationMetadataSchema>;

const path = z
	.string()
	.trim()
	.min(1)
	.max(4096)
	.refine((value) => !value.includes("\0"));
const editorDocumentInputSchema = z
	.object({
		path,
		deviceId: z.string().min(1).max(128).optional(),
		origin: z.enum(["reference", "legacy"]),
	})
	.strict();
export const createEditorDocumentSchema = z.union([
	editorDocumentInputSchema.extend({ protocol: z.literal("text-v1").optional() }).strict(),
	editorDocumentInputSchema
		.extend({
			protocol: z.literal("raw-v2"),
			encodingHint: z.enum(["utf-8", "utf-16le", "utf-16be", "gbk", "gb18030"]).optional(),
		})
		.strict(),
]);
export const editorRawOpenSchema = z.object({ versionHandle: z.string().uuid() }).strict();
export const editorRawChunkSchema = z
	.object({
		offset: z.number().int().nonnegative().max(EDITOR_FILE_MAX_BYTES),
		maxBytes: z
			.number()
			.int()
			.min(1)
			.max(64 * 1024),
	})
	.strict();
export const editorRecoveryScopeSchema = editorDocumentInputSchema.omit({ path: true }).strict();
export const createEditorDraftAnchorSchema = editorRecoveryScopeSchema
	.extend({
		writerId: z.string().min(1).max(128),
		resourceKey: z.string().regex(/^[a-f0-9]{64}$/),
		revision: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
		modelLifetime: z.string().min(1).max(128),
		operation: z
			.object({
				operationId: z.string().uuid().optional(),
				snapshotRevision: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
			})
			.strict()
			.optional(),
	})
	.strict();
export const createEditorUploadSchema = z
	.object({
		baseHash: z
			.string()
			.regex(/^[a-f0-9]{64}$/)
			.nullable(),
		encoding: z.string().min(1).max(64),
		snapshotRevision: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
	})
	.strict();
export const commitEditorUploadSchema = z
	.object({
		confirmationToken: z.string().min(1).max(128).optional(),
		draftWriterId: z.string().min(1).max(128).optional(),
		draftModelLifetime: z.string().min(1).max(128).optional(),
	})
	.strict()
	.refine((input) => !!input.draftWriterId === !!input.draftModelLifetime, {
		message: "Draft writer and model lifetime must be provided together",
	});
