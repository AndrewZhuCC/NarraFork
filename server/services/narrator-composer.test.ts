import {
	afterAll,
	afterEach,
	beforeEach,
	describe,
	expect,
	mock,
	setSystemTime,
	spyOn,
	test,
} from "bun:test";
import {
	CODEX_COMPOSER_MAX_SEED_BYTES,
	type CodexComposerRunOptions,
	type CodexComposerSeed,
	getCodexComposerSettings,
} from "@server/lib/agent/codex-composer";
import type { GenerateMetaResult } from "@server/lib/agent/provider";
import type {
	ApiRequestFinishOptions,
	ApiRequestHandle,
	ApiRequestStartOptions,
} from "@server/lib/api-request-tracker";
import { settings } from "@server/lib/settings";

// Only side-effect boundaries are stubbed: no DB, provider, transport, or real model.
const broadcast = mock((_narratorId: string, _message: unknown) => {});
const startRequest = mock(
	(options: ApiRequestStartOptions) =>
		({ ...options, id: "composer-request", startTime: Date.now() }) as ApiRequestHandle,
);
const finishRequest = mock(
	async (_handle: ApiRequestHandle, _options: ApiRequestFinishOptions) => "composer-request",
);
const warn = mock((_message: string, _data?: Record<string, unknown>) => {});
mock.module("@server/websocket/narrator-ws", () => ({ broadcastToNarrator: broadcast }));
mock.module("@server/lib/api-request-tracker", () => ({
	startApiRequest: startRequest,
	finishApiRequest: finishRequest,
	isRequestDumpEnabled: () => settings.agent.requestDumpEnabled === true,
	isRequestDumpErrorsOnly: () => settings.agent.requestDumpErrorsOnly === true,
}));
mock.module("@server/lib/logger", () => ({ logger: { debug: () => {}, warn } }));

const {
	beginComposerRun,
	rememberComposerSeed,
	startComposerPrediction,
	getComposerSuggestion,
	invalidateComposerSuggestion,
	invalidateAllComposerSuggestions,
	shutdownComposerSuggestions,
} = await import("./narrator-composer");

const cleanup: Array<(result: GenerateMetaResult) => void> = [];
const answer = (suggestion: string | null = "继续") => ({ text: JSON.stringify({ suggestion }) });
const settle = () => new Promise<void>((resolve) => setImmediate(resolve));
function deferred() {
	let resolve!: (result: GenerateMetaResult) => void;
	const promise = new Promise<GenerateMetaResult>((done) => {
		resolve = done;
	});
	cleanup.push(resolve);
	return { promise, resolve };
}
function seed(run: CodexComposerSeed["run"], bytes = 1): CodexComposerSeed {
	return {
		providerId: "codex",
		model: "test-codex-model",
		conversationId: "ordinary-conversation",
		configSignature: getCodexComposerSettings("codex")?.signature ?? "disabled",
		bytes,
		run,
	};
}
function prepare(narratorId: string, value: CodexComposerSeed): string {
	const generation = beginComposerRun(narratorId);
	rememberComposerSeed(narratorId, generation, value, "initiating-user");
	return generation;
}
function codexSettings() {
	if (!settings.codex) throw new Error("Missing Codex test settings");
	return settings.codex;
}

beforeEach(() => {
	codexSettings().useComposer = true;
	settings.agent.requestDumpEnabled = false;
	broadcast.mockClear();
	startRequest.mockClear();
	finishRequest.mockClear();
	warn.mockClear();
});
afterEach(async () => {
	invalidateAllComposerSuggestions();
	for (const resolve of cleanup.splice(0)) resolve(answer(null));
	await settle();
	setSystemTime();
});
afterAll(() => mock.restore());

describe("server Composer state", () => {
	test("disabled settings and GET never initiate a model request", async () => {
		const run = mock(async () => answer());
		const value = seed(run);
		codexSettings().useComposer = false;
		const generation = prepare("disabled", value);
		expect(getComposerSuggestion("unknown")).toEqual({ suggestion: null });
		expect(getComposerSuggestion("disabled")).toEqual({ suggestion: null });
		startComposerPrediction("disabled", generation);
		await settle();
		expect(run).not.toHaveBeenCalled();
		expect(startRequest).not.toHaveBeenCalled();
	});

	test("latest seed replaces or clears an older snapshot without discarding the generation", async () => {
		const old = mock(async () => answer("旧结果"));
		const latest = mock(async () => answer("最新结果"));
		const generation = prepare("n", seed(old));
		rememberComposerSeed("n", generation, undefined);
		startComposerPrediction("n", generation);
		expect(old).not.toHaveBeenCalled();
		rememberComposerSeed("n", generation, seed(old));
		rememberComposerSeed("n", generation, seed(latest));
		startComposerPrediction("n", generation);
		startComposerPrediction("n", generation);
		await settle();
		expect(old).not.toHaveBeenCalled();
		expect(latest).toHaveBeenCalledTimes(1);
		expect(getComposerSuggestion("n")).toEqual({ suggestion: "最新结果" });
		expect(startRequest).toHaveBeenCalledWith(
			expect.objectContaining({ kind: "composer", userId: null }),
		);
		expect(broadcast).toHaveBeenLastCalledWith("n", {
			type: "composer_suggestion_changed",
			narratorId: "n",
		});
	});

	test("cancelled late results and stale seeds cannot overwrite a new generation", async () => {
		const wait = deferred();
		const old = mock((_options: CodexComposerRunOptions) => wait.promise);
		const first = prepare("n", seed(old));
		startComposerPrediction("n", first);
		const latest = mock(async () => answer("新结果"));
		const second = beginComposerRun("n");
		expect(old.mock.calls[0]?.[0].signal.aborted).toBe(true);
		rememberComposerSeed("n", first, seed(old));
		rememberComposerSeed("n", second, seed(latest));
		startComposerPrediction("n", second);
		await settle();
		const notifications = broadcast.mock.calls.length;
		wait.resolve({ ...answer("迟到的旧结果"), usage: { inputTokens: 10, outputTokens: 2 } });
		await settle();
		expect(getComposerSuggestion("n")).toEqual({ suggestion: "新结果" });
		expect(broadcast.mock.calls.length).toBe(notifications);
		expect(finishRequest.mock.calls.at(-1)?.[1].usage?.inputTokens).toBe(10);
	});

	test("a foreground request cancels a running prediction even within the same generation", async () => {
		const wait = deferred();
		const run = mock((_options: CodexComposerRunOptions) => wait.promise);
		const generation = prepare("foreground", seed(run));
		startComposerPrediction("foreground", generation);
		rememberComposerSeed("foreground", generation, undefined);
		expect(run.mock.calls[0]?.[0].signal.aborted).toBe(true);
		wait.resolve(answer("stale suggestion"));
		await settle();
		expect(getComposerSuggestion("foreground")).toEqual({ suggestion: null });
	});
	test("configuration changes invalidate seeds, in-flight publication, and cached reads", async () => {
		const run = mock(async () => answer());
		const beforeStart = prepare("before-start", seed(run));
		codexSettings().composerPrompt = "Changed before start";
		startComposerPrediction("before-start", beforeStart);
		expect(run).not.toHaveBeenCalled();
		const wait = deferred();
		const during = prepare(
			"during",
			seed(() => wait.promise),
		);
		startComposerPrediction("during", during);
		codexSettings().composerPrompt = "Changed during request";
		wait.resolve(answer());
		await settle();
		expect(getComposerSuggestion("during")).toEqual({ suggestion: null });
		const completed = prepare("completed", seed(run));
		startComposerPrediction("completed", completed);
		await settle();
		expect(getComposerSuggestion("completed").suggestion).toBe("继续");
		codexSettings().useComposer = false;
		expect(getComposerSuggestion("completed")).toEqual({ suggestion: null });
		expect(run).toHaveBeenCalledTimes(1);
	});

	test("two concurrent slots never queue, retry, or free a still-unsettled cancelled request", async () => {
		const wait = deferred();
		const first = mock((_options: CodexComposerRunOptions) => wait.promise);
		startComposerPrediction("a", prepare("a", seed(first)));
		startComposerPrediction(
			"b",
			prepare(
				"b",
				seed(() => wait.promise),
			),
		);
		invalidateComposerSuggestion("a");
		const dropped = mock(async () => answer());
		const third = prepare("c", seed(dropped));
		startComposerPrediction("c", third);
		expect(dropped).not.toHaveBeenCalled();
		expect(first.mock.calls[0]?.[0].signal.aborted).toBe(true);
		wait.resolve(answer());
		await settle();
		startComposerPrediction("c", third);
		expect(dropped).not.toHaveBeenCalled();
		startComposerPrediction("d", prepare("d", seed(dropped)));
		await settle();
		expect(dropped).toHaveBeenCalledTimes(1);
	});

	test("single-seed and total byte limits discard old snapshots and release replacement reservations", async () => {
		const tooLarge = mock(async () => answer());
		const oversized = prepare("oversized", seed(tooLarge, CODEX_COMPOSER_MAX_SEED_BYTES + 1));
		startComposerPrediction("oversized", oversized);
		expect(tooLarge).not.toHaveBeenCalled();
		const entries = Array.from({ length: 5 }, (_, index) => {
			const run = mock(async () => answer(String(index)));
			return {
				id: `budget-${index}`,
				run,
				generation: prepare(`budget-${index}`, seed(run, CODEX_COMPOSER_MAX_SEED_BYTES)),
			};
		});
		for (const entry of entries) {
			startComposerPrediction(entry.id, entry.generation);
			await settle();
		}
		expect(entries[0]?.run).not.toHaveBeenCalled();
		for (const entry of entries.slice(1)) expect(entry.run).toHaveBeenCalledTimes(1);
		const replacement = prepare("replacement", seed(tooLarge, CODEX_COMPOSER_MAX_SEED_BYTES));
		rememberComposerSeed("replacement", replacement, undefined);
		const survivors = Array.from({ length: 4 }, (_, index) => {
			const run = mock(async () => answer());
			return {
				id: `released-${index}`,
				run,
				generation: prepare(`released-${index}`, seed(run, CODEX_COMPOSER_MAX_SEED_BYTES)),
			};
		});
		for (const entry of survivors) {
			startComposerPrediction(entry.id, entry.generation);
			await settle();
			expect(entry.run).toHaveBeenCalledTimes(1);
		}
	});

	test("generation/cache entries are bounded and cached suggestions expire after one hour", async () => {
		const run = mock(async () => answer());
		const oldest = prepare("oldest", seed(run));
		startComposerPrediction("oldest", oldest);
		await settle();
		expect(getComposerSuggestion("oldest").suggestion).toBe("继续");
		for (let index = 0; index < 256; index++) beginComposerRun(`empty-${index}`);
		expect(getComposerSuggestion("oldest")).toEqual({ suggestion: null });
		rememberComposerSeed("oldest", oldest, seed(run));
		startComposerPrediction("oldest", oldest);
		expect(run).toHaveBeenCalledTimes(1);
		startComposerPrediction("fresh", prepare("fresh", seed(run)));
		await settle();
		setSystemTime(Date.now() + 60 * 60 * 1000 + 1);
		expect(getComposerSuggestion("fresh")).toEqual({ suggestion: null });
		expect(run).toHaveBeenCalledTimes(2);
	});

	test("host validation failures retain consumed usage and error-only dumps without unsafe logs", async () => {
		settings.agent.requestDumpEnabled = true;
		settings.agent.requestDumpErrorsOnly = true;
		const invalidText = '{"suggestion":"wrong","extra":true}';
		const run = mock(async ({ requestDump }: CodexComposerRunOptions) => {
			requestDump?.setRequest({
				method: "POST",
				url: "https://composer.test.invalid",
				body: { input: [] },
			});
			requestDump?.setResponseBodyText(invalidText);
			return {
				text: invalidText,
				usage: { inputTokens: 100, cachedInputTokens: 99, outputTokens: 3 },
				credentialId: "ordinary-account",
			};
		});
		startComposerPrediction("invalid", prepare("invalid", seed(run)));
		await settle();
		expect(startRequest.mock.calls[0]?.[0].userId).toBe("initiating-user");
		const recorded = finishRequest.mock.calls[0]?.[1];
		expect(recorded?.usage).toEqual({ inputTokens: 100, cachedInputTokens: 99, outputTokens: 3 });
		expect(recorded?.credentialId).toBe("ordinary-account");
		expect(recorded?.errorMessage).toBe("Invalid Composer suggestion object");
		expect(recorded?.rawDump).toMatchObject({
			response: { bodyText: invalidText, error: "Invalid Composer suggestion object" },
		});
		expect(getComposerSuggestion("invalid")).toEqual({ suggestion: null });
		expect(JSON.stringify(warn.mock.calls)).not.toContain(invalidText);
		const valid = mock(async () => answer());
		startComposerPrediction("valid", prepare("valid", seed(valid)));
		await settle();
		expect(finishRequest.mock.calls.at(-1)?.[1].rawDump).toBeUndefined();
	});

	test("the 90s deadline aborts the original signal and prevents late publication", async () => {
		const original = globalThis.setTimeout;
		let expire: (() => void) | undefined;
		const timer = spyOn(globalThis, "setTimeout").mockImplementation(
			Object.assign((...args: Parameters<typeof globalThis.setTimeout>) => {
				const [handler, delay, ...values] = args;
				if (delay === 90_000) expire = () => handler(...values);
				return original(...args);
			}, original),
		);
		try {
			const wait = deferred();
			const run = mock((_options: CodexComposerRunOptions) => wait.promise);
			startComposerPrediction("timeout", prepare("timeout", seed(run)));
			expect(expire).toBeDefined();
			expire?.();
			expect(run.mock.calls[0]?.[0].signal.aborted).toBe(true);
			wait.resolve(answer());
			await settle();
			expect(getComposerSuggestion("timeout")).toEqual({ suggestion: null });
			expect(finishRequest.mock.calls[0]?.[1].errorMessage).toBe("Composer prediction timed out");
		} finally {
			timer.mockRestore();
		}
	});

	test("shutdown cancels outstanding tasks and has a finite wait even when producers ignore abort", async () => {
		const original = globalThis.setTimeout;
		let finishWait: (() => void) | undefined;
		const timer = spyOn(globalThis, "setTimeout").mockImplementation(
			Object.assign((...args: Parameters<typeof globalThis.setTimeout>) => {
				const [handler, delay, ...values] = args;
				if (delay === 2_000) finishWait = () => handler(...values);
				return original(...args);
			}, original),
		);
		try {
			const wait = deferred();
			const run = mock((_options: CodexComposerRunOptions) => wait.promise);
			startComposerPrediction("shutdown-a", prepare("shutdown-a", seed(run)));
			startComposerPrediction("shutdown-b", prepare("shutdown-b", seed(run)));
			const shutdown = expect(shutdownComposerSuggestions()).rejects.toThrow("shutdown timeout");
			for (const [options] of run.mock.calls) expect(options.signal.aborted).toBe(true);
			expect(finishWait).toBeDefined();
			finishWait?.();
			await shutdown;
			wait.resolve(answer());
			await settle();
			expect(getComposerSuggestion("shutdown-a")).toEqual({ suggestion: null });
			startComposerPrediction("shutdown-new", prepare("shutdown-new", seed(run)));
			expect(run).toHaveBeenCalledTimes(2);
		} finally {
			timer.mockRestore();
		}
	});
});
