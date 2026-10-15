import { Worker } from "node:worker_threads";
import { AppError } from "@server/lib/errors";
import { hotSafe } from "@server/lib/hot-safe";
import { logger } from "@server/lib/logger";
import type {
	TokenRepairApplyResult,
	TokenRepairCandidatePage,
	TokenRepairJob,
	TokenRepairProbe,
	TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import type { TokenRepairStoreConfig } from "./usage-token-repair-store";
import {
	tokenRepairWorkerEntry,
	tokenRepairWorkerSpecifiers,
} from "./usage-token-repair-worker-paths";

type Command = {
	op: string;
	jobId?: string;
	userId?: string;
	includeReviewed?: boolean;
	protocolOverrides?: TokenRepairProtocolOverride[];
	sourceJobId?: string;
	cursor?: string;
	candidateIds?: string[];
	config?: TokenRepairStoreConfig;
	directory?: string;
};
const unavailable = () =>
	new AppError(
		"Historical token repair is unavailable. Inspect the persisted job before retrying a write.",
		503,
		"TOKEN_REPAIR_UNAVAILABLE",
	);

export class TokenRepairClient {
	private worker: Worker | null = null;
	private startup: Promise<void> | null = null;
	private sequence = 0;
	private closed = false;
	private pending = new Map<
		number,
		{
			resolve(value: unknown): void;
			reject(error: unknown): void;
			timer: ReturnType<typeof setTimeout>;
		}
	>();
	private probeCache: { at: number; value: TokenRepairProbe } | null = null;
	private probePending: Promise<TokenRepairProbe> | null = null;
	constructor(
		private config: TokenRepairStoreConfig,
		private directory: string,
	) {}
	private fail(): void {
		for (const call of this.pending.values()) {
			clearTimeout(call.timer);
			call.reject(unavailable());
		}
		this.pending.clear();
		this.probeCache = null;
		this.startup = null;
		const worker = this.worker;
		this.worker = null;
		void worker?.terminate().catch(() => {});
	}
	private async initialize(): Promise<void> {
		if (this.closed) throw unavailable();
		if (this.startup) return this.startup;
		this.startup = (async () => {
			for (const specifier of tokenRepairWorkerSpecifiers("usage-token-repair-worker")) {
				let worker: Worker | undefined;
				let ready = false;
				try {
					worker = new Worker(tokenRepairWorkerEntry(specifier));
					await new Promise<void>((resolve, reject) => {
						const timer = setTimeout(() => reject(unavailable()), 5000);
						worker?.once("message", (message: { ready?: string }) => {
							clearTimeout(timer);
							if (message.ready === "token-repair-v1") {
								ready = true;
								resolve();
							} else reject(unavailable());
						});
						worker?.once("error", () => {
							clearTimeout(timer);
							reject(unavailable());
						});
						worker?.once("exit", () => {
							clearTimeout(timer);
							reject(unavailable());
						});
					});
					this.worker = worker;
					worker.on("message", (message: { id?: number; value?: unknown; error?: string }) => {
						if (typeof message.id !== "number") return;
						const pending = this.pending.get(message.id);
						if (!pending) return;
						this.pending.delete(message.id);
						clearTimeout(pending.timer);
						if (message.error) pending.reject(unavailable());
						else pending.resolve(message.value);
					});
					worker.on("error", () => {
						if (this.worker === worker) this.fail();
					});
					worker.on("exit", () => {
						if (this.worker === worker) this.fail();
					});
					worker.unref();
					await this.dispatch(
						{ op: "init", config: this.config, directory: this.directory },
						10_000,
					);
					return;
				} catch {
					await worker?.terminate();
					if (ready) throw unavailable(); // Never replay a dispatched initialization against another worker.
				}
			}
			throw unavailable();
		})().catch((error: unknown) => {
			this.fail();
			throw error;
		});
		return this.startup;
	}
	private dispatch(command: Command, timeout = 15_000): Promise<unknown> {
		if (!this.worker || this.pending.size >= 32) return Promise.reject(unavailable());
		const id = ++this.sequence;
		return new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				this.pending.delete(id);
				reject(unavailable());
			}, timeout);
			this.pending.set(id, { resolve, reject, timer });
			try {
				this.worker?.postMessage({ ...command, id });
			} catch {
				clearTimeout(timer);
				this.pending.delete(id);
				reject(unavailable());
			}
		});
	}
	private async call<T>(command: Command, timeout?: number): Promise<T> {
		await this.initialize();
		const started = performance.now();
		try {
			return (await this.dispatch(command, timeout)) as T;
		} finally {
			const elapsedMs = performance.now() - started;
			if (elapsedMs > 250)
				logger.warn("Slow historical token repair operation", { operation: command.op, elapsedMs });
		}
	}
	async probe(): Promise<TokenRepairProbe> {
		if (this.probeCache && Date.now() - this.probeCache.at < 30_000) return this.probeCache.value;
		if (this.probePending) return this.probePending;
		this.probePending = this.call<TokenRepairProbe>({ op: "probe" })
			.then((value) => {
				this.probeCache = { at: Date.now(), value };
				return value;
			})
			.finally(() => {
				this.probePending = null;
			});
		return this.probePending;
	}
	latestJob() {
		return this.call<TokenRepairJob | null>({ op: "latest" });
	}
	getJob(jobId: string) {
		return this.call<TokenRepairJob | null>({ op: "get", jobId });
	}
	listCandidates(jobId: string, cursor?: string) {
		return this.call<TokenRepairCandidatePage>({ op: "list", jobId, cursor });
	}
	async start(
		userId: string,
		includeReviewed: boolean,
		protocolOverrides?: TokenRepairProtocolOverride[],
		sourceJobId?: string,
	) {
		this.probeCache = null;
		return this.call<TokenRepairJob>({
			op: "start",
			userId,
			includeReviewed,
			protocolOverrides,
			sourceJobId,
		});
	}
	async cancel(jobId: string) {
		this.probeCache = null;
		return this.call<TokenRepairJob | null>({ op: "cancel", jobId });
	}
	async apply(jobId: string, candidateIds: string[], userId: string) {
		this.probeCache = null;
		try {
			return await this.call<TokenRepairApplyResult>(
				{ op: "apply", jobId, candidateIds, userId },
				300_000,
			);
		} finally {
			this.probeCache = null;
		}
	}
	async close(): Promise<void> {
		if (this.closed) return;
		if (this.worker) {
			try {
				await this.dispatch({ op: "close" }, 10_000);
			} catch {
				/* Unfinished jobs require an explicit end and a new analysis; never replay writes. */
			}
		}
		this.closed = true;
		this.fail();
	}
}

const state = hotSafe<{ client?: TokenRepairClient }>("usage-token-repair-client-v1", () => ({}));
async function client(): Promise<TokenRepairClient> {
	if (state.client) return state.client;
	// Resolve exactly the master backend; never open a fallback SQLite DB in PostgreSQL mode.
	const { activeDatabaseBackend, activePostgresReadUrl } = await import("@server/db");
	const { getDbPath } = await import("@server/db/connection");
	const { getNarraforkPath } = await import("@server/lib/narrafork-home");
	state.client ??= new TokenRepairClient(
		activeDatabaseBackend === "sqlite"
			? { backend: "sqlite", sqlitePath: getDbPath() }
			: { backend: "postgres", postgresUrl: activePostgresReadUrl ?? undefined },
		getNarraforkPath("request-dumps"),
	);
	return state.client;
}
export const usageTokenRepairService = {
	probe: async () => (await client()).probe(),
	latestJob: async () => (await client()).latestJob(),
	getJob: async (id: string) => (await client()).getJob(id),
	listCandidates: async (id: string, cursor?: string) =>
		(await client()).listCandidates(id, cursor),
	start: async (
		userId: string,
		reviewed: boolean,
		overrides?: TokenRepairProtocolOverride[],
		sourceJobId?: string,
	) => (await client()).start(userId, reviewed, overrides, sourceJobId),
	cancel: async (id: string) => (await client()).cancel(id),
	apply: async (id: string, ids: string[], userId: string) =>
		(await client()).apply(id, ids, userId),
};
