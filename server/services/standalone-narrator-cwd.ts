/**
 * standalone-narrator-cwd.ts — where a STANDALONE narrator starts when the client
 * sent no directory.
 *
 * The answer is the instance setting `paths.defaultProjectDir`, but only when it names
 * a directory that EXISTS. Nothing here creates it: the setting starts unset (see
 * `project-dir.ts`), and silently `mkdir`-ing a guessed path — the old `~/projects` —
 * put first sessions in a folder the user never chose. With no usable default the
 * caller must refuse and ask for a directory instead of guessing one (home included).
 *
 * Filesystem effects are INJECTED so the decision can be tested without a real disk.
 */

import { expandHomePath } from "./project-dir";

export interface StandaloneNarratorCwdDeps {
	/** Absolute home directory of the server process. */
	home: string;
	/** `settings.paths.defaultProjectDir`, possibly empty/undefined. */
	configuredDir: string | undefined;
	/** Whether an absolute path is an existing directory. */
	isDirectory: (path: string) => boolean;
	/** Resolve to an absolute path (node:path `resolve`). */
	toAbsolute: (path: string) => string;
}

export type StandaloneNarratorCwd =
	/** Persist this cwd. */
	| { kind: "cwd"; cwd: string }
	/** No directory sent and no existing default: the user must choose one. */
	| { kind: "required" };

export function resolveStandaloneNarratorCwd(
	input: { cwd?: string },
	deps: StandaloneNarratorCwdDeps,
): StandaloneNarratorCwd {
	const explicit = input.cwd?.trim();
	if (explicit) return { kind: "cwd", cwd: explicit };

	const configured = deps.configuredDir?.trim();
	if (!configured) return { kind: "required" };
	const expanded = expandHomePath(configured, deps.home);
	if (!expanded) return { kind: "required" };
	const absolute = deps.toAbsolute(expanded);
	return deps.isDirectory(absolute) ? { kind: "cwd", cwd: absolute } : { kind: "required" };
}
