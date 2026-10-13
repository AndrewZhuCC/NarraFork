import { z } from "zod";

/** Claude Code / Codex session import and export. */

export const sessionInterchangeFormatSchema = z.enum(["claude-code", "codex"]);

/** A path on the target machine, as typed or picked by the user. */
const targetPathSchema = z
	.string()
	.trim()
	.min(1)
	.max(4096)
	.refine((value) => !value.includes("\0"), "Path contains a NUL byte");

/** Optional text fields from multipart forms arrive as "" when left empty. */
const optionalText = (max: number) =>
	z
		.string()
		.trim()
		.max(max)
		.optional()
		.transform((value) => value || undefined);

/**
 * Where scan/import/export-to operate. `root` overrides the CLI's config dir
 * (`~/.claude`, `~/.codex`); it is required on a device whose home cannot be
 * inferred from its default cwd.
 */
const sessionLocationSchema = z.discriminatedUnion("target", [
	z.object({ target: z.literal("local"), root: targetPathSchema.optional() }),
	z.object({
		target: z.literal("device"),
		deviceId: z.string().trim().min(1).max(128),
		root: targetPathSchema.optional(),
	}),
]);

export const sessionScanQuerySchema = z
	.object({
		format: sessionInterchangeFormatSchema,
		target: z.enum(["local", "device"]),
		deviceId: z.string().trim().min(1).max(128).optional(),
		root: targetPathSchema.optional(),
		cursor: z.string().max(2048).optional(),
		limit: z.coerce.number().int().min(1).max(50).optional(),
	})
	.refine((value) => value.target !== "device" || !!value.deviceId, {
		message: "deviceId is required for a device scan",
		path: ["deviceId"],
	});

const importOverridesSchema = z.object({
	cwd: optionalText(4096),
	model: optionalText(200),
	title: optionalText(200),
});

export const sessionImportFromPathSchema = z
	.object({
		format: sessionInterchangeFormatSchema,
		location: sessionLocationSchema,
		path: targetPathSchema,
	})
	.merge(importOverridesSchema);

/** Text fields of the multipart upload; files are read separately. */
export const sessionImportUploadFieldsSchema = z
	.object({ format: z.union([sessionInterchangeFormatSchema, z.literal("auto")]).default("auto") })
	.merge(importOverridesSchema);

const exportCommonSchema = z.object({
	format: sessionInterchangeFormatSchema,
	cwd: optionalText(4096),
	codexModelProvider: z
		.string()
		.trim()
		.max(64)
		.regex(/^[A-Za-z0-9._-]*$/, "Provider id may only contain letters, digits, . _ -")
		.optional()
		.transform((value) => value || undefined),
	dropAllReasoning: z
		.union([z.boolean(), z.enum(["true", "false", "1", "0"])])
		.optional()
		.transform((value) => value === true || value === "true" || value === "1"),
});

export const sessionExportDownloadQuerySchema = exportCommonSchema;

export const sessionExportToSchema = exportCommonSchema.extend({ location: sessionLocationSchema });

export type SessionLocationInput = z.infer<typeof sessionLocationSchema>;
