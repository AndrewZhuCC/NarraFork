import type { Database, SQLQueryBindings } from "bun:sqlite";
import type { Dir, Dirent } from "node:fs";
import { constants } from "node:fs";
import { type FileHandle, lstat, open, opendir, readlink, realpath } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve } from "node:path";
import {
	type ProtectionSource,
	protectionSources,
	protectionTables,
} from "../db/resource-protection-index";
import { localPathSemantics } from "../lib/agent/execution/path-semantics";
import { hotSafe } from "../lib/hot-safe";
import { normalizePathForComparison } from "../lib/platform-path";
import { worktreeReceiptHasResourceEffect } from "./narrator-worktree-journal";
import { treeSnapshotKey, treeSnapshotPhysicalDir } from "./tree-snapshot-paths";
import type { LifecycleTarget, ResourceClaim } from "./worktree-lifecycle-guard";

const PAGE = 64;
const MAX_ROW_BYTES = 64 * 1024;
const MAX_RECEIPT_BYTES = 32 * 1024;
const PENDING_RECEIPT =
	/^([a-f0-9]{64}\.json)\.[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}\.pending$/;
const tick = () => new Promise<void>((done) => setTimeout(done, 0));

function check(deadline: number, signal?: AbortSignal) {
	if (performance.now() > deadline || signal?.aborted)
		throw new Error("Protection index budget exceeded");
}
async function bounded<T>(work: Promise<T>, deadline: number, signal?: AbortSignal): Promise<T> {
	try {
		check(deadline, signal);
	} catch (error) {
		void work.catch(() => {});
		throw error;
	}
	let timer: ReturnType<typeof setTimeout> | undefined;
	let abort: (() => void) | undefined;
	try {
		return await Promise.race([
			work,
			new Promise<never>((_, reject) => {
				timer = setTimeout(
					() => reject(new Error("Protection index deadline")),
					Math.max(0, deadline - performance.now()),
				);
				abort = () => reject(new Error("Protection index cancelled"));
				signal?.addEventListener("abort", abort, { once: true });
			}),
		]);
	} finally {
		clearTimeout(timer);
		if (abort) signal?.removeEventListener("abort", abort);
	}
}

/** Same admission semantics as the lifecycle guard, including a dangling symlink leaf. */
export async function canonicalProtectionAdmissionPath(path: string): Promise<string> {
	if (!isAbsolute(path) || path.length > 4096 || path.includes("\0"))
		throw new Error("Invalid resource path");
	let current = path;
	const suffix: string[] = [];
	for (let depth = 0; depth < 64; depth++) {
		try {
			return resolve(await realpath(current), ...suffix);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			const node = await lstat(current).catch((cause) => {
				if ((cause as NodeJS.ErrnoException).code === "ENOENT") return null;
				throw cause;
			});
			if (node?.isSymbolicLink()) {
				const link = await readlink(current);
				if (Buffer.byteLength(link) > 4096) throw new Error("Symlink target exceeds budget");
				current = resolve(dirname(current), link);
				continue;
			}
			const parent = dirname(current);
			if (parent === current) throw error;
			suffix.unshift(localPathSemantics.basename(current));
			current = parent;
		}
	}
	throw new Error("Resource path ancestry exceeded budget");
}
export async function canonicalProtectionPath(path: string): Promise<string> {
	return normalizePathForComparison(await canonicalProtectionAdmissionPath(path));
}

type SourceState = {
	source: ProtectionSource;
	source_id: string;
	revision: number;
};
type PathRow = {
	source: ProtectionSource;
	source_id: string;
	slot: number;
	canonical_path?: string | null;
};
type IndexedClaim = {
	claim: ResourceClaim;
	/** Canonical logical reference; claim.shadowKey retains the persisted physical hash input. */
	shadowKey?: string;
	canonicalPath: string | null;
	deviceId: string;
	alias: number;
};

function validText(value: unknown, max = 4096): value is string {
	return (
		typeof value === "string" && value.length > 0 && value.length <= max && !value.includes("\0")
	);
}
function receiptClaim(record: unknown, id: string): ResourceClaim {
	if (!record || typeof record !== "object" || Array.isArray(record))
		throw new Error("Invalid receipt");
	const row = record as Record<string, unknown>;
	if (
		Object.hasOwn(row, "__proto__") ||
		Object.hasOwn(row, "version") ||
		!validText(row.destination) ||
		!isAbsolute(row.destination) ||
		!validText(row.repositoryKey) ||
		!row.repositoryKey.trim() ||
		(Object.hasOwn(row, "deviceId") &&
			(typeof row.deviceId !== "string" || !/^[-A-Za-z0-9_]{1,128}$/.test(row.deviceId)))
	)
		throw new Error("Invalid receipt");
	return {
		kind: "receipt",
		id,
		deviceId: (row.deviceId as string | undefined) ?? "local",
		path: row.destination,
		repositoryKey: row.repositoryKey,
	};
}

/** Injectable handles keep custom fixtures entirely out of the production database. */
export function createResourceProtectionIndex(database: Database, receiptDirectory: string) {
	let dirtyCursor: [string, string] = ["", ""];
	let receiptEntries: Dir | undefined;
	let receiptEntry: Dirent | null | undefined;
	let receiptImport: Promise<void> | undefined;
	let receiptRetryCursor = "";
	// New shadow-only projections use slot -1. Upgrade old slots in bounded
	// maintenance pages; this cursor is never a global read/admission barrier.
	let shadowProjectionCursor: [string, number] = ["", -2];
	let shadowProjectionComplete = false;
	function shadowProjectionPage(limit: number) {
		return database
			.query(`SELECT source_id, slot, shadow_key IS NOT NULL AS has_shadow FROM resource_protection_paths
			WHERE source = 'chapter' AND (source_id, slot) > (?, ?)
			ORDER BY source_id, slot LIMIT ?`)
			.all(...shadowProjectionCursor, limit) as {
			source_id: string;
			slot: number;
			has_shadow: number;
		}[];
	}
	function legacyShadowProjection(row: { slot: number; has_shadow: number }): boolean {
		return row.slot >= 0 && row.has_shadow !== 0;
	}
	function invalidateLegacyShadowProjections() {
		if (shadowProjectionComplete) return;
		const rows = shadowProjectionPage(PAGE);
		database.transaction(() => {
			for (const row of rows) {
				if (!legacyShadowProjection(row)) continue;
				const result = database
					.query(`UPDATE resource_protection_sources SET dirty = 1, revision = revision + 1
					WHERE source = 'chapter' AND source_id = ? AND dirty = 0`)
					.run(row.source_id);
				if (result.changes) changedSource("chapter");
			}
		})();
		const last = rows.at(-1);
		if (last) shadowProjectionCursor = [last.source_id, last.slot];
		if (rows.length < PAGE) shadowProjectionComplete = true;
	}
	// Project bounded scalars before crossing the SQLite/JS boundary, including malformed legacy rows.
	const smallText = (column: string, max = 4096) =>
		`CASE WHEN length(cast(${column} AS blob)) > ${max} THEN char(0) ELSE ${column} END`;
	function requireSmall(row: object | null): void {
		if (row && Object.values(row).some((value) => value === "\0"))
			throw new Error("Protection source scalar exceeds row budget");
	}
	function readRaw(source: ProtectionSource, id: string): ResourceClaim[] {
		if (!validText(id)) throw new Error("Invalid protection source key");
		if (source === "receipt") return [];
		if (source === "registry") {
			const row = database
				.query(`SELECT ${smallText("id")} AS id, ${smallText("device_id", 256)} AS deviceId,
				${smallText("worktree_path")} AS path, ${smallText("repository_key")} AS repositoryKey,
				${smallText("scope_project_id")} AS scopeProjectId, ${smallText("state", 256)} AS state
				FROM narrator_worktree_resources WHERE id = ?`)
				.get(id) as Omit<ResourceClaim, "kind"> | null;
			requireSmall(row);
			return row ? [{ kind: "registry", ...row }] : [];
		}
		if (source === "chapter") {
			const row = database
				.query(`SELECT ${smallText("id")} AS id, ${smallText("worktree_path")} AS path,
				${smallText("snapshot_shadow_key", 8193)} AS shadowKey FROM chapters WHERE id = ?`)
				.get(id) as { id: string; path: string | null; shadowKey: string | null } | null;
			requireSmall(row);
			return row
				? [
						{
							kind: "chapter",
							id: row.id,
							...(row.path ? { path: row.path } : {}),
							...(row.shadowKey ? { shadowKey: row.shadowKey } : {}),
						},
					]
				: [];
		}
		const row = database
			.query(`SELECT ${smallText("id")} AS id, ${smallText("chapter_id")} AS chapter_id,
			${smallText("cwd")} AS cwd, ${smallText("default_device_id", 256)} AS default_device_id, workspace_revision,
			CASE WHEN length(cast(workspace_context AS blob)) <= ${MAX_ROW_BYTES} THEN workspace_context END AS context,
			length(cast(workspace_context AS blob)) AS context_bytes FROM narrators WHERE id = ?`)
			.get(id) as {
			id: string;
			chapter_id: string | null;
			cwd: string | null;
			default_device_id: string | null;
			workspace_revision: number;
			context: string | null;
			context_bytes: number | null;
		} | null;
		if (!row) return [];
		requireSmall(row);
		if ((row.context_bytes ?? 0) > MAX_ROW_BYTES)
			throw new Error("Workspace context exceeds row budget");
		const context = row.context === null ? null : JSON.parse(row.context);
		const contextCwd = context?.cwd ?? null;
		const contextDevice = context?.deviceId ?? null;
		for (const device of [row.default_device_id, contextDevice])
			if (device !== null && !validText(device, 256))
				throw new Error("Legacy claim device is unverifiable");
		for (const path of [row.cwd, contextCwd])
			if (path !== null && !validText(path)) throw new Error("Legacy claim cwd is unverifiable");
		const rawDevice =
			row.cwd && isAbsolute(row.cwd)
				? "local"
				: (row.default_device_id ?? contextDevice ?? "local");
		const effectiveDevice =
			contextDevice ??
			(row.default_device_id && row.default_device_id !== "local" ? null : "local");
		if (contextCwd && !effectiveDevice) throw new Error("Legacy context device is unverifiable");
		const base = {
			kind: "narrator" as const,
			id: row.id,
			chapterId: row.chapter_id,
			revision: row.workspace_revision,
			legacyChapterCwd: !!row.chapter_id && row.workspace_revision === 0,
		};
		const claims: ResourceClaim[] = [];
		if (row.cwd)
			claims.push({
				...base,
				path: rawDevice === "local" ? localPathSemantics.resolve(process.cwd(), row.cwd) : row.cwd,
				deviceId: rawDevice,
			});
		if (contextCwd) {
			const repositoryKey = context?.git?.repositoryKey;
			if (repositoryKey != null && !validText(repositoryKey))
				throw new Error("Invalid repository key");
			claims.push({
				...base,
				deviceId: effectiveDevice ?? "local",
				path:
					effectiveDevice === "local"
						? localPathSemantics.resolve(process.cwd(), contextCwd)
						: contextCwd,
				...(repositoryKey ? { repositoryKey } : {}),
			});
		}
		return claims;
	}

	async function indexedPath(
		claim: ResourceClaim,
		path: string | undefined,
		deviceId: string,
		deadline: number,
		signal?: AbortSignal,
	): Promise<IndexedClaim> {
		if (!validText(deviceId, 256)) throw new Error("Invalid resource device");
		if (!path) return { claim, canonicalPath: null, deviceId, alias: 0 };
		if (!validText(path)) throw new Error("Invalid resource path");
		if (deviceId !== "local") return { claim, canonicalPath: path, deviceId, alias: 0 };
		const canonicalPath = await bounded(canonicalProtectionPath(path), deadline, signal);
		const metadata = await bounded(
			lstat(path).catch((error) => {
				if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
				throw error;
			}),
			deadline,
			signal,
		);
		return {
			claim,
			canonicalPath,
			deviceId,
			alias:
				canonicalPath !== normalizePathForComparison(path) || metadata?.isSymbolicLink() ? 1 : 0,
		};
	}
	async function indexed(
		claim: ResourceClaim,
		deadline: number,
		signal?: AbortSignal,
	): Promise<IndexedClaim[]> {
		if (Buffer.byteLength(JSON.stringify(claim)) > MAX_ROW_BYTES)
			throw new Error("Claim exceeds row budget");
		const { shadowKey, ...direct } = claim;
		const rows: IndexedClaim[] = [];
		if (shadowKey) {
			const parts = shadowKey.split("\0");
			const [deviceId, logicalPath] = parts;
			if (parts.length !== 2 || !deviceId || !logicalPath || !isAbsolute(logicalPath))
				throw new Error("Invalid shadow reference");
			const reference = { ...claim, path: undefined };
			const logical = await indexedPath(reference, logicalPath, deviceId, deadline, signal);
			const physical = await indexedPath(
				reference,
				treeSnapshotPhysicalDir(deviceId, logicalPath),
				deviceId,
				deadline,
				signal,
			);
			rows.push({
				...physical,
				shadowKey: treeSnapshotKey(deviceId, logical.canonicalPath ?? logicalPath),
				alias: logical.alias || physical.alias,
			});
		}
		if (claim.path || !shadowKey)
			rows.push(await indexedPath(direct, claim.path, claim.deviceId ?? "local", deadline, signal));
		return rows;
	}

	function changedSource(source: ProtectionSource) {
		database
			.query(
				"UPDATE resource_protection_sources SET revision = revision + 1 WHERE source = ? AND source_id = ''",
			)
			.run(source);
	}
	function save(
		source: ProtectionSource,
		id: string,
		revision: number,
		rows: IndexedClaim[],
	): boolean {
		return database.transaction(() => {
			const state = database
				.query(
					"SELECT revision, dirty FROM resource_protection_sources WHERE source = ? AND source_id = ?",
				)
				.get(source, id) as { revision: number; dirty: number } | null;
			if (!state || state.revision !== revision) return false;
			const old = database
				.query(
					"SELECT slot, device_id, canonical_path, shadow_key, alias_sensitive, claim_json FROM resource_protection_paths WHERE source = ? AND source_id = ? ORDER BY slot LIMIT ?",
				)
				.all(source, id, PAGE + 1) as {
				slot: number;
				device_id: string;
				canonical_path: string | null;
				shadow_key: string | null;
				alias_sensitive: number;
				claim_json: string;
			}[];
			const unchanged =
				old.length === rows.length &&
				rows.every(
					(row, i) =>
						old[i]?.slot === (row.shadowKey ? -1 : i) &&
						old[i]?.device_id === row.deviceId &&
						old[i]?.canonical_path === row.canonicalPath &&
						old[i]?.shadow_key === (row.shadowKey ?? null) &&
						old[i]?.alias_sensitive === row.alias &&
						old[i]?.claim_json === JSON.stringify(row.claim),
				);
			if (!state.dirty && unchanged) return true;
			database
				.query(
					"UPDATE resource_protection_sources SET dirty = 0 WHERE source = ? AND source_id = ?",
				)
				.run(source, id);
			changedSource(source);
			if (unchanged) return true;
			database
				.query("DELETE FROM resource_protection_paths WHERE source = ? AND source_id = ?")
				.run(source, id);
			for (const [slot, row] of rows.entries())
				database
					.query(`INSERT INTO resource_protection_paths
				(source, source_id, slot, device_id, canonical_path, repository_key, shadow_key, scope_project_id, alias_sensitive, claim_json)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
					.run(
						source,
						id,
						row.shadowKey ? -1 : slot,
						row.deviceId,
						row.canonicalPath,
						row.claim.repositoryKey ?? null,
						row.shadowKey ?? null,
						row.claim.scopeProjectId ?? null,
						row.alias,
						JSON.stringify(row.claim),
					);
			return true;
		})();
	}

	async function readReceipt(
		filePath: string,
		deadline: number,
		signal?: AbortSignal,
	): Promise<ResourceClaim | null | false> {
		check(deadline, signal);
		let file: FileHandle;
		try {
			file = await open(filePath, constants.O_RDONLY | constants.O_NOFOLLOW);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw error;
		}
		try {
			check(deadline, signal);
			const before = await file.stat();
			check(deadline, signal);
			if (!before.isFile() || before.size > MAX_RECEIPT_BYTES)
				throw new Error("Receipt exceeds row budget");
			const buffer = Buffer.alloc(MAX_RECEIPT_BYTES + 1);
			const { bytesRead } = await file.read(buffer, 0, buffer.length, 0);
			check(deadline, signal);
			const after = await file.stat();
			check(deadline, signal);
			if (
				bytesRead > MAX_RECEIPT_BYTES ||
				before.size !== after.size ||
				before.mtimeMs !== after.mtimeMs ||
				before.ctimeMs !== after.ctimeMs
			)
				throw new Error("Receipt changed while reading");
			const record: unknown = JSON.parse(buffer.subarray(0, bytesRead).toString("utf8"));
			if (
				!record ||
				typeof record !== "object" ||
				Array.isArray(record) ||
				Object.hasOwn(record, "__proto__") ||
				Object.hasOwn(record, "version")
			)
				throw new Error("Invalid receipt");
			// An explicit no-dispatch result is durable retry metadata, not a claim.
			// Its invalid original destination need not be canonicalizable.
			if (!worktreeReceiptHasResourceEffect(record)) return false;
			return receiptClaim(record, filePath);
		} finally {
			await file.close();
		}
	}

	/** Register a durable retry before consuming the current directory entry. */
	function registerReceipt(filePath: string): number | null {
		const state = database
			.query(
				"SELECT revision, dirty FROM resource_protection_sources WHERE source = 'receipt' AND source_id = ?",
			)
			.get(filePath) as { revision: number; dirty: number } | null;
		if (state && !state.dirty) return null;
		database
			.query(`INSERT INTO resource_protection_sources (source, source_id)
			VALUES ('receipt', ?) ON CONFLICT DO NOTHING`)
			.run(filePath);
		return state?.revision ?? 1;
	}
	/** Legacy files are imported once. Existing pins never follow later journal rewrites/deletes. */
	async function importReceipt(
		filePath: string,
		revision: number,
		deadline: number,
		signal?: AbortSignal,
	) {
		try {
			const claim = await readReceipt(filePath, deadline, signal);
			if (claim === null) throw new Error("Legacy receipt disappeared before import");
			save(
				"receipt",
				filePath,
				revision,
				claim === false ? [] : await indexed(claim, deadline, signal),
			);
		} catch {
			check(deadline, signal); // Retry bad finals after reaching EOF, without starving later files.
		}
	}
	async function runLegacyImport(deadline: number, signal?: AbortSignal) {
		const progress = database
			.query(
				"SELECT cursor, complete FROM resource_protection_sources WHERE source = 'receipt' AND source_id = ''",
			)
			.get() as { cursor: string | null; complete: number } | null;
		if (!progress) throw new Error("Protection triggers not installed");
		if (progress.complete) return;
		if (progress.cursor !== "eof") {
			if (!receiptEntries) {
				const metadata = await lstat(receiptDirectory).catch((error) => {
					if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
					throw error;
				});
				check(deadline, signal);
				if (metadata && (!metadata.isDirectory() || metadata.isSymbolicLink()))
					throw new Error("Unsafe receipt directory");
				if (metadata) receiptEntries = await opendir(receiptDirectory);
			}
			for (let count = 0; count < PAGE; count++) {
				check(deadline, signal);
				if (receiptEntry === undefined) {
					try {
						receiptEntry = receiptEntries ? await receiptEntries.read() : null;
						check(deadline, signal);
					} catch (error) {
						const entries = receiptEntries;
						receiptEntries = undefined;
						receiptEntry = undefined;
						await entries?.close().catch(() => {});
						throw error;
					}
				}
				check(deadline, signal);
				if (!receiptEntry) {
					const entries = receiptEntries;
					receiptEntries = undefined;
					await entries?.close();
					check(deadline, signal);
					database
						.query(
							"UPDATE resource_protection_sources SET cursor = 'eof' WHERE source = 'receipt' AND source_id = ''",
						)
						.run();
					receiptEntry = undefined;
					break;
				}
				if (PENDING_RECEIPT.test(receiptEntry.name)) {
					receiptEntry = undefined;
					continue;
				}
				const filePath = join(receiptDirectory, receiptEntry.name);
				const revision = registerReceipt(filePath);
				receiptEntry = undefined;
				if (revision !== null) await importReceipt(filePath, revision, deadline, signal);
			}
			// Keep the directory handle across bounded batches; restart safely skips imported rows.
			return;
		}
		const failed = database
			.query(`SELECT source_id FROM resource_protection_sources
			WHERE dirty = 1 AND source = 'receipt' AND source_id > ? ORDER BY source_id LIMIT ?`)
			.all(receiptRetryCursor, PAGE) as { source_id: string }[];
		if (!failed.length) receiptRetryCursor = "";
		for (const row of failed) {
			check(deadline, signal);
			try {
				const revision = registerReceipt(row.source_id);
				if (revision !== null) await importReceipt(row.source_id, revision, deadline, signal);
			} finally {
				receiptRetryCursor = row.source_id;
			}
		}
		if (
			!database
				.query(
					"SELECT 1 FROM resource_protection_sources WHERE dirty = 1 AND source = 'receipt' AND source_id != '' LIMIT 1",
				)
				.get()
		)
			database
				.query(
					"UPDATE resource_protection_sources SET complete = 1, revision = revision + 1 WHERE source = 'receipt' AND source_id = '' AND complete = 0",
				)
				.run();
	}

	async function importLegacyReceipts(deadline: number, signal?: AbortSignal) {
		// A timed-out filesystem operation (including close) still owns its handles until it settles.
		// Pause only legacy import instead of reopening it or blocking the three business sources.
		if (receiptImport) return;
		const work = runLegacyImport(deadline, signal);
		receiptImport = work;
		void work
			.finally(() => {
				if (receiptImport === work) receiptImport = undefined;
			})
			.catch(() => {});
		await bounded(work, deadline, signal);
	}

	async function maintain(
		deadline = performance.now() + 1000,
		signal?: AbortSignal,
	): Promise<void> {
		check(deadline, signal);
		invalidateLegacyShadowProjections();
		for (const source of ["narrator", "chapter", "registry"] as const) {
			check(deadline, signal);
			const progress = database
				.query(
					"SELECT cursor, complete FROM resource_protection_sources WHERE source = ? AND source_id = ''",
				)
				.get(source) as { cursor: string | null; complete: number } | null;
			if (!progress) throw new Error("Protection triggers not installed");
			if (progress.complete) continue;
			const ids = database
				.query(`SELECT id FROM ${protectionTables[source].table} WHERE id > ? ORDER BY id LIMIT ?`)
				.all(progress.cursor ?? "", PAGE) as { id: string }[];
			database.transaction(() => {
				for (const { id } of ids)
					database
						.query(`INSERT INTO resource_protection_sources (source, source_id)
					VALUES (?, ?) ON CONFLICT DO NOTHING`)
						.run(source, id);
				database
					.query(
						"UPDATE resource_protection_sources SET cursor = ?, complete = ?, revision = revision + 1 WHERE source = ? AND source_id = ''",
					)
					.run(ids.at(-1)?.id ?? progress.cursor, ids.length < PAGE ? 1 : 0, source);
			})();
			await tick();
		}
		const dirty = database
			.query(`SELECT source, source_id, revision FROM resource_protection_sources
			WHERE dirty = 1 AND source_id != '' AND source != 'receipt' AND (source, source_id) > (?, ?)
			ORDER BY source, source_id LIMIT ?`)
			.all(...dirtyCursor, PAGE) as SourceState[];
		if (!dirty.length) dirtyCursor = ["", ""];
		for (const state of dirty) {
			check(deadline, signal);
			try {
				save(
					state.source,
					state.source_id,
					state.revision,
					(
						await Promise.all(
							readRaw(state.source, state.source_id).map((claim) =>
								indexed(claim, deadline, signal),
							),
						)
					).flat(),
				);
			} catch {
				check(deadline, signal); /* Keep invalid facts dirty and fail closed on reads. */
			} finally {
				dirtyCursor = [state.source, state.source_id];
			}
		}
		await importLegacyReceipts(deadline, signal);
	}

	function pathQueries(
		target: LifecycleTarget,
	): { clause: string; values: SQLQueryBindings[]; pathOrder?: boolean }[] {
		const device = target.deviceId ?? "local";
		const queries: { clause: string; values: SQLQueryBindings[]; pathOrder?: boolean }[] = [];
		if (target.scopeProjectId)
			queries.push({ clause: "scope_project_id = ?", values: [target.scopeProjectId] });
		if (target.shadowKey) queries.push({ clause: "shadow_key = ?", values: [target.shadowKey] });
		if (target.repositoryKey)
			queries.push({
				clause: "device_id = ? AND repository_key = ?",
				values: [device, target.repositoryKey],
			});
		if (target.path) {
			let path = target.path;
			for (let depth = 0; depth < 65; depth++) {
				queries.push({ clause: "device_id = ? AND canonical_path = ?", values: [device, path] });
				const parent = normalizePathForComparison(dirname(path));
				if (parent === path) break;
				if (depth === 64) throw new Error("Target ancestry exceeds budget");
				path = parent;
			}
			const prefix = target.path.endsWith("/") ? target.path : `${target.path}/`;
			queries.push({
				clause: "device_id = ? AND canonical_path >= ? AND canonical_path < ?",
				values: [device, prefix, `${prefix.slice(0, -1)}0`],
				pathOrder: true,
			});
		}
		return queries;
	}
	function matches(targets: readonly LifecycleTarget[], row: IndexedClaim): boolean {
		return targets.some((target) => {
			if (target.scopeProjectId && target.scopeProjectId === row.claim.scopeProjectId) return true;
			if (target.shadowKey && target.shadowKey === row.shadowKey) return true;
			if ((target.deviceId ?? "local") !== row.deviceId) return false;
			if (target.repositoryKey && target.repositoryKey === row.claim.repositoryKey) return true;
			const a = target.path,
				b = row.canonicalPath;
			return !!(
				a &&
				b &&
				(a === b ||
					a.startsWith(b.endsWith("/") ? b : `${b}/`) ||
					b.startsWith(a.endsWith("/") ? a : `${a}/`))
			);
		});
	}

	async function read(
		targets: readonly LifecycleTarget[],
		deadline: number,
		signal?: AbortSignal,
		includeLegacyReceipts = true,
	): Promise<{ complete: boolean; claims: AsyncIterable<ResourceClaim> }> {
		const empty = async function* () {};
		check(deadline, signal);
		const sources = includeLegacyReceipts
			? protectionSources
			: protectionSources.filter((source) => source !== "receipt");
		const sourceFilter = `source IN (${sources.map(() => "?").join(", ")})`;
		const progress = database
			.query(
				`SELECT source, complete FROM resource_protection_sources WHERE ${sourceFilter} AND source_id = ''`,
			)
			.all(...sources) as { source: ProtectionSource; complete: number }[];
		if (progress.length !== sources.length) return { complete: false, claims: empty() };
		if (progress.some((state) => state.source === "receipt" && !state.complete))
			return { complete: false, claims: empty() };
		const bootstrapOverlay: { source: Exclude<ProtectionSource, "receipt">; id: string }[] = [];
		// Small fresh databases can be proved completely in memory, without mutating bootstrap state.
		// LIMIT n+1 is a completeness proof, never a truncated inventory interpreted as empty.
		for (const state of progress) {
			if (state.complete || state.source === "receipt") continue;
			const ids = database
				.query(`SELECT id FROM ${protectionTables[state.source].table} ORDER BY id LIMIT ?`)
				.all(PAGE + 1) as { id: string }[];
			if (ids.length > PAGE) return { complete: false, claims: empty() };
			for (const { id } of ids) bootstrapOverlay.push({ source: state.source, id });
		}
		const version = () =>
			JSON.stringify(
				database
					.query(
						`SELECT source, revision FROM resource_protection_sources WHERE ${sourceFilter} AND source_id = '' ORDER BY source`,
					)
					.all(...sources),
			);
		const initialVersion = version();
		const bootstrapKeys = new Set(bootstrapOverlay.map(({ source, id }) => `${source}\0${id}`));
		function candidateClaim(row: PathRow): ResourceClaim {
			const saved = database
				.query(`SELECT ${smallText("claim_json", MAX_ROW_BYTES)} AS claim_json FROM resource_protection_paths
				WHERE source = ? AND source_id = ? AND slot = ?`)
				.get(row.source, row.source_id, row.slot) as { claim_json: string } | null;
			requireSmall(saved);
			if (!saved) throw new Error("Protection candidate disappeared");
			return JSON.parse(saved.claim_json) as ResourceClaim;
		}
		// LIMIT is applied to raw indexed candidates, never after a dirty-row anti-join.
		function skipCandidate(row: PathRow): boolean {
			if (row.source === "receipt") return !includeLegacyReceipts;
			return !!(
				database
					.query("SELECT dirty FROM resource_protection_sources WHERE source = ? AND source_id = ?")
					.get(row.source, row.source_id) as { dirty: number } | null
			)?.dirty;
		}
		async function* claims(): AsyncGenerator<ResourceClaim> {
			for (const { source, id } of bootstrapOverlay) {
				for (const claim of readRaw(source, id))
					for (const row of await indexed(claim, deadline, signal))
						if (matches(targets, row)) yield row.claim;
			}
			const seen = new Set<string>();
			for (const target of targets)
				for (const query of pathQueries(target)) {
					const order = query.pathOrder
						? "canonical_path, source, source_id, slot"
						: "source, source_id, slot";
					let cursor: SQLQueryBindings[] = query.pathOrder ? ["", "", "", -1] : ["", "", -1];
					const cursorParameters = query.pathOrder ? "?, ?, ?, ?" : "?, ?, ?";
					while (true) {
						check(deadline, signal);
						const rows = database
							.query(`SELECT source, source_id, slot, canonical_path FROM resource_protection_paths
						WHERE ${query.clause}
						AND (${order}) > (${cursorParameters}) ORDER BY ${order} LIMIT ?`)
							.all(...query.values, ...cursor, PAGE) as PathRow[];
						for (const row of rows) {
							if (skipCandidate(row)) continue;
							const key = `${row.source}\0${row.source_id}\0${row.slot}`;
							if (!seen.has(key)) {
								seen.add(key);
								const claim = candidateClaim(row);
								if (row.slot >= 0 && claim.shadowKey) {
									for (const projection of await indexed(claim, deadline, signal))
										if (matches(targets, projection)) yield projection.claim;
								} else yield claim;
							}
						}
						if (rows.length < PAGE) break;
						const last = rows.at(-1);
						if (!last) break;
						if (query.pathOrder && last.canonical_path == null)
							throw new Error("Unverifiable path-range cursor");
						cursor = query.pathOrder
							? [last.canonical_path ?? "", last.source, last.source_id, last.slot]
							: [last.source, last.source_id, last.slot];
						await tick();
					}
				}
			// Raw-PK overlay: never repair the database from the guard's read path.
			let cursor: [string, string] = ["", ""];
			while (true) {
				check(deadline, signal);
				const states = database
					.query(`SELECT source, source_id, revision FROM resource_protection_sources
					WHERE dirty = 1 AND source IN ('narrator', 'chapter', 'registry') AND source_id != '' AND (source, source_id) > (?, ?) ORDER BY source, source_id LIMIT ?`)
					.all(...cursor, PAGE) as SourceState[];
				for (const state of states) {
					if (bootstrapKeys.has(`${state.source}\0${state.source_id}`)) continue;
					for (const claim of readRaw(state.source, state.source_id))
						for (const row of await indexed(claim, deadline, signal))
							if (matches(targets, row)) yield row.claim;
				}
				if (states.length < PAGE) break;
				const last = states.at(-1);
				if (!last) break;
				cursor = [last.source, last.source_id];
				await tick();
			}
			// Before maintenance upgrades old shadow slots, only physical shadow
			// deletion needs their missing hash-derived path projection. Checkout
			// reads and cold births do not depend on unrelated migration history.
			if (
				!shadowProjectionComplete &&
				includeLegacyReceipts &&
				targets.some((target) => target.path)
			) {
				const storage = await bounded(
					canonicalProtectionPath(dirname(treeSnapshotPhysicalDir("local", "/"))),
					deadline,
					signal,
				);
				const physicalDeletion = targets.some(
					(target) =>
						(target.deviceId ?? "local") === "local" &&
						target.path &&
						(localPathSemantics.contains(storage, target.path) ||
							localPathSemantics.contains(target.path, storage)),
				);
				if (physicalDeletion) {
					let oldCursor: [string, number] = ["", -2];
					let foundLegacy = false;
					while (true) {
						check(deadline, signal);
						const rows = database
							.query(`SELECT 'chapter' AS source, source_id, slot, shadow_key IS NOT NULL AS has_shadow
							FROM resource_protection_paths WHERE source = 'chapter' AND (source_id, slot) > (?, ?)
							ORDER BY source_id, slot LIMIT ?`)
							.all(...oldCursor, PAGE) as (PathRow & { has_shadow: number })[];
						for (const row of rows) {
							if (!legacyShadowProjection(row)) continue;
							foundLegacy = true;
							if (skipCandidate(row) || seen.has(`${row.source}\0${row.source_id}\0${row.slot}`))
								continue;
							for (const projection of await indexed(candidateClaim(row), deadline, signal))
								if (matches(targets, projection)) yield projection.claim;
						}
						if (rows.length < PAGE) break;
						const last = rows.at(-1);
						if (!last) break;
						oldCursor = [last.source_id, last.slot];
						await tick();
					}
					if (!foundLegacy) shadowProjectionComplete = true;
				}
			}
			let aliasCursor: [string, string, number] = ["", "", -1];
			while (true) {
				check(deadline, signal);
				const rows = database
					.query(`SELECT source, source_id, slot, canonical_path FROM resource_protection_paths
					WHERE alias_sensitive = 1
					AND (source, source_id, slot) > (?, ?, ?) ORDER BY source, source_id, slot LIMIT ?`)
					.all(...aliasCursor, PAGE) as PathRow[];
				for (const row of rows) {
					if (skipCandidate(row)) continue;
					const claim = candidateClaim(row);
					for (const projection of await indexed(claim, deadline, signal))
						if (matches(targets, projection)) yield projection.claim;
				}
				if (rows.length < PAGE) break;
				const last = rows.at(-1);
				if (!last) break;
				aliasCursor = [last.source, last.source_id, last.slot];
				await tick();
			}
			check(deadline, signal);
			if (version() !== initialVersion)
				throw new Error("Protection facts changed during inspection");
		}
		return { complete: true, claims: claims() };
	}

	return { read, maintain };
}

let singleton: ReturnType<typeof createResourceProtectionIndex> | undefined;
async function productionIndex() {
	if (singleton) return singleton;
	const { sqlite } = await import("../db");
	const { narraforkDir } = await import("../lib/settings");
	singleton = createResourceProtectionIndex(sqlite, join(narraforkDir, "worktree-requests"));
	return singleton;
}
let lastWarning = -Infinity;
let suppressedWarnings = 0;
async function warnUnavailable(error: unknown): Promise<void> {
	if (performance.now() - lastWarning < 60_000) {
		suppressedWarnings++;
		return;
	}
	lastWarning = performance.now();
	const { logger } = await import("../lib/logger");
	logger.warn("Resource protection evidence unavailable", {
		reason: error instanceof Error ? error.message : String(error),
		suppressedWarnings,
	});
	suppressedWarnings = 0;
}
export async function readProtectionClaims(
	targets: readonly LifecycleTarget[],
	deadline: number,
	signal?: AbortSignal,
	includeLegacyReceipts = true,
) {
	try {
		const result = await (await productionIndex()).read(
			targets,
			deadline,
			signal,
			includeLegacyReceipts,
		);
		if (!result.complete) await warnUnavailable(new Error("Protection index bootstrap incomplete"));
		return {
			complete: result.complete,
			claims: (async function* () {
				try {
					yield* result.claims;
				} catch (error) {
					await warnUnavailable(error);
					throw error;
				}
			})(),
		};
	} catch (error) {
		await warnUnavailable(error);
		throw error;
	}
}
export async function maintainProtectionIndex(
	deadline = performance.now() + 1000,
	signal?: AbortSignal,
) {
	try {
		await (await productionIndex()).maintain(deadline, signal);
	} catch (error) {
		await warnUnavailable(error);
		throw error;
	}
}
const maintenance = hotSafe("resource-protection-index-maintenance", () => ({
	timer: undefined as ReturnType<typeof setTimeout> | undefined,
}));
export function startProtectionIndexMaintenance(): void {
	if (maintenance.timer || process.env.NARRAFORK_TEST === "1") return;
	const run = async () => {
		try {
			await maintainProtectionIndex();
		} catch {
			/* Backlog or invalid evidence stays dirty; destructive reads fail closed. */
		}
		maintenance.timer = setTimeout(run, 1000);
		maintenance.timer.unref();
	};
	maintenance.timer = setTimeout(run, 0);
	maintenance.timer.unref();
}
