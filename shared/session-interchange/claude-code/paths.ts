/**
 * Where Claude Code keeps session transcripts, reproduced exactly.
 *
 * Claude Code resolves a session by `<config>/projects/<sanitizePath(cwd)>/<uuid>.jsonl`,
 * so an exported transcript is only resumable if every step here matches the CLI
 * byte for byte. Mirrors `sanitizePath` / `getProjectDir` / `getAgentTranscriptPath`
 * in Claude Code's `utils/sessionStoragePortable.ts` and `utils/sessionStorage.ts`.
 */

/** Longest sanitized component before Claude Code truncates and appends a hash. */
export const CLAUDE_CODE_MAX_SANITIZED_LENGTH = 200;

/** Claude Code's config directory name under the user's home. */
export const CLAUDE_CODE_CONFIG_DIRNAME = ".claude";

export const CLAUDE_CODE_PROJECTS_DIRNAME = "projects";

/**
 * The hash Claude Code appends to over-long sanitized paths.
 *
 * The CLI runs under Bun and uses `Bun.hash(name).toString(36)`; the SDK under Node
 * falls back to djb2, and the two disagree (Claude Code's own SDK compensates with a
 * prefix scan). Exporting for the CLI therefore needs Bun's hash, which is why it is
 * injected rather than imported: this module is also bundled for the browser.
 */
export type ClaudeCodePathHash = (value: string) => string;

/** Bun's hash when available, else Claude Code's own djb2 fallback. */
export function defaultClaudeCodePathHash(value: string): string {
	const bun = (globalThis as { Bun?: { hash?: (input: string) => number | bigint } }).Bun;
	if (bun?.hash) return bun.hash(value).toString(36);
	return Math.abs(djb2(value)).toString(36);
}

function djb2(value: string): number {
	let hash = 0;
	for (let i = 0; i < value.length; i++) {
		hash = ((hash << 5) - hash + value.charCodeAt(i)) | 0;
	}
	return hash;
}

/** Claude Code's `sanitizePath`: every non-alphanumeric becomes `-`, long names get a hash. */
export function sanitizeClaudeCodePath(
	name: string,
	hash: ClaudeCodePathHash = defaultClaudeCodePathHash,
): string {
	const sanitized = name.replace(/[^a-zA-Z0-9]/g, "-");
	if (sanitized.length <= CLAUDE_CODE_MAX_SANITIZED_LENGTH) return sanitized;
	return `${sanitized.slice(0, CLAUDE_CODE_MAX_SANITIZED_LENGTH)}-${hash(name)}`;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Claude Code only lists and resumes files whose basename is a UUID. */
export function isClaudeCodeSessionId(value: string): boolean {
	return UUID_PATTERN.test(value);
}

/** Session id from a transcript file name, or null when Claude Code would ignore the file. */
export function claudeCodeSessionIdFromFileName(fileName: string): string | null {
	if (!fileName.endsWith(".jsonl")) return null;
	const id = fileName.slice(0, -".jsonl".length);
	return isClaudeCodeSessionId(id) ? id : null;
}

/** agentId from a sidechain file name (`agent-<id>.jsonl`). */
export function claudeCodeAgentIdFromFileName(fileName: string): string | null {
	const match = /^agent-([A-Za-z0-9_-]+)\.jsonl$/.exec(fileName);
	return match?.[1] ?? null;
}

/** Path segments are joined by the caller with the target machine's separator. */
export interface ClaudeCodeSessionLayout {
	/** Relative to the config root: `projects/<sanitized cwd>`. */
	projectDir: string[];
	/** Relative to the config root. */
	transcript: string[];
	/** Relative to the config root: `projects/<sanitized cwd>/<sessionId>/subagents`. */
	subagentsDir: string[];
}

export function claudeCodeSessionLayout(
	cwd: string,
	sessionId: string,
	hash?: ClaudeCodePathHash,
): ClaudeCodeSessionLayout {
	const projectDir = [CLAUDE_CODE_PROJECTS_DIRNAME, sanitizeClaudeCodePath(cwd, hash)];
	return {
		projectDir,
		transcript: [...projectDir, `${sessionId}.jsonl`],
		subagentsDir: [...projectDir, sessionId, "subagents"],
	};
}

export function claudeCodeSubagentFileNames(agentId: string): {
	transcript: string;
	meta: string;
} {
	return { transcript: `agent-${agentId}.jsonl`, meta: `agent-${agentId}.meta.json` };
}

/**
 * A new agentId in Claude Code's shape: `a` followed by 16 hex digits. The CLI's
 * `createAgentId` uses the same alphabet; nothing parses the value beyond matching
 * it to the sidechain file name.
 */
export function newClaudeCodeAgentId(randomHex: (bytes: number) => string): string {
	return `a${randomHex(8)}`;
}
