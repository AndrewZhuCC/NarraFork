/**
 * Where Codex keeps session rollouts, reproduced exactly.
 *
 * `codex resume <id>` finds a thread by walking `sessions/` and matching the id in
 * the file name, then requires the first `session_meta` id to equal it. Mirrors
 * `precompute_new_rollout_path` and `RolloutFileName` in `codex-rs/rollout/src`.
 */

export const CODEX_HOME_DIRNAME = ".codex";
export const CODEX_SESSIONS_DIRNAME = "sessions";
export const CODEX_ARCHIVED_SESSIONS_DIRNAME = "archived_sessions";
export const CODEX_SESSION_INDEX_FILENAME = "session_index.jsonl";

const UUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
/**
 * `rollout-YYYY-MM-DDTHH-MM-SS-<threadId>[_<rolloutId>].jsonl[.zst]`. The optional
 * second id is written by `thread/revert`, which keeps the thread id stable.
 */
const ROLLOUT_FILE_PATTERN = new RegExp(
	`^rollout-(\\d{4}-\\d{2}-\\d{2}T\\d{2}-\\d{2}-\\d{2})-(${UUID})(?:_(${UUID}))?\\.jsonl(\\.zst)?$`,
	"i",
);

export interface ParsedCodexRolloutFileName {
	/** The file-name timestamp, as written (`YYYY-MM-DDTHH-MM-SS`). */
	stamp: string;
	threadId: string;
	rolloutId: string;
	compressed: boolean;
}

export function parseCodexRolloutFileName(fileName: string): ParsedCodexRolloutFileName | null {
	const match = ROLLOUT_FILE_PATTERN.exec(fileName);
	if (!match) return null;
	const [, stamp = "", threadId = "", rolloutId, compressed] = match;
	return {
		stamp,
		threadId: threadId.toLowerCase(),
		rolloutId: (rolloutId ?? threadId).toLowerCase(),
		compressed: compressed !== undefined,
	};
}

function pad(value: number, width = 2): string {
	return String(value).padStart(width, "0");
}

/**
 * Relative path of a new rollout under the Codex home.
 *
 * Codex uses the machine's local time for both the folders and the file-name stamp,
 * so the caller passes the date parts it wants written; the stamp only drives
 * listing order, never lookup, so any consistent zone works.
 */
export function codexRolloutPath(
	threadId: string,
	at: {
		year: number;
		month: number;
		day: number;
		hour: number;
		minute: number;
		second: number;
	},
): string[] {
	const date = `${pad(at.year, 4)}-${pad(at.month)}-${pad(at.day)}`;
	const time = `${pad(at.hour)}-${pad(at.minute)}-${pad(at.second)}`;
	return [
		CODEX_SESSIONS_DIRNAME,
		pad(at.year, 4),
		pad(at.month),
		pad(at.day),
		`rollout-${date}T${time}-${threadId}.jsonl`,
	];
}

export function localDateParts(date: Date) {
	return {
		year: date.getFullYear(),
		month: date.getMonth() + 1,
		day: date.getDate(),
		hour: date.getHours(),
		minute: date.getMinutes(),
		second: date.getSeconds(),
	};
}

/**
 * A UUIDv7. Codex mints v7 thread ids and uses them to order rollouts created in the
 * same second, so an exported id sorts where its timestamp says it should.
 */
export function newUuidV7(nowMs: number, randomBytes: (count: number) => Uint8Array): string {
	const bytes = randomBytes(16);
	let ms = BigInt(Math.max(0, Math.floor(nowMs)));
	for (let i = 5; i >= 0; i--) {
		bytes[i] = Number(ms & 0xffn);
		ms >>= 8n;
	}
	bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x70;
	bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
	const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
	return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
