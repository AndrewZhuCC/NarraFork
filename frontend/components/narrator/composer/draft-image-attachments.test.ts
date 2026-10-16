import { afterEach, describe, expect, test } from "bun:test";
import {
	getComposerAssetId,
	getDraftImageAttachmentKey,
	loadDraftImageAttachments,
	MAX_DRAFT_ATTACHMENT_BYTES,
	saveDraftImageAttachments,
	setComposerAssetId,
} from "./draft-image-attachments";

const previousIndexedDb = Object.getOwnPropertyDescriptor(globalThis, "indexedDB");
afterEach(() => {
	if (previousIndexedDb) Object.defineProperty(globalThis, "indexedDB", previousIndexedDb);
	else Reflect.deleteProperty(globalThis, "indexedDB");
});

describe("composer resource identity", () => {
	test("same-name files remain distinct and explicit restored ids stay stable", () => {
		const first = new File(["first"], "same.png", { type: "image/png" });
		const second = new File(["second"], "same.png", { type: "image/png" });
		expect(getComposerAssetId(first)).toBe(getComposerAssetId(first));
		expect(getComposerAssetId(first)).not.toBe(getComposerAssetId(second));
		setComposerAssetId(second, "restored-resource");
		expect(getComposerAssetId(second)).toBe("restored-resource");
		expect(Object.keys(second)).not.toContain("assetId");
		expect(() => setComposerAssetId(second, "bad identity")).toThrow();
	});

	test("IDB serialization restores resource ids, migrates legacy entries and keeps blob budgets", async () => {
		// Exercise the real serialization functions with an async IDB transaction facade.
		const records = new Map<string, Record<string, unknown>>();
		const request = (result: unknown) => {
			const value = { result, onsuccess: null as (() => void) | null };
			queueMicrotask(() => value.onsuccess?.());
			return value;
		};
		const database = {
			transaction: () => {
				const transaction = {
					oncomplete: null as (() => void) | null,
					objectStore: () => ({
						get: (key: string) =>
							request(records.has(key) ? structuredClone(records.get(key)) : undefined),
						getAll: () => request([...records.values()].map((entry) => structuredClone(entry))),
						put: (record: Record<string, unknown>) =>
							records.set(String(record.draftKey), structuredClone(record)),
						delete: (key: string) => records.delete(key),
					}),
				};
				setTimeout(() => transaction.oncomplete?.(), 0);
				return transaction;
			},
		};
		Object.defineProperty(globalThis, "indexedDB", {
			configurable: true,
			value: { open: () => request(database) },
		});
		const first = new File(["first"], "same.png", { type: "image/png" });
		const second = new File(["other"], "same.png", { type: "image/png" });
		const text = new File(["log"], "a.log", { type: "text/plain" });
		const ids = [getComposerAssetId(first), getComposerAssetId(second), getComposerAssetId(text)];
		await saveDraftImageAttachments("u", "n", [first, second], [text]);
		const loaded = await loadDraftImageAttachments("u", "n");
		expect([...loaded.images, ...loaded.textFiles].map(getComposerAssetId)).toEqual(ids);
		expect(await loaded.images[0].text()).toBe("first");
		expect(await loaded.images[1].text()).toBe("other");
		expect(loaded.droppedCount).toBe(0);
		expect((await loadDraftImageAttachments("other-user", "n")).images).toEqual([]);

		const key = getDraftImageAttachmentKey("u", "legacy");
		const updatedAt = new Date().toISOString();
		records.set(key, {
			draftKey: key,
			userId: "u",
			narratorId: "legacy",
			updatedAt,
			images: [{ name: "old.png", type: "image/png", size: first.size, blob: first }],
		});
		const migrated = await loadDraftImageAttachments("u", "legacy");
		const migratedId = getComposerAssetId(migrated.images[0]);
		expect((await loadDraftImageAttachments("u", "legacy")).images.map(getComposerAssetId)).toEqual(
			[migratedId],
		);
		expect(records.get(key)?.updatedAt).toBe(updatedAt);

		const oversized = new File([new Uint8Array(MAX_DRAFT_ATTACHMENT_BYTES + 1)], "huge.log");
		await saveDraftImageAttachments("u", "limited", [], [text, oversized]);
		const limited = await loadDraftImageAttachments("u", "limited");
		expect(limited.textFiles.map((file) => file.name)).toEqual(["a.log"]);
		expect(limited.textFiles.map(getComposerAssetId)).toEqual([ids[2]]);
	});
});
