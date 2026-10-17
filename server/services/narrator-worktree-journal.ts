import { createHash, randomUUID } from "node:crypto";
import { constants } from "node:fs";
import { type FileHandle, lstat, mkdir, open, rename } from "node:fs/promises";
import { resolve } from "node:path";
import type { WorktreeCreateResult } from "@shared/narrator-worktrees";
import { AppError } from "../lib/errors";
import type { WorktreeCreateRequest } from "../lib/validators/narrator-worktrees";

const MAX_RECORD_BYTES = 32 * 1024;

/** Resource evidence only, never recovery authorization or permission to retry a request.
 * Unknown/malformed/contradictory evidence stays protected; only a proven pre-dispatch
 * receipt can be omitted before validating its (possibly rejected) destination.
 */
export function worktreeReceiptHasResourceEffect(record: unknown): boolean {
	if (!record || typeof record !== "object" || Array.isArray(record)) return true;
	const receipt = record as Record<string, unknown>;
	if (receipt.commandSucceeded !== undefined || receipt.dispatchState === "started") return true;
	if (receipt.dispatchState !== undefined && receipt.dispatchState !== "not_started") return true;
	if (receipt.result === undefined) return receipt.dispatchState !== "not_started";
	if (!receipt.result || typeof receipt.result !== "object" || Array.isArray(receipt.result))
		return true;
	const result = receipt.result as Record<string, unknown>;
	if (result.outcome !== "failed" || result.worktree !== null) return true;
	if (!result.error || typeof result.error !== "object" || Array.isArray(result.error)) return true;
	const failure = result.error as Record<string, unknown>;
	if (typeof failure.code !== "string" || !failure.code || typeof failure.message !== "string")
		return true;
	if (!result.residuals || typeof result.residuals !== "object" || Array.isArray(result.residuals))
		return true;
	const residuals = result.residuals as Record<string, unknown>;
	if (residuals.destinationExists !== null || residuals.branchExists !== null) return true;
	// Legacy HEAD was saved after validation, before any possible worktree add.
	return receipt.dispatchState !== "not_started" && receipt.expectedHead !== "";
}

export type { WorktreeCreateResult, WorktreeEntry } from "@shared/narrator-worktrees";
export interface WorktreeJournalRecord {
	proposalHash: string;
	request: WorktreeCreateRequest;
	repositoryKey: string;
	/** Server-side actor and execution-device binding, never accepted from client input. */
	actorKey?: string;
	deviceId?: string;
	destination: string;
	expectedHead: string;
	/** Resolved once before dispatch; original request stays unchanged for proposal hashing. */
	branchName?: string;
	/** Persist started before dispatch; an interrupted started operation is never retried. */
	dispatchState?: "not_started" | "started";
	commandSucceeded?: boolean;
	result?: WorktreeCreateResult;
}
export interface WorktreeJournal {
	/** Bounded read only: never creates a receipt/directory or claims an absent requestId. */
	read(narratorId: string, requestId: string): Promise<WorktreeJournalRecord | null>;
	claim(
		narratorId: string,
		requestId: string,
		record: WorktreeJournalRecord,
	): Promise<{
		fresh: boolean;
		record: WorktreeJournalRecord;
	}>;
	save(narratorId: string, requestId: string, record: WorktreeJournalRecord): Promise<void>;
}

export function worktreeProposalHash(request: WorktreeCreateRequest): string {
	return createHash("sha256")
		.update(
			JSON.stringify([
				request.expectedRevision,
				request.workspaceKey,
				request.destinationPath,
				request.branch.kind,
				request.branch.name,
				request.baseRef ?? null,
			]),
		)
		.digest("hex");
}

/** Request idempotency/results only; the resource registry protects created worktrees.
 * A pending result never authorizes replaying Git.
 */
export class FileWorktreeJournal implements WorktreeJournal {
	constructor(
		private readonly directory: string,
		private readonly legacyDirectory?: string,
	) {}

	private path(narratorId: string, requestId: string, directory = this.directory) {
		const key = createHash("sha256")
			.update(JSON.stringify([narratorId, requestId]))
			.digest("hex");
		return resolve(directory, `${key}.json`);
	}

	private async prepare() {
		await mkdir(this.directory, { recursive: true, mode: 0o700 });
		// Refuse a replaced receipt directory; Windows cannot open directory descriptors.
		const directoryStat = await lstat(this.directory);
		if (directoryStat.isSymbolicLink() || !directoryStat.isDirectory())
			throw new Error("Invalid worktree receipt directory");
		if (process.platform === "win32") return;
		const handle = await open(this.directory, constants.O_RDONLY | constants.O_NOFOLLOW);
		try {
			if (!(await handle.stat()).isDirectory())
				throw new Error("Invalid worktree receipt directory");
		} finally {
			await handle.close();
		}
	}

	async read(narratorId: string, requestId: string): Promise<WorktreeJournalRecord | null> {
		const current = await this.readFrom(this.directory, narratorId, requestId);
		return current || !this.legacyDirectory
			? current
			: this.readFrom(this.legacyDirectory, narratorId, requestId);
	}

	private async readFrom(
		directoryPath: string,
		narratorId: string,
		requestId: string,
	): Promise<WorktreeJournalRecord | null> {
		const directory = await lstat(directoryPath).catch((cause) => {
			if ((cause as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw cause;
		});
		if (!directory) return null;
		if (directory.isSymbolicLink() || !directory.isDirectory())
			throw new Error("Invalid worktree receipt directory");
		const path = this.path(narratorId, requestId, directoryPath);
		const file = await lstat(path).catch((cause) => {
			if ((cause as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw cause;
		});
		if (!file) return null;
		if (file.isSymbolicLink() || !file.isFile()) throw new Error("Invalid worktree receipt file");
		const existing = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
		try {
			const stat = await existing.stat();
			if (!stat.isFile() || stat.size > MAX_RECORD_BYTES)
				throw new Error("Receipt exceeds byte budget");
			const buffer = Buffer.alloc(MAX_RECORD_BYTES + 1);
			const { bytesRead } = await existing.read(buffer, 0, buffer.length, 0);
			if (bytesRead > MAX_RECORD_BYTES) throw new Error("Receipt exceeds byte budget");
			const record = JSON.parse(buffer.subarray(0, bytesRead).toString("utf8"));
			if (!record || typeof record !== "object" || Array.isArray(record))
				throw new AppError("Invalid worktree receipt", 409, "WORKTREE_RECEIPT_INVALID");
			return record as WorktreeJournalRecord;
		} finally {
			await existing.close();
		}
	}

	async claim(narratorId: string, requestId: string, record: WorktreeJournalRecord) {
		// The old namespace is read-only: replay its receipts, never dispatch Git again.
		if (this.legacyDirectory) {
			const saved = await this.read(narratorId, requestId);
			if (saved) return this.replay(record, saved);
		}
		const encoded = this.encode(record);
		await this.prepare();
		const path = this.path(narratorId, requestId);
		let handle: FileHandle;
		try {
			handle = await open(path, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
			const saved = await this.read(narratorId, requestId);
			if (!saved) throw new Error("Worktree receipt disappeared during replay");
			return this.replay(record, saved);
		}
		try {
			await handle.writeFile(encoded);
			await handle.sync();
		} finally {
			await handle.close();
		}
		await this.syncDirectory();
		return { fresh: true, record };
	}

	private replay(proposed: WorktreeJournalRecord, saved: WorktreeJournalRecord) {
		if (saved.proposalHash !== proposed.proposalHash)
			throw new AppError(
				"requestId already belongs to another proposal",
				409,
				"WORKTREE_REQUEST_CONFLICT",
			);
		return { fresh: false, record: saved };
	}

	private encode(record: WorktreeJournalRecord) {
		const json = JSON.stringify(record);
		if (Buffer.byteLength(json) > MAX_RECORD_BYTES) throw new Error("Receipt exceeds byte budget");
		return json;
	}

	private async syncDirectory() {
		// Windows does not support fsync on directory descriptors. File fsync still applies.
		if (process.platform === "win32") return;
		const handle = await open(this.directory, constants.O_RDONLY | constants.O_NOFOLLOW);
		try {
			await handle.sync();
		} finally {
			await handle.close();
		}
	}

	async save(narratorId: string, requestId: string, record: WorktreeJournalRecord) {
		const encoded = this.encode(record);
		await this.prepare();
		const path = this.path(narratorId, requestId);
		const temporary = `${path}.${randomUUID()}.pending`;
		const handle = await open(
			temporary,
			constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL,
			0o600,
		);
		try {
			await handle.writeFile(encoded);
			await handle.sync();
		} finally {
			await handle.close();
		}
		await rename(temporary, path);
		await this.syncDirectory();
	}
}
