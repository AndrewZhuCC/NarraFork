import { afterEach, beforeEach, expect, spyOn, test } from "bun:test";
import { clearToken, setToken } from "@frontend/lib/api/client";
import type { PretextDocumentPageResult } from "@frontend/lib/api/types";
import { peekCachedPretextDocument, writeCachedPretextDocument } from "./pretext-document-cache";
import {
	loadPretextDocumentTail,
	type PretextDocumentFetchPage,
	preloadPretextDocumentTail,
} from "./pretext-document-loader";

const originalStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
const page: PretextDocumentPageResult = {
	messages: [],
	minSeq: null,
	maxSeq: null,
	hasPrev: false,
	hasNext: false,
	messageVersion: 7,
};
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
beforeEach(() => {
	const store = new Map<string, string>();
	Object.defineProperty(globalThis, "localStorage", {
		configurable: true,
		value: {
			getItem: (key: string) => store.get(key) ?? null,
			setItem: (key: string, value: string) => store.set(key, value),
			removeItem: (key: string) => store.delete(key),
		},
	});
	setToken("principal-a");
});
afterEach(() => {
	clearToken();
	if (originalStorage) Object.defineProperty(globalThis, "localStorage", originalStorage);
	else Reflect.deleteProperty(globalThis, "localStorage");
});
test("preload and list mount share one LOD-bounded tail even if LOD changes", async () => {
	const pending = deferred<PretextDocumentPageResult>();
	let calls = 0;
	const fetchPage: PretextDocumentFetchPage = async (_id, opts) => {
		calls++;
		expect(opts.limit).toBe(40);
		expect(opts.maxResponseBytes).toBe(8 * 1024 * 1024);
		return pending.promise;
	};
	const preload = preloadPretextDocumentTail("n", { fetchPage, firstScreenPageSize: 40 });
	const mounted = loadPretextDocumentTail("n", { fetchPage, firstScreenPageSize: 100 });
	expect(calls).toBe(1);
	pending.resolve(page);
	expect(await preload).toBe(await mounted);
});
test("completed preload hands off once without a second tail", async () => {
	let calls = 0;
	const fetchPage: PretextDocumentFetchPage = async () => {
		calls++;
		return page;
	};
	const preloaded = await preloadPretextDocumentTail("n", { fetchPage });
	expect(await loadPretextDocumentTail("n", { fetchPage })).toBe(preloaded);
	expect(calls).toBe(1);
	await loadPretextDocumentTail("n", { fetchPage });
	expect(calls).toBe(2);
});
test("one cancelled waiter never aborts another mounted panel", async () => {
	const pending = deferred<PretextDocumentPageResult>();
	let networkSignal: AbortSignal | undefined;
	const fetchPage: PretextDocumentFetchPage = async (_id, opts) => {
		networkSignal = opts.signal;
		return pending.promise;
	};
	const controller = new AbortController();
	const preload = preloadPretextDocumentTail("n", { fetchPage, signal: controller.signal });
	const rejected = preload.catch((error: Error) => error);
	const mounted = loadPretextDocumentTail("n", { fetchPage });
	controller.abort();
	await rejected;
	expect(networkSignal?.aborted).toBe(false);
	pending.resolve(page);
	expect((await mounted).messageVersion).toBe(7);
});
test("last waiter cancellation aborts transport and a late response cannot enter the next load", async () => {
	const old = deferred<PretextDocumentPageResult>();
	let calls = 0;
	let oldSignal: AbortSignal | undefined;
	const fetchPage: PretextDocumentFetchPage = async (_id, opts) => {
		calls++;
		if (calls === 1) {
			oldSignal = opts.signal;
			return old.promise;
		}
		return { ...page, messageVersion: 8 };
	};
	const controller = new AbortController();
	const preload = preloadPretextDocumentTail("n", { fetchPage, signal: controller.signal });
	const rejected = preload.catch((error: Error) => error);
	controller.abort();
	await rejected;
	expect(oldSignal?.aborted).toBe(true);
	const next = await loadPretextDocumentTail("n", { fetchPage });
	old.resolve(page);
	expect(next.messageVersion).toBe(8);
	expect(calls).toBe(2);
});
for (const panels of [5, 8]) {
	for (const preload of [false, true]) {
		test(`${panels} panels${preload ? " with preload + mount waiters" : ""} never evict a live transport`, async () => {
			const requests = new Map<string, ReturnType<typeof deferred<PretextDocumentPageResult>>>();
			const signals = new Map<string, AbortSignal>();
			let calls = 0;
			const fetchPage: PretextDocumentFetchPage = async (id, opts) => {
				calls++;
				const pending = deferred<PretextDocumentPageResult>();
				requests.set(id, pending);
				if (opts.signal) signals.set(id, opts.signal);
				return pending.promise;
			};
			const ids = Array.from({ length: panels }, (_, index) => `panel-${index}`);
			const preloads = preload
				? ids.map((id) => preloadPretextDocumentTail(id, { fetchPage }))
				: [];
			const mounted = ids.map((id) => loadPretextDocumentTail(id, { fetchPage }));
			const all = Promise.all([...preloads, ...mounted]);
			const failed = all.catch(() => {});
			try {
				expect(calls).toBe(panels);
				expect(signals.size).toBe(panels);
				for (const signal of signals.values()) expect(signal.aborted).toBe(false);
				for (const [index, id] of ids.entries())
					requests.get(id)?.resolve({ ...page, messageVersion: 100 + index });
				const results = await all;
				const versions = ids.map((_id, index) => 100 + index);
				expect(results.map((result) => result.messageVersion)).toEqual(
					preload ? [...versions, ...versions] : versions,
				);
			} finally {
				for (const request of requests.values()) request.resolve(page);
				await failed;
			}
		});
	}
}

test("only four completed idle handoffs are retained while an older live request remains intact", async () => {
	let calls = 0;
	const pending = deferred<PretextDocumentPageResult>();
	let liveSignal: AbortSignal | undefined;
	const fetchPage: PretextDocumentFetchPage = async (id, opts) => {
		calls++;
		if (id === "live") {
			liveSignal = opts.signal;
			return pending.promise;
		}
		return page;
	};
	const live = loadPretextDocumentTail("live", { fetchPage });
	const failed = live.catch(() => {});
	try {
		for (let index = 0; index < 8; index++)
			await preloadPretextDocumentTail(`idle-${index}`, { fetchPage });
		expect(calls).toBe(9);
		for (let index = 4; index < 8; index++)
			await loadPretextDocumentTail(`idle-${index}`, { fetchPage });
		expect(calls).toBe(9);
		for (let index = 0; index < 4; index++)
			await loadPretextDocumentTail(`idle-${index}`, { fetchPage });
		expect(calls).toBe(13);
		expect(liveSignal?.aborted).toBe(false);
		pending.resolve(page);
		expect((await live).messageVersion).toBe(7);
	} finally {
		pending.resolve(page);
		await failed;
	}
});

test("32 active transports reject a new narrator without aborting existing consumers or shared waiters", async () => {
	const pending = deferred<PretextDocumentPageResult>();
	const signals: AbortSignal[] = [];
	let calls = 0;
	const fetchPage: PretextDocumentFetchPage = async (_id, opts) => {
		calls++;
		if (opts.signal) signals.push(opts.signal);
		return pending.promise;
	};
	const mounted = Array.from({ length: 32 }, (_, index) =>
		loadPretextDocumentTail(`active-${index}`, { fetchPage }),
	);
	const joined = loadPretextDocumentTail("active-0", { fetchPage });
	const all = Promise.all([...mounted, joined]);
	const failed = all.catch(() => {});
	try {
		const overflow = await loadPretextDocumentTail("active-32", { fetchPage }).catch(
			(error: Error) => error,
		);
		expect(overflow).toBeInstanceOf(Error);
		expect(overflow instanceof Error && overflow.message).toContain("limit 32");
		expect(calls).toBe(32);
		for (const signal of signals) expect(signal.aborted).toBe(false);
		pending.resolve(page);
		expect(await all).toHaveLength(33);
		await loadPretextDocumentTail("active-32", { fetchPage });
		expect(calls).toBe(33);
	} finally {
		pending.resolve(page);
		await failed;
	}
});

test("superseded force-reload transports still count against the active admission budget", async () => {
	const pending = deferred<PretextDocumentPageResult>();
	let calls = 0;
	const fetchPage: PretextDocumentFetchPage = async () => {
		calls++;
		return pending.promise;
	};
	const mounted = Array.from({ length: 32 }, () =>
		loadPretextDocumentTail("force", { fetchPage, forceTailReload: true }),
	);
	const all = Promise.all(mounted);
	const failed = all.catch(() => {});
	try {
		const overflow = await loadPretextDocumentTail("force", {
			fetchPage,
			forceTailReload: true,
		}).catch((error: Error) => error);
		expect(overflow).toBeInstanceOf(Error);
		expect(calls).toBe(32);
		pending.resolve(page);
		expect(await all).toHaveLength(32);
	} finally {
		pending.resolve(page);
		await failed;
	}
});

test("the shared deadline rejects every waiter even if a custom fetcher ignores abort", async () => {
	const nativeSetTimeout = globalThis.setTimeout;
	const deadlines: Array<() => void> = [];
	const timerSpy = spyOn(globalThis, "setTimeout").mockImplementation(((
		callback: (...args: unknown[]) => void,
		delay = 0,
		...args: unknown[]
	) => {
		if (delay === 15_000) {
			deadlines.push(() => callback(...args));
			return nativeSetTimeout(() => {}, delay);
		}
		return nativeSetTimeout(callback, delay, ...args);
	}) as typeof setTimeout);
	const pending = deferred<PretextDocumentPageResult>();
	let signal: AbortSignal | undefined;
	const fetchPage: PretextDocumentFetchPage = async (_id, opts) => {
		signal = opts.signal;
		return pending.promise;
	};
	const preload = preloadPretextDocumentTail("timeout", { fetchPage }).catch(
		(error: Error) => error,
	);
	const mounted = loadPretextDocumentTail("timeout", { fetchPage }).catch((error: Error) => error);
	try {
		expect(deadlines).toHaveLength(1);
		deadlines[0]?.();
		expect(signal?.aborted).toBe(true);
		expect(await preload).toBeInstanceOf(Error);
		expect(await mounted).toBeInstanceOf(Error);
	} finally {
		pending.resolve(page);
		timerSpy.mockRestore();
	}
});

test("narrator and authentication switches never reuse an old response or document cache", async () => {
	const old = deferred<PretextDocumentPageResult>();
	let calls = 0;
	const fetchPage: PretextDocumentFetchPage = async (id) => {
		calls++;
		return id === "old" ? old.promise : page;
	};
	const previous = preloadPretextDocumentTail("old", { fetchPage });
	const rejected = previous.catch((error: Error) => error);
	await loadPretextDocumentTail("other", { fetchPage });
	expect(calls).toBe(2);
	writeCachedPretextDocument({
		narratorId: "old",
		input: {
			messages: [{ id: "private", seq: 1 } as never],
			messageVersion: 7,
			hasPrev: false,
			oldestLoadedSeq: 1,
		},
	});
	setToken("principal-b");
	expect(peekCachedPretextDocument("old")).toBeNull();
	old.resolve(page);
	await rejected;
});
