import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { readdir, readFile, rm } from "node:fs/promises";
import { REQUEST_DUMP_SPILL_DIR } from "../../api-request-dump-store";
import { getNarraforkPath } from "../../narrafork-home";
import { MALFORMED_REQUEST_DUMP_DIR } from "../malformed-request-dump";
import type { ProviderAdapter } from "../provider";
import type { AgentConfig, AgentEvent } from "../types";
import { ApiError } from "../types";

const UPSTREAM_BODY = JSON.stringify({
	__type: "com.amazon.runtimeservice#ValidationException",
	message: "Improperly formed request.",
	reason: "REQUEST_BODY_INVALID",
});

const REQUEST_BODY = {
	conversationState: {
		conversationId: "conv-malformed",
		history: [
			{
				assistantResponseMessage: {
					content: "",
					toolUses: [{ toolUseId: "tu-orphan", name: "Bash", input: { command: "echo hi" } }],
				},
			},
		],
		currentMessage: {
			userInputMessage: { content: "go", modelId: "claude-sonnet-4.5" },
		},
	},
};

/** "throw" = provider throws; "stream" = SSE error event (NUG relay path). */
let mode: "throw" | "stream" | "unrelated" = "throw";
/** Attempts observed by the provider, so tests can assert the replay count. */
let chatAttempts = 0;
/**
 * Attempt number (1-based) at which the provider starts succeeding. `Infinity` = always
 * fail, which is how the "budget is bounded" assertions are written.
 */
let succeedOnAttempt = Number.POSITIVE_INFINITY;

const testProvider: ProviderAdapter = {
	mayLeakXmlToolCalls: true,
	formatTools: (tools) => tools,
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	async *chat(params) {
		chatAttempts++;
		if (chatAttempts >= succeedOnAttempt) {
			params.onRequestStart?.();
			yield { text: "recovered" };
			return;
		}
		params.requestDump?.setRequest({
			transport: "http",
			url: "https://nug.example.test/v1/messages",
			headers: { Authorization: "Bearer secret-token" },
			body: REQUEST_BODY,
		});
		params.onRequestStart?.();
		if (mode === "throw") {
			params.requestDump?.setResponseMeta({ status: 400 });
			params.requestDump?.setResponseBodyText(UPSTREAM_BODY);
			throw new ApiError(400, `Error: ${UPSTREAM_BODY}`, {
				schema: "narrafork.error-diagnostics.v1",
				statusCode: 400,
			});
		}
		if (mode === "stream") {
			yield {
				invalidState: {
					reason: "validation_exception",
					message: "Improperly formed request.",
					diagnostics: { schema: "narrafork.error-diagnostics.v1", statusCode: 400 },
				},
			};
			return;
		}
		throw new ApiError(400, "some other bad request", {
			schema: "narrafork.error-diagnostics.v1",
			statusCode: 400,
		});
	},
	formatToolResult: (toolUseId, output, isError) => ({ toolUseId, output, isError }),
	pushUserTurn: () => {},
	pushAssistantTurn: () => {},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

/**
 * Delays the loop actually waited, in order.
 *
 * Recorded by stubbing `abortableSleep` rather than measuring elapsed time: the failure
 * being guarded against is "announced 30ms, slept 60ms", and a wall-clock lower bound is
 * satisfied by an over-long sleep. The stub also keeps the suite from really waiting.
 */
const sleepCalls: number[] = [];
const realAbortableSleepModule = { ...(await import("../abortable-sleep")) };
mock.module("../abortable-sleep", () => ({
	...realAbortableSleepModule,
	abortableSleep: async (ms: number) => {
		sleepCalls.push(ms);
	},
}));

/**
 * Provider prefix the loop resolves to. Mutable so one test can exercise a STATEFUL provider
 * (`codex`), where re-sending is not a replay: the server already consumed the request.
 */
let resolvedProviderPrefix = "test";

const realProviderModule = { ...(await import("../provider")) };

mock.module("../provider", () => ({
	...realProviderModule,
	getProvider: () => testProvider,
	resolveProviderAndModel: () => ({
		requestedProvider: resolvedProviderPrefix,
		requestedModel: "test:model",
		provider: resolvedProviderPrefix,
		adapter: testProvider,
		model: "test:model",
	}),
}));

const { agentLoop } = await import("../loop");

const dumpDir = getNarraforkPath(MALFORMED_REQUEST_DUMP_DIR);
const spillDir = getNarraforkPath(REQUEST_DUMP_SPILL_DIR);

beforeEach(async () => {
	await rm(dumpDir, { recursive: true, force: true }).catch(() => {});
	await rm(spillDir, { recursive: true, force: true }).catch(() => {});
	chatAttempts = 0;
	succeedOnAttempt = Number.POSITIVE_INFINITY;
	resolvedProviderPrefix = "test";
});

afterAll(async () => {
	await rm(dumpDir, { recursive: true, force: true }).catch(() => {});
	await rm(spillDir, { recursive: true, force: true }).catch(() => {});
	mock.module("../provider", () => realProviderModule);
	mock.module("../abortable-sleep", () => realAbortableSleepModule);
	mock.restore();
});

function makeConfig(signal: AbortSignal, retryBackoffCeilMs = 0): AgentConfig {
	return {
		narratorId: "n-malformed-loop",
		conversationId: "conv-malformed",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		signal,
		permissionHandler: async () => ({ behavior: "allow" }),
		toolFilter: () => false,
		maxTransientRetries: 0,
		retryBackoffCeilMs,
		maxTurns: 1,
	};
}

async function runLoop(retryBackoffCeilMs = 0): Promise<AgentEvent[]> {
	const events: AgentEvent[] = [];
	const controller = new AbortController();
	for await (const event of agentLoop(
		makeConfig(controller.signal, retryBackoffCeilMs),
		"go",
		[],
	)) {
		events.push(event);
	}
	return events;
}

type ApiEnd = Extract<AgentEvent, { type: "api_request_end" }>;
type CaptureRecord = {
	capture?: { reason?: string; filePath?: string | null; summary?: Record<string, unknown> };
	request?: { body?: unknown };
	response?: { status?: number };
};

describe("agentLoop malformed-request-body capture", () => {
	test("captures the request when the provider throws the upstream ValidationException", async () => {
		mode = "throw";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd).toBeDefined();
		// Forced so the capture survives even with dumping disabled / errors-only.
		expect(apiEnd?.forceDumpPersist).toBe(true);

		const record = apiEnd?.rawDump as CaptureRecord;
		expect(record.capture?.reason).toBe("malformed_request_body");
		const filePath = record.capture?.filePath;
		expect(typeof filePath).toBe("string");
		// The structural summary flags the orphaned toolUse that likely caused the rejection.
		expect(record.capture?.summary?.unmatchedToolUseIds).toEqual(["tu-orphan"]);
		// The capture ANNOTATES the dump; it must never replace it. Handing back a summary
		// and a server-side path while dropping the request is the failure mode the dump
		// exists to prevent, so the rejected body stays in the dump next to the annotation.
		expect(record.request?.body).toEqual(REQUEST_BODY);
		expect(record.response?.status).toBe(400);

		const saved = JSON.parse(await readFile(filePath as string, "utf8")) as {
			request: { body: unknown; headers: Record<string, string> };
			response: { status?: number; bodyText?: string };
		};
		// The exact request body that upstream rejected is on disk, verbatim.
		expect(saved.request.body).toEqual(REQUEST_BODY);
		expect(JSON.stringify(saved.request.headers)).not.toContain("secret-token");
		expect(saved.response.status).toBe(400);
		expect(saved.response.bodyText).toContain("REQUEST_BODY_INVALID");
	});

	test("captures the request when the rejection arrives as a stream error event", async () => {
		mode = "stream";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd?.forceDumpPersist).toBe(true);
		const record = apiEnd?.rawDump as CaptureRecord;
		expect(record.capture?.reason).toBe("malformed_request_body");
		// Same rule on the stream path: annotate, never replace.
		expect(record.request?.body).toEqual(REQUEST_BODY);

		const saved = JSON.parse(await readFile(record.capture?.filePath as string, "utf8")) as {
			request: { body: unknown };
		};
		expect(saved.request.body).toEqual(REQUEST_BODY);
	});

	test("does not capture unrelated 400 errors", async () => {
		mode = "unrelated";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnd?.forceDumpPersist).toBeFalsy();
		expect((apiEnd?.rawDump as CaptureRecord)?.capture).toBeUndefined();
	});
});

/**
 * This rejection is ~0.02% of requests and 39 of 40 recorded occurrences cleared on
 * their own with the request unchanged, so the turn must not die on the first one. The
 * budget is bounded because the error carries no signal separating "upstream hiccup" from
 * "this body really is invalid" — an unbounded retry would re-send the whole history
 * forever to reach the same hard failure.
 */
describe("agentLoop malformed-request-body replay", () => {
	test("replays twice, then gives up", async () => {
		mode = "throw";
		const events = await runLoop();

		// 1 initial attempt + 2 replays.
		expect(chatAttempts).toBe(3);
		const retries = events.filter((e) => e.type === "retrying");
		expect(retries.map((e) => (e as { attempt: number }).attempt)).toEqual([1, 2]);
		expect(retries.every((e) => (e as { maxRetries: number }).maxRetries === 2)).toBe(true);
	});

	test("recovers silently when a replay succeeds", async () => {
		mode = "throw";
		succeedOnAttempt = 2; // fails once, then the upstream clears
		const events = await runLoop();

		expect(chatAttempts).toBe(2);
		expect(events.filter((e) => e.type === "retrying")).toHaveLength(1);
		// The turn completed rather than surfacing the rejection to the user.
		expect(events.some((e) => e.type === "error")).toBe(false);
	});

	test("replays the stream-error form of the rejection too", async () => {
		// NUG relays the same rejection as an SSE invalidState event rather than a throw, and
		// that path has its own classification branch — it would silently keep dying if only
		// the throw path replayed.
		mode = "stream";
		succeedOnAttempt = 3;
		const events = await runLoop();

		expect(chatAttempts).toBe(3);
		expect(events.filter((e) => e.type === "retrying")).toHaveLength(2);
		expect(events.some((e) => e.type === "error")).toBe(false);
	});

	test("does not replay unrelated 400s", async () => {
		mode = "unrelated";
		await runLoop();
		// One attempt only: the ordinary retry budget is 0 in this config, so any replay here
		// would mean the malformed-body path had captured an error that is not its own.
		expect(chatAttempts).toBe(1);
	});

	test("does not replay for a stateful provider, whose request was already consumed", async () => {
		// The ordinary retry path grants 0 retries to stateful providers (codex/responses)
		// because the server consumed the request; re-sending it is not a replay. This path
		// needed the same guard and did not have it: `isMalformedRequestBodyError` matches on
		// response TEXT and is scoped to no channel, so any codex error whose body happens to
		// contain "Improperly formed request." would replay a consumed request.
		mode = "throw";
		resolvedProviderPrefix = "codex";
		const events = await runLoop();

		expect(chatAttempts).toBe(1);
		expect(events.filter((e) => e.type === "retrying")).toHaveLength(0);
		// The capture still happens — the evidence is the point, only the replay is unsafe.
		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect((apiEnd?.rawDump as CaptureRecord).capture?.reason).toBe("malformed_request_body");
	});

	test("the announced delay is the delay actually waited", async () => {
		// The backoff used to be computed twice — once for the `retrying` event, once for the
		// sleep — with the attempt counter incremented in between. They agreed only because
		// nothing moved the counter between the two calls, and a UI announcing "retrying in 2s"
		// while sleeping 4s produces no error to notice.
		//
		// Asserting on wall-clock elapsed >= sum(announced) does NOT catch that: sleeping
		// LONGER than announced — the actual bug — passes it. So the sleep is stubbed and its
		// arguments are compared to the announced delays directly. That is both stricter and
		// independent of machine timing.
		mode = "throw";
		sleepCalls.length = 0;
		const events = await runLoop(30);

		// `delayMs` is optional on the event, so a missing one must fail loudly rather than
		// compare equal to a missing sleep.
		const announced = events
			.filter((e) => e.type === "retrying")
			.map((e) => (e as { delayMs?: number }).delayMs)
			.filter((delay): delay is number => typeof delay === "number");
		expect(announced).toEqual([30, 30]);
		// One sleep per replay, each for exactly the announced duration.
		expect(sleepCalls).toEqual(announced);
	});

	/**
	 * The malformed-dump directory is guarded by `malformedDumpPath`, but each attempt also
	 * persists its own force-saved `api_requests` row whose dump spills to a SECOND directory.
	 * That one had no guard, so three attempts wrote three near-identical multi-MB files and,
	 * through newest-N pruning, evicted unrelated captures to keep three copies of one request.
	 *
	 * The loop's half of the fix is this token: it must be present, identical on every attempt,
	 * and absent for rejections that are not malformed-body replays (otherwise unrelated
	 * requests would start sharing a dump file). Whether the token actually collapses the files
	 * is asserted in `api-request-tracker.test.ts`, which owns the spill decision.
	 */
	test("every attempt of one rejection carries the same spill-reuse token", async () => {
		mode = "throw";
		const events = await runLoop();

		const tokens = events
			.filter((e): e is ApiEnd => e.type === "api_request_end")
			.map((e) => (e as { dumpSpillReuseToken?: string }).dumpSpillReuseToken);
		expect(tokens).toHaveLength(3);
		expect(tokens.every((token) => typeof token === "string" && token.length > 0)).toBe(true);
		expect(new Set(tokens).size).toBe(1);
	});

	test("an unrelated rejection gets no spill-reuse token", async () => {
		// Sharing a dump file across unrelated requests would make a download answer for the
		// wrong request, so the token must never be handed out by default.
		mode = "unrelated";
		const events = await runLoop();

		const apiEnd = events.find((e): e is ApiEnd => e.type === "api_request_end");
		expect((apiEnd as { dumpSpillReuseToken?: string })?.dumpSpillReuseToken).toBeUndefined();
	});

	test("writes one dump file per turn, not one per replay", async () => {
		// The replays re-send an identical body. Writing a file each time would store several
		// MB of near-duplicates and, through the newest-N pruning, evict unrelated captures —
		// losing other evidence to keep three copies of the same request.
		mode = "throw";
		const events = await runLoop();
		expect(chatAttempts).toBe(3);

		const files = await readdir(dumpDir);
		expect(files).toHaveLength(1);

		// Every attempt's row still carries the annotation pointing at that one file, so no
		// attempt looks like an ordinary failure with no evidence.
		const apiEnds = events.filter((e): e is ApiEnd => e.type === "api_request_end");
		expect(apiEnds).toHaveLength(3);
		const paths = apiEnds.map((e) => (e.rawDump as CaptureRecord).capture?.filePath);
		expect(new Set(paths).size).toBe(1);
		expect(paths[0]).toContain(dumpDir);
	});
});
