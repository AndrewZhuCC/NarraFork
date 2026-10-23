import { db } from "@server/db";
import {
	narratorBlacklistCmds,
	narratorBlacklistDirs,
	narrators,
	narratorWhitelistCmds,
	narratorWhitelistDirs,
	projects,
} from "@server/db/schema";
import { MAX_POLICY_SOURCE_DEPTH, policySourceIdOf } from "@server/lib/runtime-source";
import { getSettingsRevision, settings } from "@server/lib/settings";
import { eq, inArray } from "drizzle-orm";
import { narratorProjectIdColumn } from "../narrator-project";
import { isSignedReviewBoundaryRow, markTrustedReviewBoundary } from "../narrator-review-boundary";
import {
	mergeExecutionPolicyRuleSets,
	normalizeDirectoryBlacklistRule,
	normalizeExecutionPolicyRuleSet,
} from "./normalize";
import type { ExecutionPolicyRuleSet, LegacyExecutionPolicyRuleSet } from "./types";

export interface LoadedExecutionPolicy extends ExecutionPolicyRuleSet {
	narratorId: string;
	ownerNarratorId: string;
	/** This narrator and its registered policy-source chain, never siblings. */
	inheritedNarratorIds?: string[];
	projectId: string | null;
	projectGitPath: string | null;
	settingsRevision: number;
}

export class ExecutionPolicyRepository {
	async load(narratorId: string): Promise<LoadedExecutionPolicy> {
		return this.loadNow(narratorId);
	}

	/**
	 * Bounded synchronous snapshot, also usable inside the final request transaction.
	 * Rules merge along the registered policy-source chain (same source rule and depth
	 * bound as capability resolution), so any two related narrators share rules the
	 * same way, whatever created them.
	 */
	loadNow(narratorId: string, store: Pick<typeof db, "select"> = db): LoadedExecutionPolicy {
		const inheritedNarratorIds: string[] = [];
		let next: string | null = narratorId;
		let projectId: string | null = null;
		const contextProjects = new Set<string>();
		while (next) {
			if (
				inheritedNarratorIds.includes(next) ||
				inheritedNarratorIds.length >= MAX_POLICY_SOURCE_DEPTH
			) {
				throw new Error("Invalid or excessive permission inheritance chain");
			}
			const narrator = store
				.select({
					id: narrators.id,
					// The narrator's primary project (explicit, else its indexed directory's):
					// the same single answer as resolveNarratorProjectId.
					projectId: narratorProjectIdColumn,
					policySourceNarratorId: narrators.policySourceNarratorId,
					runtimeRegistrationJson: narrators.runtimeRegistrationJson,
					type: narrators.type,
					variant: narrators.variant,
					parentNarratorId: narrators.parentNarratorId,
				})
				.from(narrators)
				.where(eq(narrators.id, next))
				.get();
			if (!narrator) throw new Error(`Narrator not found: ${next}`);
			inheritedNarratorIds.push(narrator.id);
			const effectiveProjectId = narrator.projectId;
			projectId ??= effectiveProjectId;
			if (effectiveProjectId) contextProjects.add(effectiveProjectId);
			next = policySourceIdOf(narrator);
		}
		const ownerNarratorId = inheritedNarratorIds.at(-1) ?? narratorId;
		let projectGitPath: string | null = null;
		const projectRuleSets: ExecutionPolicyRuleSet[] = [];
		for (const contextProjectId of contextProjects) {
			const project = store
				.select({ chapterSettings: projects.chapterSettings, gitPath: projects.gitPath })
				.from(projects)
				.where(eq(projects.id, contextProjectId))
				.get();
			if (!project) throw new Error("Permission project context no longer exists");
			if (contextProjectId === projectId) projectGitPath = project.gitPath;
			// Switching a child's context cannot erase the parent's project deny layer.
			projectRuleSets.push(
				normalizeExecutionPolicyRuleSet(
					project.chapterSettings as LegacyExecutionPolicyRuleSet,
					"project",
				),
			);
		}
		const [whitelistDirs, blacklistDirs, commandWhitelist, commandBlacklist] = [
			store
				.select()
				.from(narratorWhitelistDirs)
				.where(inArray(narratorWhitelistDirs.narratorId, inheritedNarratorIds))
				.limit(2001)
				.all(),
			store
				.select()
				.from(narratorBlacklistDirs)
				.where(inArray(narratorBlacklistDirs.narratorId, inheritedNarratorIds))
				.limit(2001)
				.all(),
			store
				.select()
				.from(narratorWhitelistCmds)
				.where(inArray(narratorWhitelistCmds.narratorId, inheritedNarratorIds))
				.limit(2001)
				.all(),
			store
				.select()
				.from(narratorBlacklistCmds)
				.where(inArray(narratorBlacklistCmds.narratorId, inheritedNarratorIds))
				.limit(2001)
				.all(),
		] as const;
		if (
			[whitelistDirs, blacklistDirs, commandWhitelist, commandBlacklist].some(
				(rows) => rows.length > 2000,
			)
		) {
			throw new Error("Permission rule budget exceeded");
		}
		return {
			narratorId,
			ownerNarratorId,
			inheritedNarratorIds,
			projectId,
			projectGitPath,
			settingsRevision: getSettingsRevision(),
			...mergeExecutionPolicyRuleSets(
				normalizeExecutionPolicyRuleSet(settings.agent, "global"),
				...projectRuleSets,
				normalizeExecutionPolicyRuleSet(
					{
						whitelistDirs,
						blacklistDirs: [],
						commandWhitelist,
						commandBlacklist,
					},
					"narrator",
				),
				{
					directoryWhitelist: [],
					directoryBlacklist: [
						...blacklistDirs.map((row) => {
							const normalized = normalizeDirectoryBlacklistRule(row, "narrator");
							// Only persisted instance-signed rows confer the additional hard fence.
							// Unsigned legacy/global/project IDs remain ordinary deny rules.
							return isSignedReviewBoundaryRow(row)
								? markTrustedReviewBoundary({ ...normalized, enabled: true })
								: normalized;
						}),
					],
					commandWhitelist: [],
					commandBlacklist: [],
				},
			),
		};
	}
}
export const executionPolicyRepository = new ExecutionPolicyRepository();
