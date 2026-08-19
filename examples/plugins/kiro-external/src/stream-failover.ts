/**
 * Mid-stream credential failover for chat and generate.
 *
 * ## The failure this exists to handle
 *
 * Kiro answers over AWS event-stream, so an account-level verdict can arrive as an `exception`
 * **frame inside an HTTP 200 body**. By then `sendChatRequest` has already returned and recorded
 * the credential as successful, so its own retry loop cannot react: the suspended credential is
 * never marked, never excluded, and every later request — each compact retry included —
 * reselects the same dead account and surfaces raw AWS JSON to the user.
 *
 * The HTTP client hands back two hooks for exactly this seam, and the plugin was calling
 * neither: `reportStreamFailure` (mark the credential, and report whether an alternative exists)
 * and `retryWithNextCredential` (replay inheriting this call's exclusion set and replay budget).
 *
 * ## Why the replay is gated on having emitted nothing
 *
 * Replaying a request that already streamed text would emit that text twice — the host appends
 * deltas as they arrive, so there is no way to retract the prefix. When output already exists the
 * credential is still marked (so later requests skip it) and the error propagates instead. This
 * mirrors `kiroChatRaw`, which owns the same decision for the built-in path.
 *
 * A suspension is durable: it is lifted by contacting support, not by waiting, so the mark is not
 * a cooldown. Every other failure keeps the credential usable, because a transient upstream error
 * must not disable an account.
 */

import { isSuspendedExceptionType } from "@server/lib/kiro/quota-check";
import { KiroApiError } from "@server/lib/kiro/types";
import type { ParsedStreamEvent } from "@shared/codewhisperer/types";

/** The subset of `sendChatRequest`'s result this needs. Narrowed so tests need no HTTP. */
export interface FailoverChatResponse {
	response: { body: ReadableStream<Uint8Array> | null };
	credentialId: string;
	release: () => void;
	reportStreamFailure: (reason?: "account_suspended") => boolean;
	retryWithNextCredential: () => Promise<FailoverChatResponse>;
}

export interface StreamAttempt {
	/** Credential serving this attempt, for the `request_started` event. */
	credentialId: string;
	/** Parsed events for this attempt. Throws on an exception frame. */
	events: AsyncGenerator<ParsedStreamEvent>;
	/**
	 * Frees this attempt's concurrency slot.
	 *
	 * Exposed so a cancellation arriving mid-stream can release immediately instead of waiting
	 * for the loop to unwind. This generator also releases it in its own `finally`, and calling
	 * it twice is harmless — the client's `releaseHeld` is guarded by a `held` flag — but leaving
	 * it unreleased on an abandoned turn would leak a slot and slowly starve the provider.
	 */
	release: () => void;
}

export interface FailoverOptions {
	/** Parse a response body into events. Injected so the caller owns parse options. */
	parse: (body: ReadableStream<Uint8Array>) => AsyncGenerator<ParsedStreamEvent>;
	/** True once anything the user can see has been emitted, making a replay unsafe. */
	hasEmittedContent: () => boolean;
	/** Aborted turns must not be replayed. */
	isAborted: () => boolean;
	/** Diagnostics. Never receives credential material. */
	log: (message: string, detail?: Record<string, unknown>) => void;
}

/** Whether a stream error is an account suspension delivered as an exception frame. */
export function isSuspensionStreamError(error: unknown): error is KiroApiError {
	if (!(error instanceof KiroApiError)) return false;
	return isSuspendedExceptionType(error.code, error.message);
}

/**
 * Hard iteration ceiling as defense-in-depth.
 *
 * The normal termination path is the client's replay budget (`retryWithNextCredential` throws
 * `STREAM_REPLAY_EXHAUSTED` once spent). This constant is a safety net in case a bug in the
 * budget tracking or the suspension detection causes an infinite loop. 20 iterations far
 * exceeds any reasonable credential pool size while still terminating deterministically.
 */
export const MAX_FAILOVER_ITERATIONS = 20;

/**
 * Yield attempts until one completes or no replay remains.
 *
 * Each yielded attempt is a fresh credential and a fresh event stream; the caller consumes
 * `events` and must let a thrown error propagate out of the loop body. When that error is a
 * suspension and nothing has been emitted, the next iteration yields a replay on a healthy
 * credential. Otherwise the error is rethrown.
 *
 * The caller re-emits `request_started` per attempt, which the host treats as an update to the
 * serving credential rather than a second turn.
 */
export async function* streamWithFailover(
	initial: FailoverChatResponse,
	options: FailoverOptions,
): AsyncGenerator<StreamAttempt> {
	let chat = initial;
	let iterations = 0;

	// Bounded by the client's own replay budget: `retryWithNextCredential` decrements it and
	// throws `STREAM_REPLAY_EXHAUSTED` once spent, so this cannot loop forever.
	// Additionally bounded by MAX_FAILOVER_ITERATIONS as a hard safety net.
	while (true) {
		iterations += 1;
		if (iterations > MAX_FAILOVER_ITERATIONS) {
			throw new Error(
				`streamWithFailover exceeded ${MAX_FAILOVER_ITERATIONS} iterations — ` +
					"likely a bug in replay budget tracking or suspension detection",
			);
		}
		const { response, credentialId, release } = chat;
		if (!response.body) {
			release();
			throw new Error("upstream returned no response body");
		}

		const body = response.body;
		let suspension: KiroApiError | undefined;

		try {
			yield {
				credentialId,
				release,
				events: (async function* () {
					try {
						yield* options.parse(body);
					} catch (error) {
						// Held rather than rethrown here so the caller's own loop ends cleanly and
						// the decision below is made once, in one place.
						if (!isSuspensionStreamError(error)) throw error;
						suspension = error;
					}
				})(),
			};
		} finally {
			release();
		}

		if (!suspension) return;

		// Durable mark, and it happens even when a replay is not possible: a later request must
		// still skip this account.
		const hasAlternative = chat.reportStreamFailure("account_suspended");
		if (options.hasEmittedContent() || !hasAlternative || options.isAborted()) {
			options.log("credential suspended mid-stream; not replaying", {
				emittedContent: options.hasEmittedContent(),
				hasAlternative,
				aborted: options.isAborted(),
			});
			throw suspension;
		}

		options.log("credential suspended mid-stream; replaying on another credential");
		// Through the client, so the replay inherits this call's excluded credentials and its
		// remaining budget.
		chat = await chat.retryWithNextCredential();
	}
}
