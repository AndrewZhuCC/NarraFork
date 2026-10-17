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
const closeCachedConnections: (() => void)[] = [];
afterEach(() => {
	for (const close of closeCachedConnections.splice(0)) close();
	if (previousIndexedDb) Object.defineProperty(globalThis, "indexedDB", previousIndexedDb);
	else Reflect.deleteProperty(globalThis, "indexedDB");
});

function installIndexedDbFacade(firstOpen?: "error" | "blocked" | "throw") {
	const records = new Map<string, Record<string, unknown>>();
	const request = (result: unknown) => {
		const value = { result, onsuccess: null as (() => void) | null };
		queueMicrotask(() => value.onsuccess?.());
		return value;
	};
	const createDatabase = () => {
		const database = {
			closed: false,
			close: () => {
				database.closed = true;
			},
			onversionchange: null as (() => void) | null,
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
		closeCachedConnections.push(() => database.onversionchange?.());
		return database;
	};
	const opens: {
		result: ReturnType<typeof createDatabase>;
		error: Error;
		onsuccess: (() => void) | null;
		onerror: (() => void) | null;
		onblocked: (() => void) | null;
	}[] = [];
	let attempts = 0;
	Object.defineProperty(globalThis, "indexedDB", {
		configurable: true,
		value: {
			open: () => {
				const outcome = attempts++ === 0 ? firstOpen : undefined;
				if (outcome === "throw") throw new Error("IndexedDB open threw");
				const openRequest = {
					result: createDatabase(),
					error: new Error("IndexedDB open failed"),
					onsuccess: null as (() => void) | null,
					onerror: null as (() => void) | null,
					onblocked: null as (() => void) | null,
				};
				opens.push(openRequest);
				queueMicrotask(() => {
					if (outcome === "error") openRequest.onerror?.();
					else if (outcome === "blocked") openRequest.onblocked?.();
					else openRequest.onsuccess?.();
				});
				return openRequest;
			},
		},
	});
	return { records, opens };
}

describe("draft attachment storage failures", () => {
	for (const failure of ["error", "blocked", "throw"] as const) {
		test(`first open ${failure} rejects instead of returning absent; a retry preserves the record`, async () => {
			const { records, opens } = installIndexedDbFacade(failure);
			const key = getDraftImageAttachmentKey("u", "n");
			const blob = new Blob(["original"], { type: "image/png" });
			const record = {
				draftKey: key,
				userId: "u",
				narratorId: "n",
				updatedAtMs: Date.now(),
				images: [{ id: "f", name: "f.png", type: blob.type, size: blob.size, blob }],
				textFiles: [],
			};
			records.set(key, record);
			await expect(loadDraftImageAttachments("u", "n")).rejects.toThrow();
			expect(records.get(key)).toBe(record);
			const restored = await loadDraftImageAttachments("u", "n");
			expect(restored.images.map(getComposerAssetId)).toEqual(["f"]);
			expect(await restored.images[0].text()).toBe("original");
			expect(records.get(key)).toBe(record);
			if (failure === "blocked") {
				// The abandoned blocked open can succeed late. Close only that handle,
				// retaining the successful retry's cache and untouched stored blobs.
				opens[0].onsuccess?.();
				expect(opens[0].result.closed).toBe(true);
				expect(opens[1].result.closed).toBe(false);
				await loadDraftImageAttachments("u", "n");
				expect(opens).toHaveLength(2);
			}
		});
	}

	test("an absent record remains a successful empty restore", async () => {
		installIndexedDbFacade();
		expect(await loadDraftImageAttachments("u", "absent")).toEqual({
			images: [],
			textFiles: [],
			droppedCount: 0,
		});
	});

	test("missing IndexedDB rejects for a valid owner, but an unknown owner remains empty", async () => {
		Object.defineProperty(globalThis, "indexedDB", { configurable: true, value: undefined });
		await expect(loadDraftImageAttachments("u", "n")).rejects.toThrow("storage unavailable");
		expect(await loadDraftImageAttachments("", "n")).toEqual({
			images: [],
			textFiles: [],
			droppedCount: 0,
		});
	});
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
		const { records } = installIndexedDbFacade();
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
