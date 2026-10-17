import { rm } from "node:fs/promises";
import { join } from "node:path";
import { logger } from "../lib/logger";
import { getNarraforkHome } from "../lib/narrafork-home";

/**
 * Directories written by the pre-unification request dump and token repair features.
 * Request evidence now lives only in the recorder; nothing reads these back.
 */
export const LEGACY_REQUEST_DUMP_DIRS = [
	"request-dumps",
	"malformed-request-dumps",
	"token-repair-jobs",
] as const;

/** Async fs only; runs once in the background at startup and never blocks requests. */
export async function removeLegacyRequestDumpDirs(home = getNarraforkHome()): Promise<void> {
	for (const name of LEGACY_REQUEST_DUMP_DIRS) {
		try {
			await rm(join(home, name), { recursive: true, force: true, maxRetries: 2 });
		} catch (error) {
			logger.warn("Failed to remove legacy request dump directory", {
				directory: name,
				error: String(error),
			});
		}
	}
}
