import { z } from "zod";
import {
	blacklistDirEntrySchema,
	commandBlacklistEntrySchema,
	commandSchema,
	commandWhitelistEntrySchema,
	gitBranchName,
	whitelistDirEntrySchema,
} from "./common";

export const createProjectSchema = z.object({
	name: z.string().min(1).max(200),
	description: z.string().max(2000).optional(),
	// Repository mode: "existing" (default), "init", "clone"
	repoMode: z.enum(["existing", "init", "clone"]).default("existing"),
	gitPath: z.string().trim().min(1).max(4096),
	// Clone-specific fields
	cloneUrl: z.string().min(1).optional(),
	cloneBranch: gitBranchName.optional(),
	cloneUsername: z.string().max(200).optional(),
	clonePassword: z.string().max(200).optional(),
});

export const projectChapterSettingsSchema = z.object({
	commands: z.array(commandSchema).max(100).optional(),
	routines: z
		.object({
			disabledRoutines: z.array(z.string()).optional(),
			enabledRoutines: z.array(z.string()).optional(),
			/** Three-position mode per optional tool routine. See lib/routine-modes.ts. */
			toolModes: z.record(z.string(), z.enum(["manual", "auto", "resident"])).optional(),
		})
		.optional(),
	whitelistDirs: z.array(whitelistDirEntrySchema).max(50).optional(),
	blacklistDirs: z.array(blacklistDirEntrySchema).max(50).optional(),
	commandWhitelist: z.array(commandWhitelistEntrySchema).max(50).optional(),
	commandBlacklist: z.array(commandBlacklistEntrySchema).max(50).optional(),
});

export const updateProjectSchema = z.object({
	name: z.string().min(1).max(200).optional(),
	description: z.string().max(2000).optional(),
	gitPath: z.string().trim().min(1).max(4096).optional(),
	status: z.enum(["active", "archived"]).optional(),
	defaultBranch: gitBranchName.optional(),
	chapterSettings: projectChapterSettingsSchema.optional(),
});

// ─────────────────────────────────────────────────────────────────────────────
// Project access control (membership)
// ─────────────────────────────────────────────────────────────────────────────

/** Who may reach the project at all. Write access never comes from here. */
export const projectVisibilitySchema = z.object({
	visibility: z.enum(["private", "public"]),
});

/**
 * Add or change members.
 *
 * Bounded at 50 per call: inviting people is a deliberate act on a handful of
 * colleagues, and an unbounded list would turn one request into an arbitrarily large
 * write on the main thread.
 */
export const projectMembersSchema = z.object({
	userIds: z.array(z.string().min(1).max(128)).min(1).max(50),
	/** read = follow along; write = work here; manage = also decide who else may. */
	role: z.enum(["read", "write", "manage"]).default("read"),
});

export const projectTransferOwnerSchema = z.object({
	/** null hands the project back to "no owner" (admin-managed). */
	userId: z.string().min(1).max(128).nullable(),
});
