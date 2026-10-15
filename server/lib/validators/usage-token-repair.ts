import {
	TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES,
	TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES,
} from "@shared/usage-token-repair";
import { z } from "zod";

export const tokenRepairIdSchema = z
	.string()
	.trim()
	.min(1)
	.max(128)
	.regex(/^[a-zA-Z0-9_-]+$/);
export const tokenRepairProtocolOverridesSchema = z
	.array(
		z
			.object({
				provider: z.string().min(1).max(128).nullable(),
				model: z
					.string()
					.min(1)
					.max(256)
					.refine((model) => model.trim().length > 0, "A model identity is required"),
				protocol: z.enum(["openai", "anthropic", "gemini"]),
			})
			.strict(),
	)
	.max(TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES)
	.refine(
		(entries) =>
			new Set(entries.map((entry) => JSON.stringify([entry.provider, entry.model]))).size ===
			entries.length,
		"Duplicate model identities",
	)
	.refine(
		(entries) =>
			Buffer.byteLength(JSON.stringify(entries)) <= TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES,
		"Protocol choices exceed the byte budget",
	);
export const tokenRepairStartSchema = z
	.object({
		includeReviewed: z.boolean().default(false),
		protocolOverrides: tokenRepairProtocolOverridesSchema.default([]),
		sourceJobId: tokenRepairIdSchema.optional(),
	})
	.strict()
	.refine(
		(input) => !input.sourceJobId || input.includeReviewed,
		"Source-batch reanalysis must include reviewed records",
	);
export const tokenRepairApplySchema = z
	.object({
		candidateIds: z
			.array(tokenRepairIdSchema)
			.min(1)
			.max(50)
			.refine((ids) => new Set(ids).size === ids.length, "Duplicate candidate IDs"),
		confirm: z.literal(true),
	})
	.strict();
export const tokenRepairPageSchema = z.object({ cursor: tokenRepairIdSchema.optional() }).strict();
