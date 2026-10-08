import type { GitWorkspaceSummary } from "@shared/git-workspace";
import { eq } from "drizzle-orm";
import { db } from "../db";
import { narrators } from "../db/schema";
import { AppError } from "../lib/errors";
import { logger } from "../lib/logger";
import { authorizeGitTargetForPrincipal } from "./git-workspace-access";
import type { NarratorPrincipal } from "./narrator-acl";

/** Authorized cached first-paint facts. Never start or wait for Git/directory discovery. */
export async function getNarratorGitSummary(
	id: string,
	revision: number,
	principal: NarratorPrincipal,
	signal: AbortSignal,
): Promise<GitWorkspaceSummary | null> {
	const started = performance.now();
	try {
		const target = await authorizeGitTargetForPrincipal(
			principal,
			{ narratorId: id },
			"read",
			AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
			undefined,
			false,
			"cache-only",
		);
		if (!["ready", "not_git", "access_denied"].includes(target.workspace.state)) return null;
		// A switch completing during authorization must not label old detail with new facts.
		const current = await db.query.narrators.findFirst({
			where: eq(narrators.id, id),
			columns: { workspaceRevision: true },
		});
		if (current?.workspaceRevision !== revision) return null;
		return { workspace: target.workspace, branch: target.workspace.branch ?? null, revision };
	} catch (error) {
		signal.throwIfAborted();
		if (error instanceof AppError && error.code === "GIT_DISCOVERY_CACHE_MISS") return null;
		const denied = error instanceof AppError && error.statusCode === 403;
		if (!denied) {
			logger.warn("Narrator Git summary discovery failed", {
				narratorId: id,
				error: String(error),
			});
			// An exception is not evidence of an unsupported backend. Leave the
			// detail usable without seeding a permanent failure into useGitWorkspace;
			// its authoritative GET and transient-error recovery can retry safely.
			return null;
		}
		// Permission failures remain explicit and cannot expose cached Git facts.
		return {
			revision,
			branch: null,
			workspace: {
				narratorId: id,
				deviceId: "",
				cwd: "",
				rootPath: null,
				workspaceKey: null,
				repositoryKey: null,
				state: "access_denied",
				capabilities: { read: false, write: false },
			},
		};
	} finally {
		const elapsedMs = Math.round(performance.now() - started);
		if (elapsedMs >= 1000) logger.warn("Slow narrator Git summary", { narratorId: id, elapsedMs });
	}
}
