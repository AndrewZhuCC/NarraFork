/**
 * Mid-stream credential failover in the plugin's chat path.
 *
 * ## The failure being pinned
 *
 * Kiro delivers an account suspension as an `exception` frame inside an HTTP **200** body. By
 * then `sendChatRequest` has returned and already recorded the credential as successful, so its
 * retry loop cannot react. The HTTP client exposes `reportStreamFailure` and
 * `retryWithNextCredential` for exactly this seam — and the plugin called neither, so a suspended
 * account was never marked, never excluded, and every later request (each compact retry included)
 * reselected it.
 *
 * ## What these tests actually exercise
 *
 * The real `streamWithFailover`, driven by a stub that mimics the client's contract: replay
 * budget, exclusion set, and slot release. The failure lived in the seam between the HTTP client
 * and the stream reader, so the tests assert the *interactions* — which credentials were marked,
 * whether a replay happened, and whether slots were released — rather than only the final text.
 *
 * Release accounting matters as much as the failover: `release()` frees a concurrency slot, so
 * leaking one on the replay path would slowly starve the provider until the process restarted.
 */

import { describe, expect, test } from "bun:test";
import { KiroApiError } from "@server/lib/kiro/types";
import type { ParsedStreamEvent } from "@shared/codewhisperer/types";
import {
	type FailoverChatResponse,
	isSuspensionStreamError,
	streamWithFailover,
} from "../../../../examples/plugins/kiro-external/src/stream-failover";

/**
 * An account suspension as the event-stream decoder raises it from an exception frame.
 *
 * The real upstream wording, not a paraphrase: the classifier requires *both* a suspension-capable
 * exception type and suspension prose that names the account lock, precisely so a bare
 * `AccessDeniedException` — an ordinary authorization failure, such as a profileArn the credential
 * may not use — cannot durably disable a healthy account.
 */
function suspensionError(): KiroApiError {
	// The decoder puts `:exception-type` in `code` and the raw payload in `message`, and the
	// payload is a JSON envelope — not bare prose. Getting that wrong makes the classifier return
	// false and the whole suite pass for the wrong reason.
	return new KiroApiError(
		"com.amazon.aws.codewhisperer#AccessDeniedException",
		JSON.stringify({
			__type: "com.amazon.aws.codewhisperer#AccessDeniedException",
			message:
				"Your User ID (e498f488-e0e1-70b4-9543-44c437a0089f) temporarily is suspended. We've locked your account as a security precaution. To restore access, please contact our support team to verify your identity.",
		}),
	);
}

interface ScriptedAttempt {
	credentialId: string;
	/** Text chunks to emit before `fail`, if any. */
	chunks?: string[];
	/** Error thrown after the chunks. */
	fail?: unknown;
	/** No body at all, as a truncated response would give. */
	noBody?: boolean;
}

interface Recorder {
	marked: Array<{ credentialId: string; reason?: string }>;
	releases: string[];
	replays: number;
}

/**
 * Build a chain of stubbed responses.
 *
 * `hasAlternative` mirrors what `reportStreamFailure` returns — whether the manager still has a
 * usable credential — because the replay decision depends on it.
 */
function scriptResponses(
	attempts: ScriptedAttempt[],
	options: { hasAlternative?: boolean; replayBudget?: number } = {},
): { first: FailoverChatResponse; recorder: Recorder } {
	const recorder: Recorder = { marked: [], releases: [], replays: 0 };
	let budget = options.replayBudget ?? attempts.length;

	function build(index: number): FailoverChatResponse {
		const attempt = attempts[index];
		if (!attempt) throw new Error(`no scripted attempt at index ${index}`);
		return {
			credentialId: attempt.credentialId,
			response: {
				body: attempt.noBody
					? null
					: new ReadableStream<Uint8Array>({
							start(controller) {
								controller.enqueue(new Uint8Array([0]));
								controller.close();
							},
						}),
			},
			release: () => {
				recorder.releases.push(attempt.credentialId);
			},
			reportStreamFailure: (reason) => {
				recorder.marked.push({ credentialId: attempt.credentialId, reason });
				return options.hasAlternative ?? index + 1 < attempts.length;
			},
			retryWithNextCredential: async () => {
				if (budget <= 0) {
					throw new KiroApiError(
						"STREAM_REPLAY_EXHAUSTED",
						"Exhausted credential replays after mid-stream failures",
					);
				}
				budget -= 1;
				recorder.replays += 1;
				return build(index + 1);
			},
		};
	}

	return { first: build(0), recorder };
}

/** A parser that replays a script rather than decoding bytes. */
function scriptedParser(attempts: ScriptedAttempt[]) {
	let call = 0;
	return (_body: ReadableStream<Uint8Array>): AsyncGenerator<ParsedStreamEvent> => {
		const attempt = attempts[call++];
		return (async function* () {
			for (const text of attempt?.chunks ?? []) yield { text } as ParsedStreamEvent;
			if (attempt?.fail) throw attempt.fail;
		})();
	};
}

/** Drive the real failover loop the way `startChat` does. */
async function run(
	attempts: ScriptedAttempt[],
	options: { hasAlternative?: boolean; replayBudget?: number; abortAfterText?: boolean } = {},
) {
	const { first, recorder } = scriptResponses(attempts, options);
	const logs: string[] = [];
	const seen: string[] = [];
	const startedWith: string[] = [];
	let aborted = false;

	const iterate = async () => {
		for await (const attempt of streamWithFailover(first, {
			parse: scriptedParser(attempts),
			hasEmittedContent: () => seen.length > 0,
			isAborted: () => aborted,
			log: (message) => logs.push(message),
		})) {
			startedWith.push(attempt.credentialId);
			for await (const event of attempt.events) {
				if (typeof event.text === "string") {
					seen.push(event.text);
					if (options.abortAfterText) aborted = true;
				}
			}
		}
	};

	let thrown: unknown;
	try {
		await iterate();
	} catch (error) {
		thrown = error;
	}
	return { recorder, logs, text: seen.join(""), startedWith, thrown };
}

describe("stream failover: suspension detection", () => {
	test("recognises a suspension exception frame", () => {
		expect(isSuspensionStreamError(suspensionError())).toBe(true);
	});

	test("does not treat an unrelated upstream error as a suspension", () => {
		// Marking a credential for a transient failure would disable a healthy account.
		expect(isSuspensionStreamError(new KiroApiError("ThrottlingException", "slow down"))).toBe(
			false,
		);
		expect(isSuspensionStreamError(new Error("socket hang up"))).toBe(false);
	});
});

describe("stream failover: replay on a healthy credential", () => {
	test("replays when the suspension arrives before any content", async () => {
		const result = await run([
			{ credentialId: "cred-a", fail: suspensionError() },
			{ credentialId: "cred-b", chunks: ["recovered"] },
		]);

		expect(result.thrown).toBeUndefined();
		expect(result.text).toBe("recovered");
		expect(result.recorder.replays).toBe(1);
		// The suspended credential is marked durably: a suspension is lifted by contacting
		// support, so later requests must skip it rather than wait out a cooldown.
		expect(result.recorder.marked).toEqual([
			{ credentialId: "cred-a", reason: "account_suspended" },
		]);
	});

	test("reports the replacement credential to the host", async () => {
		// The host records which credential served the turn; leaving it as the suspended one
		// would attribute the usage and any later diagnosis to the wrong account.
		const result = await run([
			{ credentialId: "cred-a", fail: suspensionError() },
			{ credentialId: "cred-b", chunks: ["ok"] },
		]);

		expect(result.startedWith).toEqual(["cred-a", "cred-b"]);
	});

	test("releases the slot for every attempt, including the abandoned one", async () => {
		// `release()` frees a concurrency slot. Leaking one per failover would starve the
		// provider until the process restarted — a slow failure with no obvious cause.
		const result = await run([
			{ credentialId: "cred-a", fail: suspensionError() },
			{ credentialId: "cred-b", chunks: ["ok"] },
		]);

		expect(result.recorder.releases).toEqual(["cred-a", "cred-b"]);
	});

	test("walks through several suspended credentials", async () => {
		const result = await run([
			{ credentialId: "cred-a", fail: suspensionError() },
			{ credentialId: "cred-b", fail: suspensionError() },
			{ credentialId: "cred-c", chunks: ["third time"] },
		]);

		expect(result.text).toBe("third time");
		expect(result.recorder.marked.map((entry) => entry.credentialId)).toEqual(["cred-a", "cred-b"]);
		expect(result.recorder.releases).toEqual(["cred-a", "cred-b", "cred-c"]);
	});
});

describe("stream failover: when a replay would be wrong", () => {
	test("does not replay once content has reached the user", async () => {
		// The host appends deltas as they arrive, so a replay would duplicate the prefix with no
		// way to retract it. The credential is still marked so later requests skip it.
		const result = await run([
			{ credentialId: "cred-a", chunks: ["already visible"], fail: suspensionError() },
			{ credentialId: "cred-b", chunks: ["would duplicate"] },
		]);

		expect(result.thrown).toBeInstanceOf(KiroApiError);
		expect(result.text).toBe("already visible");
		expect(result.recorder.replays).toBe(0);
		expect(result.recorder.marked).toEqual([
			{ credentialId: "cred-a", reason: "account_suspended" },
		]);
		expect(result.recorder.releases).toEqual(["cred-a"]);
	});

	test("does not replay when no healthy credential remains", async () => {
		const result = await run([{ credentialId: "cred-a", fail: suspensionError() }], {
			hasAlternative: false,
		});

		expect(result.thrown).toBeInstanceOf(KiroApiError);
		expect(result.recorder.replays).toBe(0);
		// Marked even so: the account is suspended whether or not a replacement exists.
		expect(result.recorder.marked).toHaveLength(1);
	});

	test("does not replay a cancelled turn", async () => {
		const result = await run(
			[
				{ credentialId: "cred-a", chunks: ["x"], fail: suspensionError() },
				{ credentialId: "cred-b", chunks: ["unwanted"] },
			],
			{ abortAfterText: true },
		);

		expect(result.recorder.replays).toBe(0);
		expect(result.text).toBe("x");
	});

	test("propagates a non-suspension error without marking the credential", async () => {
		// A transient upstream failure must not disable an account.
		const result = await run([
			{ credentialId: "cred-a", fail: new KiroApiError("ThrottlingException", "slow down") },
			{ credentialId: "cred-b", chunks: ["never reached"] },
		]);

		expect(result.thrown).toBeInstanceOf(KiroApiError);
		expect((result.thrown as KiroApiError).code).toBe("ThrottlingException");
		expect(result.recorder.marked).toEqual([]);
		expect(result.recorder.replays).toBe(0);
		// The slot is still freed on the error path.
		expect(result.recorder.releases).toEqual(["cred-a"]);
	});

	test("surfaces replay exhaustion rather than looping", async () => {
		const result = await run(
			[
				{ credentialId: "cred-a", fail: suspensionError() },
				{ credentialId: "cred-b", fail: suspensionError() },
			],
			{ hasAlternative: true, replayBudget: 1 },
		);

		expect((result.thrown as KiroApiError).code).toBe("STREAM_REPLAY_EXHAUSTED");
	});

	test("releases the slot when the response carries no body", async () => {
		const result = await run([{ credentialId: "cred-a", noBody: true }]);

		expect(result.thrown).toBeInstanceOf(Error);
		expect(result.recorder.releases).toEqual(["cred-a"]);
	});
});

describe("stream failover: the ordinary case", () => {
	test("a clean stream neither marks nor replays", async () => {
		const result = await run([{ credentialId: "cred-a", chunks: ["hello ", "world"] }]);

		expect(result.text).toBe("hello world");
		expect(result.recorder.marked).toEqual([]);
		expect(result.recorder.replays).toBe(0);
		expect(result.recorder.releases).toEqual(["cred-a"]);
		expect(result.logs).toEqual([]);
	});
});
