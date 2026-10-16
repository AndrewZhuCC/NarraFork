/**
 * Deterministic actor interleaving model for the local revert transaction.
 * Shared fixture: ./__tests__/revert-transaction.fixture.ts (importing it registers the hooks).
 */
import { describe, expect } from "bun:test";
import * as fs from "node:fs/promises";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import * as schema from "../db/schema";
import { generateId } from "../lib/id";
import {
	access,
	allowedFiles,
	coordinatorState,
	db,
	execution,
	history,
	prepare,
	principal,
	runtime,
	test,
	tool,
	workspace,
} from "./__tests__/revert-transaction.fixture";

describe("deterministic actor interleaving model", () => {
	for (const selectedMask of [1, 3, 6, 9, 12, 15]) {
		test(`seed ${selectedMask}: reverses only the selected real actor effects across two files`, async () => {
			const ending = selectedMask % 2 ? "\r\n" : "\n";
			const paths = [
				join(workspace, `seed-${selectedMask}-a.txt`),
				join(workspace, `seed-${selectedMask}-b.txt`),
			];
			const initial = paths.map((_, file) =>
				Array.from({ length: 42 }, (_, line) => `file-${file}-line-${line}`),
			);
			const model = initial.map((lines) => [...lines]);
			const bytes = (lines: string[]) => Buffer.from(`${lines.join(ending)}${ending}`);
			const originalModes: number[] = [];
			for (const [file, path] of paths.entries()) {
				allowedFiles.add(path);
				await fs.writeFile(path, bytes(model[file]));
				await fs.chmod(path, file ? 0o600 : 0o644);
				// Windows exposes DOS writable/read-only mode, not POSIX group/execute bits.
				originalModes.push((await fs.lstat(path)).mode & 0o7777);
			}
			const actions: {
				actor: "selected" | "other" | "human";
				file: number;
				line: number;
				ordinal?: number;
			}[] = [
				{ actor: "selected", file: 0, line: 2, ordinal: 0 },
				{ actor: "selected", file: 1, line: 2, ordinal: 1 },
				{ actor: "selected", file: 0, line: 22, ordinal: 2 },
				{ actor: "selected", file: 1, line: 22, ordinal: 3 },
				{ actor: "other", file: 0, line: 32 },
				{ actor: "other", file: 1, line: 32 },
				{ actor: "human", file: 0, line: 12 },
				{ actor: "human", file: 1, line: 12 },
			];
			let random = Math.imul(selectedMask, 0x9e3779b1) >>> 0;
			for (let index = actions.length - 1; index > 0; index--) {
				random ^= random << 13;
				random ^= random >>> 17;
				random ^= random << 5;
				const other = (random >>> 0) % (index + 1);
				[actions[index], actions[other]] = [actions[other], actions[index]];
			}
			const selected = new Map<number, Awaited<ReturnType<typeof tool>>>();
			for (const action of actions) {
				const path = paths[action.file];
				const old = model[action.file][action.line];
				const next = `${action.actor}-${selectedMask}-${action.file}-${action.line}`;
				const previousBytes = bytes(model[action.file]);
				model[action.file][action.line] = next;
				if (action.actor === "human") {
					const desiredBytes = bytes(model[action.file]);
					await runtime.executeEditor({
						requestId: generateId(),
						userId: "alice",
						narratorId: "narrator",
						projectId: "project",
						cwd: workspace,
						lexicalPath: path,
						canonicalPath: path,
						signal: new AbortController().signal,
						input: { path, content: desiredBytes.toString() },
						authorize: () => access.authenticate(principal, new AbortController().signal),
						construct(before) {
							expect(before.bytes && Buffer.from(before.bytes).equals(previousBytes)).toBe(true);
							return { nextBytes: desiredBytes, result: null, lineStats: { added: 1, removed: 1 } };
						},
					});
				} else {
					const result = await tool(
						"Edit",
						path,
						{ file_path: path, old_string: `${old}${ending}`, new_string: `${next}${ending}` },
						workspace,
						false,
						action.actor === "selected" ? "narrator" : "fork",
					);
					if (action.ordinal !== undefined) selected.set(action.ordinal, result);
				}
				expect((await fs.readFile(path)).equals(bytes(model[action.file]))).toBe(true);
			}
			// A late external edit is not in any tool receipt and must also survive.
			for (const [file, path] of paths.entries()) {
				model[file][12] = `late-human-${selectedMask}-${file}`;
				await fs.writeFile(path, bytes(model[file]));
			}
			const expected = model.map((lines) => [...lines]);
			const selectedMessages: string[] = [];
			for (const action of actions) {
				if (action.ordinal === undefined || !(selectedMask & (1 << action.ordinal))) continue;
				expected[action.file][action.line] = initial[action.file][action.line];
				const target = selected.get(action.ordinal);
				if (!target) throw new Error("Real selected effect missing from model fixture");
				selectedMessages.push(target.messageId);
			}
			const originalHistory = history();
			const foreignRefs = db
				.select()
				.from(schema.narratorMessageRefs)
				.where(eq(schema.narratorMessageRefs.narratorId, "fork"))
				.limit(10)
				.all();
			const kind = selectedMask % 2 ? "revert" : "history_delete";
			const plan = await prepare({
				kind,
				selector: { kind: "messages", messageIds: selectedMessages.reverse() },
			});
			const result = await execution(plan).whenSettled;
			expect(result).toMatchObject({
				status: "committed",
				journalStatus: "committed",
				settling: false,
				reason: null,
			});
			for (const [file, path] of paths.entries()) {
				expect((await fs.readFile(path)).equals(bytes(expected[file]))).toBe(true);
				expect((await fs.lstat(path)).mode & 0o7777).toBe(originalModes[file]);
			}
			expect(
				db
					.select()
					.from(schema.narratorMessageRefs)
					.where(eq(schema.narratorMessageRefs.narratorId, "fork"))
					.limit(10)
					.all(),
			).toEqual(foreignRefs);
			if (kind === "revert") expect(history()).toBe(originalHistory);
			else
				for (const target of selected.values()) {
					const row = db
						.select({ id: schema.narratorMessageRefs.id })
						.from(schema.narratorMessageRefs)
						.where(eq(schema.narratorMessageRefs.messageId, target.messageId))
						.get();
					expect(!!row).toBe(!selectedMessages.includes(target.messageId));
				}
			const finalHistory = history();
			await fs.writeFile(paths[0], "external-after-commit");
			expect((await execution(plan).whenSettled).status).toBe("committed");
			expect(await fs.readFile(paths[0], "utf8")).toBe("external-after-commit");
			expect(history()).toBe(finalHistory);
			expect(coordinatorState.leases.size).toBe(0);
			expect(coordinatorState.activities.size).toBe(0);
		}, 20_000);
	}
});
