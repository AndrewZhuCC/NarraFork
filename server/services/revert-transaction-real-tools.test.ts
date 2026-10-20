import { describe, expect, spyOn } from "bun:test";
import * as fs from "node:fs/promises";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import * as schema from "../db/schema";
import { generateId } from "../lib/id";
import {
	access,
	db,
	edit,
	execution,
	history,
	journalFiles,
	namespace,
	prepare,
	principal,
	restores,
	service,
	setFileHook,
	test,
	twoFiles,
	workspace,
	write,
} from "./__tests__/revert-transaction.fixture";
import { createFileChangeIdentity, fileChangeIdentityKey } from "./file-change-identity";
import { withFileWrite } from "./file-write-queue";

describe("real tools to independent restoration", () => {
	test("Write and Edit history preserve unrelated external edits", async () => {
		const path = join(workspace, "source.txt");
		await fs.writeFile(path, "old\nline\nend\n");
		const call = await edit(path, "old", "new");
		await fs.writeFile(path, "new\nline\nexternal\n");
		const plan = await prepare({
			selector: { kind: "tool_calls", toolCallIds: [call.toolCallId] },
		});
		expect((await execution(plan).whenSettled).status).toBe("committed");
		expect(await fs.readFile(path, "utf8")).toBe("old\nline\nexternal\n");
	});
	test("historical workspace identities sharing one physical file form one restore", async () => {
		const path = join(workspace, "epochs.txt");
		await fs.writeFile(path, "original");
		await write(path, "first");
		const first = db.select().from(schema.fileChangeEffects).limit(1).get();
		const scope = db.select().from(schema.fileChangeScopes).limit(1).get();
		if (!first || !scope) throw new Error("missing fixture effect");
		const legacyScope = {
			...scope,
			id: generateId(),
			workspaceInstanceId: "legacy-incarnation",
			status: "retired" as const,
		};
		db.insert(schema.fileChangeScopes).values(legacyScope).run();
		const legacyIdentity = createFileChangeIdentity(legacyScope, first.identityJson);
		db.update(schema.fileChangeEffects)
			.set({
				scopeId: legacyScope.id,
				identityJson: legacyIdentity,
				fileKey: fileChangeIdentityKey(legacyIdentity),
			})
			.where(eq(schema.fileChangeEffects.id, first.id))
			.run();
		await write(path, "second");
		const plan = await prepare();
		expect(plan.expectedFileCount).toBe(1);
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("committed");
		expect(result.files).toHaveLength(1);
		expect(await fs.readFile(path, "utf8")).toBe("original");
		expect(
			db
				.select()
				.from(schema.fileChangeEffects)
				.where(eq(schema.fileChangeEffects.id, first.id))
				.get()?.fileKey,
		).toBe(fileChangeIdentityKey(legacyIdentity));
	});

	test("new Write file is removed", async () => {
		const path = join(workspace, "new.txt");
		await write(path, "new");
		const plan = await prepare();
		expect((await execution(plan).whenSettled).files[0].status).toBe("applied");
		await expect(fs.lstat(path)).rejects.toMatchObject({ code: "ENOENT" });
	});
	test("file-only operation preserves messages", async () => {
		const path = join(workspace, "only.txt");
		await write(path, "new");
		const plan = await prepare({ kind: "revert" });
		const before = history();
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("committed");
		expect(result.historyCommitted).toBe(false);
		expect(history()).toBe(before);
	});
	test("COW preserves a sibling's shared message and tool", async () => {
		const path = join(workspace, "shared.txt");
		const call = await write(path, "new");
		db.insert(schema.narratorMessageRefs)
			.values({ id: generateId(), narratorId: "fork", messageId: call.messageId, seq: 1 })
			.run();
		const plan = await prepare();
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("committed");
		expect(
			db
				.select()
				.from(schema.narratorMessageRefs)
				.where(eq(schema.narratorMessageRefs.narratorId, "fork"))
				.all(),
		).toHaveLength(1);
		expect(
			db
				.select()
				.from(schema.narratorMessages)
				.where(eq(schema.narratorMessages.id, call.messageId))
				.get(),
		).toBeDefined();
		expect(
			db
				.select()
				.from(schema.narratorToolCalls)
				.where(eq(schema.narratorToolCalls.id, call.toolCallId))
				.get(),
		).toBeDefined();
	});
	test("per-file authorization failure retains other successful restores", async () => {
		const { a, b, plan } = await twoFiles();
		const before = history();
		setFileHook((path) => {
			if (path === b) throw new Error("permission revoked");
		});
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("partial");
		expect(result.files.map((file) => file.status)).toEqual(["applied", "failed"]);
		expect(history()).toBe(before);
		expect(await fs.readFile(a, "utf8")).toBe("original");
	});
	test("a missing historical blob creates a failed item while another file restores", async () => {
		const a = join(workspace, "good.txt");
		const b = join(workspace, "bad.txt");
		await fs.writeFile(a, "good-before");
		await fs.writeFile(b, "bad-before");
		await write(a, "good-after");
		await write(b, "bad-after");
		const bad = db
			.select()
			.from(schema.fileChangeEffects)
			.all()
			.find((effect) => effect.identityJson.canonicalPath === b);
		if (!bad?.beforeBlobDigest) throw new Error("missing test history");
		const read = namespace.store.readBytes.bind(namespace.store);
		const spy = spyOn(namespace.store, "readBytes").mockImplementation((ref, options) => {
			if (ref.digest === bad.beforeBlobDigest) return Promise.reject(new Error("blob unavailable"));
			return read(ref, options);
		});
		restores.push(() => spy.mockRestore());
		const plan = await prepare();
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("partial");
		expect(result.historyCommitted).toBe(false);
		expect(await fs.readFile(a, "utf8")).toBe("good-before");
		expect(await fs.readFile(b, "utf8")).toBe("bad-after");
	});
	test("a tool without a history journal leaves only that operation unavailable", async () => {
		const { a, b } = await twoFiles();
		const effect = db
			.select()
			.from(schema.fileChangeEffects)
			.all()
			.find((item) => item.identityJson.canonicalPath === b);
		if (!effect) throw new Error("fixture effect missing");
		db.update(schema.narratorToolCalls)
			.set({ fileChangeOperationId: null })
			.where(eq(schema.narratorToolCalls.fileChangeOperationId, effect.operationId))
			.run();
		const plan = await prepare();
		expect(plan.coverageComplete).toBe(false);
		expect(plan.expectedFileCount).toBe(1);
		const before = history();
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("partial");
		expect(result.historyCommitted).toBe(false);
		expect(result.reason).toBe("SELECTED_HISTORY_UNAVAILABLE");
		expect(await fs.readFile(a, "utf8")).toBe("original");
		expect(await fs.readFile(b, "utf8")).toBe("changed");
		expect(history()).toBe(before);
	});

	test("applied effects with all history states unknown stay local to their file", async () => {
		const { a, b } = await twoFiles();
		const effect = db
			.select()
			.from(schema.fileChangeEffects)
			.all()
			.find((item) => item.identityJson.canonicalPath === b);
		if (!effect) throw new Error("fixture effect missing");
		const unknown = { kind: "unknown" as const, reason: "history_unavailable" as const };
		db.update(schema.fileChangeEffects)
			.set({
				beforeStateJson: unknown,
				intendedAfterStateJson: unknown,
				observedAfterStateJson: unknown,
				beforeBlobDigest: null,
				intendedAfterBlobDigest: null,
				observedAfterBlobDigest: null,
				diskOutcome: "applied",
			})
			.where(eq(schema.fileChangeEffects.id, effect.id))
			.run();
		const plan = await prepare();
		expect(plan.coverageComplete).toBe(false);
		expect(plan.expectedFileCount).toBe(2);
		const before = history();
		const result = await execution(plan).whenSettled;
		expect(result.status).toBe("partial");
		expect(
			result.files.find((file) => file.filePath === effect.identityJson.displayPath)?.status,
		).toBe("failed");
		expect(await fs.readFile(a, "utf8")).toBe("original");
		expect(await fs.readFile(b, "utf8")).toBe("changed");
		expect(history()).toBe(before);
	});

	test("explicit snapshot restores a known before despite an unknown IO result", async () => {
		const { b } = await twoFiles();
		const effect = db
			.select()
			.from(schema.fileChangeEffects)
			.all()
			.find((item) => item.identityJson.canonicalPath === b);
		if (!effect) throw new Error("fixture effect missing");
		const call = db
			.select()
			.from(schema.narratorToolCalls)
			.where(eq(schema.narratorToolCalls.fileChangeOperationId, effect.operationId))
			.get();
		if (!call) throw new Error("fixture tool missing");
		const unknown = { kind: "unknown" as const, reason: "result_unknown" as const };
		db.update(schema.fileChangeEffects)
			.set({
				intendedAfterStateJson: unknown,
				observedAfterStateJson: unknown,
				intendedAfterBlobDigest: null,
				observedAfterBlobDigest: null,
				diskOutcome: "unknown",
				outcome: "unknown",
				attributionGrade: "unknown",
				executionConfirmed: false,
			})
			.where(eq(schema.fileChangeEffects.id, effect.id))
			.run();
		db.update(schema.fileChangeOperations)
			.set({ unresolvedEffectCount: 1, effectOutcome: "unknown" })
			.where(eq(schema.fileChangeOperations.id, effect.operationId))
			.run();
		const plan = await prepare({
			selector: { kind: "tool_calls", toolCallIds: [call.id] },
			uiAction: "delete_tool_block",
			recoveryMode: "snapshot",
		});
		expect(plan.coverageComplete).toBe(true);
		const result = await service.execute({
			principal,
			narratorId: "narrator",
			planId: plan.id,
			planHash: plan.planHash as string,
			action: "delete_tool_block",
			acceptSnapshotRestore: true,
		}).whenSettled;
		expect(result.status).toBe("committed");
		expect(result.historyCommitted).toBe(true);
		expect(await fs.readFile(b, "utf8")).toBe("original");
	});

	test("cancellation stops unstarted files but keeps current IO success", async () => {
		const { a, b, plan } = await twoFiles();
		const controller = new AbortController();
		let calls = 0;
		setFileHook((path) => {
			if (path === a && ++calls === 3) controller.abort();
		});
		const result = await execution(plan, controller.signal).whenSettled;
		expect(result.files.map((file) => file.status)).toEqual(["applied", "not_attempted"]);
		expect(result.historyCommitted).toBe(false);
		expect(await fs.readFile(b, "utf8")).toBe("changed");
	});
	test("queued cancellation has no target effect", async () => {
		const { a, plan } = await twoFiles();
		const row = journalFiles(plan)[0];
		const gate = Promise.withResolvers<void>();
		const entered = Promise.withResolvers<void>();
		const held = withFileWrite(row.identityJson, async () => {
			entered.resolve();
			await gate.promise;
		});
		await entered.promise;
		const controller = new AbortController();
		const attempt = execution(plan, controller.signal);
		controller.abort();
		gate.resolve();
		await held;
		await attempt.whenSettled.catch(() => {});
		expect(await fs.readFile(a, "utf8")).toBe("changed");
	});
	test("a shared path queue serializes ordinary writers against restore", async () => {
		const { a, plan } = await twoFiles();
		const identity = journalFiles(plan)[0].identityJson;
		const entered = Promise.withResolvers<void>();
		const release = Promise.withResolvers<void>();
		const writer = withFileWrite(identity, async () => {
			entered.resolve();
			await release.promise;
			await fs.writeFile(a, "writer-before-restore");
		});
		await entered.promise;
		const attempt = execution(plan);
		release.resolve();
		await writer;
		const result = await attempt.whenSettled;
		expect(result.files[0].status).toBe("conflict");
		expect(await fs.readFile(a, "utf8")).toBe("writer-before-restore");
	});
	test("unrelated database write during history preparation is allowed", async () => {
		const { plan } = await twoFiles();
		let updated = false;
		const authorize = access.authorizeNarrator;
		const spy = spyOn(access, "authorizeNarrator").mockImplementation(async (...args) => {
			await authorize(...args);
			if (!updated) {
				updated = true;
				db.update(schema.users)
					.set({ username: "unrelated" })
					.where(eq(schema.users.id, "bob"))
					.run();
			}
		});
		restores.push(() => spy.mockRestore());
		expect((await execution(plan).whenSettled).status).toBe("committed");
	});
});
