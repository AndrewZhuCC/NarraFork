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
		// A full v2 scan includes legacy reviewed rows; choices are made after scanning.
		includeReviewed: z.literal(true).default(true),
	})
	.strict();
export const tokenRepairApplySchema = z
	.object({
		previewVersion: tokenRepairIdSchema,
		confirm: z.literal(true),
	})
	.strict();

export const tokenRepairDryRunSchema = z
	.object({ protocolOverrides: tokenRepairProtocolOverridesSchema.default([]) })
	.strict();
// Model cursors are opaque identities, not request IDs (they may contain JSON punctuation).
export const tokenRepairPageSchema = z
	.object({ cursor: z.string().min(1).max(2048).optional() })
	.strict();
