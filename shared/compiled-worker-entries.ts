/**
 * Extra `bun build --compile` entry points.
 *
 * `new Worker()` specifiers are not followed by the bundler. Each file must be
 * listed here or the compiled binary fails with
 * `Cannot find module '/$bunfs/root/...'`.
 *
 * Paths are relative to `server/`. `scripts/build-cross-platform.ts` is the only
 * consumer of the build list; runtime code asks `compiledWorkerSpecifiers`.
 */
export const COMPILED_WORKER_ENTRIES = [
	"lib/db-worker/worker-entry.ts",
	"lib/fs-scan-worker/fs-scan-worker-entry.ts",
	"lib/browser/memory-snapshot-worker.ts",
	"lib/browser/memory-profile-worker.ts",
	"lib/share-preview-worker.ts",
	"lib/request-recorder-worker.ts",
	"services/editor-document-worker.ts",
	"services/git-untracked-line-worker.ts",
	"services/git-current-baseline-worker.ts",
	"services/agent-runtime/model-history-worker.ts",
	"services/revert-transaction-manifest-worker.ts",
	"services/project-archive/legacy-sync-worker.ts",
	"services/project-archive/legacy-import-worker.ts",
] as const;

export type CompiledWorkerEntry = (typeof COMPILED_WORKER_ENTRIES)[number];

/** Arguments inserted after `./server/index.ts` in the production compile command. */
export function compiledWorkerBuildEntrypoints(): string[] {
	return COMPILED_WORKER_ENTRIES.map((path) => `./server/${path}`);
}
