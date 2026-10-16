/**
 * Unit tests for the production PostgreSQL wiring (`postgres-runtime.ts`).
 *
 * Two families, both container-free:
 *
 *   1. CONFIGURATION — the fail-closed contract, resolved from explicit (env, settings)
 *      arguments. The defaults must stay SQLite; PostgreSQL requires BOTH an explicit
 *      backend and a URL; unknown/missing/mixed combinations are errors; and no error
 *      message may carry the URL or its credentials.
 *   2. STARTUP STAGES — every stage's IO is injected, so connection/migration/fts failure
 *      classification is asserted deterministically, plus close semantics (idempotent, and
 *      the executor refuses new operations the moment shutdown begins).
 *
 * A real connection-refused case runs here too: classifying it needs no PostgreSQL server,
 * only a closed loopback port.
 */
import { describe, expect, spyOn, test } from "bun:test";
import { logger } from "../lib/logger";
import type { PostgresClient } from "./postgres-client";
import {
	AMBIENT_DATABASE_URL_ENV,
	assertPostgresMigrationHistory,
	closePostgresRuntime,
	DATABASE_BACKEND_ENV,
	DATABASE_URL_ENV,
	DatabaseConfigError,
	describeDatabaseConfig,
	getOrStartPostgresRuntime,
	logPostgresStartup,
	PostgresRuntimeClosedError,
	PostgresStartupError,
	resolveDatabaseBackendConfig,
	startPostgresRuntime,
} from "./postgres-runtime";

const PG_URL = "postgres://nf:topsecret@127.0.0.1:5432/narrafork";

test("PostgreSQL refuses a divergent applied migration branch before running SQL", () => {
	const migrations = [{ hash: "published", folderMillis: 200 }];
	expect(() => assertPostgresMigrationHistory(undefined, migrations)).not.toThrow();
	expect(() =>
		assertPostgresMigrationHistory({ hash: "published", created_at: "200" }, migrations),
	).not.toThrow();
	expect(() =>
		assertPostgresMigrationHistory({ hash: "local-fork", created_at: 100 }, migrations),
	).toThrow("migration history diverges");
	expect(() =>
		assertPostgresMigrationHistory({ hash: "local-fork", created_at: 200 }, migrations),
	).toThrow("migration history diverges");
});

function postgresSection(section?: unknown): unknown {
	return section ?? { backend: "postgres" };
}

describe("resolveDatabaseBackendConfig", () => {
	test("no configuration at all resolves to SQLite", () => {
		expect(resolveDatabaseBackendConfig({}, undefined)).toEqual({
			backend: "sqlite",
			ignoredPostgresUrl: false,
		});
	});

	test("an ambient DATABASE_URL alone never selects PostgreSQL", () => {
		// The exact hazard the SQLite baseline pins one layer down: the variable is ambient in
		// developer shells for unrelated projects. Only an explicit backend selection makes it
		// meaningful.
		const resolved = resolveDatabaseBackendConfig(
			{ [AMBIENT_DATABASE_URL_ENV]: PG_URL },
			undefined,
		);
		expect(resolved).toEqual({ backend: "sqlite", ignoredPostgresUrl: true });
	});

	test("an explicit postgres backend without any URL is a configuration error", () => {
		expect(() =>
			resolveDatabaseBackendConfig({ [DATABASE_BACKEND_ENV]: "postgres" }, undefined),
		).toThrow(DatabaseConfigError);
		expect(() =>
			resolveDatabaseBackendConfig({ [DATABASE_BACKEND_ENV]: "postgres" }, undefined),
		).toThrow(/no connection URL/);
	});

	test("postgres + NF_DATABASE_URL resolves with the primary source", () => {
		const resolved = resolveDatabaseBackendConfig(
			{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
			undefined,
		);
		if (resolved.backend !== "postgres") throw new Error("expected postgres");
		expect(resolved.url).toBe(PG_URL);
		expect(resolved.urlSource).toBe(DATABASE_URL_ENV);
	});

	test("postgres + ambient DATABASE_URL resolves with the ambient source", () => {
		const resolved = resolveDatabaseBackendConfig(
			{ [DATABASE_BACKEND_ENV]: "postgres", [AMBIENT_DATABASE_URL_ENV]: PG_URL },
			undefined,
		);
		if (resolved.backend !== "postgres") throw new Error("expected postgres");
		expect(resolved.urlSource).toBe(AMBIENT_DATABASE_URL_ENV);
	});

	test("NF_DATABASE_URL wins over DATABASE_URL", () => {
		const resolved = resolveDatabaseBackendConfig(
			{
				[DATABASE_BACKEND_ENV]: "postgres",
				[DATABASE_URL_ENV]: "postgres://a:b@127.0.0.1:5432/primary",
				[AMBIENT_DATABASE_URL_ENV]: "postgres://a:b@127.0.0.1:5432/ambient",
			},
			undefined,
		);
		if (resolved.backend !== "postgres") throw new Error("expected postgres");
		expect(resolved.url).toContain("/primary");
	});

	test("the backend may come from settings while the URL still comes from the environment", () => {
		const resolved = resolveDatabaseBackendConfig(
			{ [DATABASE_URL_ENV]: PG_URL },
			{ backend: "postgres" },
		);
		expect(resolved.backend).toBe("postgres");
	});

	test("an explicit environment backend wins over the settings file", () => {
		const resolved = resolveDatabaseBackendConfig(
			{ [DATABASE_BACKEND_ENV]: "sqlite" },
			{ backend: "postgres" },
		);
		expect(resolved.backend).toBe("sqlite");
	});

	test("an unknown backend is an explicit failure, never a fallback", () => {
		expect(() =>
			resolveDatabaseBackendConfig({ [DATABASE_BACKEND_ENV]: "couchdb" }, undefined),
		).toThrow(/Unknown database backend/);
		expect(() => resolveDatabaseBackendConfig({}, { backend: "postgresql14" })).toThrow(
			DatabaseConfigError,
		);
	});

	test("the read/write selectors must not be combined with the postgres master switch", () => {
		for (const knob of ["NF_READ_BACKEND", "NF_WRITE_BACKEND"]) {
			expect(() =>
				resolveDatabaseBackendConfig(
					{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL, [knob]: "postgres" },
					undefined,
				),
			).toThrow(new RegExp(`${knob} must not be set`));
		}
	});

	test("a connection URL in settings.json is refused, not silently ignored", () => {
		// The settings object is served by GET /api/settings to any authenticated user; a
		// connection string carries credentials, so accepting it there would leak it.
		expect(() =>
			resolveDatabaseBackendConfig(
				{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
				postgresSection({ backend: "postgres", postgres: { url: PG_URL } }),
			),
		).toThrow(/database\.postgres\.url.*refused/);
	});

	test("pool tuning accepts positive numbers and rejects anything else", () => {
		const ok = resolveDatabaseBackendConfig(
			{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
			{ backend: "postgres", postgres: { max: 8, connectTimeout: 5 } },
		);
		if (ok.backend !== "postgres") throw new Error("expected postgres");
		expect(ok.pool).toEqual({ max: 8, connectTimeout: 5 });

		expect(() =>
			resolveDatabaseBackendConfig(
				{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
				{ backend: "postgres", postgres: { max: "lots" } },
			),
		).toThrow(DatabaseConfigError);
	});

	test("non-postgres URL schemes and unparsable URLs are configuration errors", () => {
		expect(() =>
			resolveDatabaseBackendConfig(
				{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: "mysql://u:p@h/db" },
				undefined,
			),
		).toThrow(/scheme/);
		expect(() =>
			resolveDatabaseBackendConfig(
				{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: "not a url" },
				undefined,
			),
		).toThrow(DatabaseConfigError);
	});

	test("configuration errors never echo the URL or its credentials", () => {
		let message = "";
		try {
			resolveDatabaseBackendConfig(
				{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
				postgresSection({ backend: "postgres", postgres: { url: PG_URL } }),
			);
		} catch (error) {
			message = error instanceof Error ? error.message : String(error);
		}
		expect(message).not.toContain(PG_URL);
		expect(message).not.toContain("topsecret");
	});

	test("the log-safe descriptor carries no URL", () => {
		const resolved = resolveDatabaseBackendConfig(
			{ [DATABASE_BACKEND_ENV]: "postgres", [DATABASE_URL_ENV]: PG_URL },
			undefined,
		);
		const descriptor = JSON.stringify(describeDatabaseConfig(resolved));
		expect(descriptor).not.toContain(PG_URL);
		expect(descriptor).not.toContain("topsecret");
		expect(descriptor).toContain(DATABASE_URL_ENV);
	});
});

// ─────────────────────────────────────────────────────────────────────────────
// Startup stages (injected IO)
// ─────────────────────────────────────────────────────────────────────────────

function fakeClient(): { client: PostgresClient; closedCount: () => number } {
	let closed = 0;
	const client = {
		sql: {
			unsafe: () => Promise.resolve([{ "?column?": 1 }]),
		} as unknown as PostgresClient["sql"],
		db: {} as PostgresClient["db"],
		close: () => {
			closed++;
			return Promise.resolve();
		},
	};
	return { client, closedCount: () => closed };
}

async function stageOf(promise: Promise<unknown>): Promise<PostgresStartupError> {
	try {
		await promise;
	} catch (error) {
		expect(error).toBeInstanceOf(PostgresStartupError);
		return error as PostgresStartupError;
	}
	throw new Error("expected startup to fail");
}

const CONFIG = {
	backend: "postgres",
	url: PG_URL,
	urlSource: DATABASE_URL_ENV,
	pool: {},
} as const;

describe("startPostgresRuntime stage classification", () => {
	test("client creation failure is a connection failure", async () => {
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => {
					throw new Error("bad driver");
				},
			}),
		);
		expect(failure.stage).toBe("connection");
	});

	test("connect probe failure is a connection failure and closes the client", async () => {
		const fake = fakeClient();
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => fake.client,
				connectProbe: () => Promise.reject(new Error("connection refused")),
			}),
		);
		expect(failure.stage).toBe("connection");
		expect(fake.closedCount()).toBe(1);
	});

	test("migration dispatch failure is a migration failure", async () => {
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => fakeClient().client,
				runMigrations: () => Promise.reject(new Error("no valid PostgreSQL migration data")),
			}),
		);
		expect(failure.stage).toBe("migration");
	});

	test("a failing migration statement is a migration failure, not a connection one", async () => {
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => fakeClient().client,
				runMigrations: async (run) => {
					await run("/resolved/drizzle-postgres");
				},
				migrateFolder: () => Promise.reject(new Error('relation "chapters" already exists')),
			}),
		);
		expect(failure.stage).toBe("migration");
	});

	test("FTS installation failure is an fts failure", async () => {
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => fakeClient().client,
				// Isolate the stage: migrations succeed, so a failure can only come from FTS.
				runMigrations: async (run) => {
					await run("/resolved/drizzle-postgres");
				},
				migrateFolder: () => Promise.resolve(),
				ensureFts: () => Promise.reject(new Error("permission denied: CREATE EXTENSION")),
			}),
		);
		expect(failure.stage).toBe("fts");
	});

	test("driver messages are scrubbed of the URL and credentials", async () => {
		const failure = await stageOf(
			startPostgresRuntime(CONFIG, {
				createClient: () => fakeClient().client,
				connectProbe: () =>
					Promise.reject(new Error(`password authentication failed for ${PG_URL} (topsecret)`)),
			}),
		);
		expect(failure.message).not.toContain(PG_URL);
		expect(failure.message).not.toContain("topsecret");
		expect(failure.message).toContain("[redacted]");
	});

	test("a successful startup returns a working runtime; close is idempotent and gates the executor", async () => {
		const fake = fakeClient();
		let migratedFolder = "";
		let ftsEnsured = 0;
		const runtime = await startPostgresRuntime(CONFIG, {
			createClient: () => fake.client,
			runMigrations: async (run) => {
				await run("/resolved/drizzle-postgres");
			},
			migrateFolder: (_client, folder) => {
				migratedFolder = folder;
				return Promise.resolve();
			},
			ensureFts: () => {
				ftsEnsured++;
				return Promise.resolve();
			},
		});
		expect(runtime.backend).toBe("postgres");
		expect(runtime.migration.folder).toBe("/resolved/drizzle-postgres");
		expect(migratedFolder).toBe("/resolved/drizzle-postgres");
		expect(ftsEnsured).toBe(1);
		expect(runtime.closed).toBe(false);

		const rows = await runtime.executor.unsafe("SELECT 1");
		expect(rows).toHaveLength(1);

		await runtime.close();
		expect(runtime.closed).toBe(true);
		await runtime.close();
		expect(fake.closedCount()).toBe(1);

		// No new operations once shutdown has begun.
		await expect(runtime.executor.unsafe("SELECT 1")).rejects.toBeInstanceOf(
			PostgresRuntimeClosedError,
		);
	});
});

describe("hot runtime connection identity", () => {
	const pinKey = Symbol.for("narrafork.postgresRuntime");
	const globals = globalThis as unknown as Record<symbol, unknown>;
	function injected() {
		const fake = fakeClient();
		let clients = 0;
		let migrations = 0;
		return {
			fake,
			counts: () => ({ clients, migrations }),
			deps: {
				createClient: () => {
					clients++;
					return fake.client;
				},
				runMigrations: async () => {
					migrations++;
				},
				ensureFts: async () => {},
			},
		};
	}
	test("module reevaluation reuses one client and exports its confirmed read URL", async () => {
		const io = injected();
		try {
			const first = await getOrStartPostgresRuntime(CONFIG, io.deps);
			const reloaded = await import(`./postgres-runtime.ts?hot=same-${Date.now()}`);
			const second = await reloaded.getOrStartPostgresRuntime({ ...CONFIG, pool: {} }, io.deps);
			expect(second.runtime).toBe(first.runtime);
			expect(second.isHotReload).toBe(true);
			expect(second.readUrl).toBe(CONFIG.url);
			expect(io.counts()).toEqual({ clients: 1, migrations: 1 });
		} finally {
			await closePostgresRuntime();
		}
	});
	test("startup snapshots configuration before asynchronous work and ignores later caller mutations", async () => {
		const io = injected();
		const config = { ...CONFIG, url: PG_URL, pool: { max: 2 } };
		try {
			const pending = getOrStartPostgresRuntime(config, io.deps);
			config.url = "postgres://other:MUTATED_PASSWORD@127.0.0.1:5432/other";
			config.pool.max = 3;
			const first = await pending;
			expect(first.readUrl).toBe(PG_URL);
			const same = await getOrStartPostgresRuntime({ ...CONFIG, pool: { max: 2 } }, io.deps);
			expect(same.runtime).toBe(first.runtime);
			await expect(getOrStartPostgresRuntime(config, io.deps)).rejects.toThrow(/restart/i);
			expect(io.counts()).toEqual({ clients: 1, migrations: 1 });
		} finally {
			await closePostgresRuntime();
		}
	});
	test("URL A to B and pool changes fail closed without leaking secrets or closing A", async () => {
		const io = injected();
		const info = spyOn(logger, "info").mockImplementation(() => {});
		const warnings = spyOn(logger, "warn").mockImplementation(() => {});
		const errors = spyOn(logger, "error").mockImplementation(() => {});
		try {
			const first = await getOrStartPostgresRuntime(CONFIG, io.deps);
			logPostgresStartup(first.runtime, CONFIG);
			const urlB = "postgres://other:URL_B_PASSWORD_SENTINEL@127.0.0.1:5432/other";
			for (const changed of [
				{ ...CONFIG, url: urlB },
				{ ...CONFIG, pool: { max: 7 } },
			]) {
				await expect(getOrStartPostgresRuntime(changed, io.deps)).rejects.toThrow(/restart/i);
				try {
					await getOrStartPostgresRuntime(changed, io.deps);
				} catch (error) {
					expect(String(error)).not.toContain(PG_URL);
					expect(String(error)).not.toContain("topsecret");
					expect(String(error)).not.toContain(urlB);
					expect(String(error)).not.toContain("URL_B_PASSWORD_SENTINEL");
				}
			}
			const logs = JSON.stringify([info.mock.calls, warnings.mock.calls, errors.mock.calls]);
			expect(logs).not.toContain("topsecret");
			expect(logs).not.toContain("URL_B_PASSWORD_SENTINEL");
			expect(logs).not.toContain(PG_URL);
			expect(io.counts()).toEqual({ clients: 1, migrations: 1 });
			expect(io.fake.closedCount()).toBe(0);
		} finally {
			info.mockRestore();
			warnings.mockRestore();
			errors.mockRestore();
			await closePostgresRuntime();
		}
	});
	test("legacy pin without private identity is rejected and never automatically closed", async () => {
		const io = injected();
		const runtime = await startPostgresRuntime(CONFIG, io.deps);
		const legacy = { runtime };
		globals[pinKey] = legacy;
		try {
			await expect(getOrStartPostgresRuntime(CONFIG, io.deps)).rejects.toThrow(/restart/i);
			expect(globals[pinKey]).toBe(legacy);
			expect(io.fake.closedCount()).toBe(0);
			expect(io.counts()).toEqual({ clients: 1, migrations: 1 });
		} finally {
			await closePostgresRuntime();
		}
	});
});

describe("real connection classification (no server needed)", () => {
	test("connecting to a closed loopback port fails as a connection-stage error", async () => {
		// Port 1/tcp is never a PostgreSQL listener. This proves the real bun-sql error crosses
		// the stage boundary classified — not just the injected stub above.
		const failure = await stageOf(
			startPostgresRuntime({
				backend: "postgres",
				url: "postgres://nf:topsecret@127.0.0.1:1/narrafork",
				urlSource: DATABASE_URL_ENV,
				pool: { connectTimeout: 5 },
			}),
		);
		expect(failure.stage).toBe("connection");
		expect(failure.message).not.toContain("topsecret");
	}, 30_000);
});
