import { expect, test } from "bun:test";
import { mkdtemp, realpath, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createPostgresClient } from "@server/db/postgres-client";
import { fileChangeStorageBudgets } from "@server/db/postgres-schema";
import { FILE_CHANGE_LIMITS } from "@shared/file-change-protocol";
import { eq } from "drizzle-orm";
import { withPostgres } from "../../tests/db/pg-test-harness";
import { migrationSql } from "../../tests/server/services/read/pg-parity-matrix";
import { FILE_CHANGE_BLOB_BUDGET_ID } from "./file-change-blob-catalog";
import { FileChangeBlobStore } from "./file-change-blob-store";
import { createPostgresFileChangeBlobCatalog } from "./postgres-file-change-blob-catalog";

/** Real PostgreSQL 17, generated migrations and physical blob publication.
 * Explicit opt-in uses the repository's isolated container harness, never DATABASE_URL. */
test.skipIf(process.env.PG_INTEGRATION !== "1")(
	"PostgreSQL blob budgets retain 5 GiB quota and cross the int4 byte-count boundary",
	async () => {
		const root = await mkdtemp(join(await realpath(tmpdir()), "pg-blob-budget-"));
		try {
			const statements = await migrationSql();
			const outcome = await withPostgres(async ({ exec, port, credentials }) => {
				for (const statement of statements) {
					const result = await exec(statement);
					if (result.code !== 0)
						throw new Error(`PG migration failed: ${result.stderr.slice(-2000)}`);
				}
				const client = createPostgresClient({
					driver: "bun-sql",
					url: `postgres://${encodeURIComponent(credentials.user)}:${encodeURIComponent(credentials.password)}@127.0.0.1:${port}/nf_harness`,
					max: 2,
					connectTimeout: 10,
				});
				try {
					const catalog = createPostgresFileChangeBlobCatalog({
						db: client.db,
						namespaceKey: root,
					});
					const initial = await catalog.initializeNamespace();
					expect(initial.quotaBytes).toBe(FILE_CHANGE_LIMITS.blobSoftQuotaBytes);
					expect(initial.quotaBytes).toBe(5 * 1024 * 1024 * 1024);
					expect(typeof initial.quotaBytes).toBe("number");
					// PostgreSQL checks the proposed INSERT values before ON CONFLICT;
					// idempotent initialization must also accept the full default quota.
					expect(await catalog.initializeNamespace()).toEqual(initial);

					const oldUsedBytes = 2 ** 31 - 2;
					const oldReservedBytes = 2 ** 31 + 5;
					// Scalar legacy counters are seeded, rather than allocating gigabytes
					// of fixture bodies. The next publication itself performs real disk IO.
					await client.db
						.update(fileChangeStorageBudgets)
						.set({
							usedBytes: oldUsedBytes,
							reservedBytes: oldReservedBytes,
							status: "reconciling",
						})
						.where(eq(fileChangeStorageBudgets.id, FILE_CHANGE_BLOB_BUDGET_ID));
					const store = new FileChangeBlobStore({
						root: join(root, "objects"),
						minimumFreeBytes: 0,
						admission: catalog.admission({ ownerEpoch: "pg-budget-regression" }),
					});
					const ref = await store.putBytes(Buffer.from("abc"), { expectedSize: 3 });
					expect(await store.readBytes(ref)).toEqual(Buffer.from("abc"));
					expect(await catalog.getMetadata({ ref })).toMatchObject({
						digest: ref.digest,
						status: "ready",
						sizeBytes: 3,
					});
					const current = await catalog.getBudget();
					expect(current).toMatchObject({
						usedBytes: oldUsedBytes + 3,
						reservedBytes: oldReservedBytes,
						quotaBytes: FILE_CHANGE_LIMITS.blobSoftQuotaBytes,
						status: "reconciling",
					});
					expect(typeof current?.usedBytes).toBe("number");
					expect(typeof current?.reservedBytes).toBe("number");
					const duplicate = await store.putBytes(Buffer.from("abc"), { expectedSize: 3 });
					expect(duplicate).toEqual(ref);
					expect((await catalog.getBudget())?.usedBytes).toBe(oldUsedBytes + 3);
					return { verified: true };
				} catch (error) {
					throw new Error(
						`${String(error)}; cause: ${String(error instanceof Error ? error.cause : undefined)}`,
					);
				} finally {
					await client.close();
				}
			});
			if ("status" in outcome)
				throw new Error(
					`PostgreSQL harness ${outcome.status}: ${"reason" in outcome ? outcome.reason : "missing result"}`,
				);
			expect(outcome.verified).toBe(true);
		} finally {
			await rm(root, { recursive: true, force: true });
		}
	},
	300_000,
);
