/**
 * Kiro error classifiers.
 *
 * The suspension classifier is the one most at risk of silent regression: the
 * upstream verdict arrives as PROSE, not a machine-readable reason, so a
 * rewording upstream (or a too-narrow pattern here) makes a suspended credential
 * look like an ordinary failure. That is exactly how a suspension used to escape
 * failover — the account was never marked, so every retry reselected it.
 *
 * The negative cases matter just as much as the positive ones: durably disabling
 * a credential is a destructive verdict, so a plain authorization failure must
 * NOT be read as a suspension.
 */

import { describe, expect, test } from "bun:test";
import {
	isAccountSuspended,
	isContextLengthExceeded,
	isMonthlyRequestLimit,
	isSuspendedExceptionType,
	isSuspiciousThrottle,
} from "../quota-check";

/** The body observed in the wild, verbatim apart from the redacted user id. */
const REAL_SUSPENSION_BODY = JSON.stringify({
	__type: "com.amazon.aws.codewhisperer#AccessDeniedException",
	message:
		"Your User ID (e498f488-e0e1-70b4-9543-44c437a0089f) temporarily is suspended. We've locked your account as a security precaution. To restore access, please contact our support team to verify your identity: https://app.kiro.dev/account/usage?support_form",
});

describe("isAccountSuspended", () => {
	test("recognizes the real AccessDeniedException suspension body", () => {
		expect(isAccountSuspended(REAL_SUSPENSION_BODY)).toBe(true);
	});

	test("still recognizes the legacy TEMPORARILY_SUSPENDED sentinel", () => {
		expect(isAccountSuspended('{"reason":"TEMPORARILY_SUSPENDED"}')).toBe(true);
		expect(isAccountSuspended('{"error":{"reason":"TEMPORARILY_SUSPENDED"}}')).toBe(true);
		expect(isAccountSuspended("TEMPORARILY_SUSPENDED")).toBe(true);
	});

	test("tolerates the other word order and the bare phrase", () => {
		for (const message of [
			"Your account is temporarily suspended.",
			"This user is temporarily suspended",
			"Account temporarily suspended for review",
		]) {
			expect(
				isAccountSuspended(
					JSON.stringify({ __type: "com.amazon.aws.codewhisperer#AccessDeniedException", message }),
				),
			).toBe(true);
		}
	});

	test("accepts a flattened envelope that keeps only the message", () => {
		// Some relays drop __type and forward just the prose.
		expect(
			isAccountSuspended(
				JSON.stringify({ message: "We've locked your account as a security precaution." }),
			),
		).toBe(true);
	});

	test("does NOT treat a plain authorization failure as a suspension", () => {
		// Durably disabling a credential is destructive; an ordinary AccessDenied
		// (wrong profileArn, revoked scope) must stay an ordinary failure.
		expect(
			isAccountSuspended(
				JSON.stringify({
					__type: "com.amazon.aws.codewhisperer#AccessDeniedException",
					message: "User is not authorized to perform this operation",
				}),
			),
		).toBe(false);
	});

	test("does not fire on unrelated bodies", () => {
		expect(isAccountSuspended("")).toBe(false);
		expect(isAccountSuspended("not json at all")).toBe(false);
		expect(isAccountSuspended('{"__type":"ValidationException"}')).toBe(false);
	});

	test("keeps suspension and suspicious-throttle as distinct verdicts", () => {
		const throttleBody = JSON.stringify({
			__type: "ThrottlingException",
			message: "Suspicious activity detected",
		});
		expect(isSuspiciousThrottle(throttleBody)).toBe(true);
		expect(isAccountSuspended(throttleBody)).toBe(false);
		expect(isSuspiciousThrottle(REAL_SUSPENSION_BODY)).toBe(false);
	});

	test("does not collide with the quota or context classifiers", () => {
		expect(isMonthlyRequestLimit(REAL_SUSPENSION_BODY)).toBe(false);
		expect(isContextLengthExceeded(REAL_SUSPENSION_BODY)).toBe(false);
		expect(isAccountSuspended('{"reason":"MONTHLY_REQUEST_COUNT"}')).toBe(false);
	});
});

describe("isSuspendedExceptionType", () => {
	test("matches an exception frame carrying the suspension prose", () => {
		// Event-stream shape: type in the frame header, prose in the payload.
		expect(
			isSuspendedExceptionType(
				"AccessDeniedException",
				JSON.stringify({ message: "Your User ID (x) temporarily is suspended." }),
			),
		).toBe(true);
	});

	test("matches the fully qualified AWS type name", () => {
		expect(
			isSuspendedExceptionType(
				"com.amazon.aws.codewhisperer#AccessDeniedException",
				REAL_SUSPENSION_BODY,
			),
		).toBe(true);
	});

	test("requires BOTH the exception type and the suspension wording", () => {
		// Right type, ordinary message.
		expect(
			isSuspendedExceptionType(
				"AccessDeniedException",
				JSON.stringify({ message: "not authorized" }),
			),
		).toBe(false);
		// Suspension wording under a type that cannot carry that verdict.
		expect(isSuspendedExceptionType("ValidationException", REAL_SUSPENSION_BODY)).toBe(false);
	});

	test("returns false without a type or body", () => {
		expect(isSuspendedExceptionType(undefined, REAL_SUSPENSION_BODY)).toBe(false);
		expect(isSuspendedExceptionType("AccessDeniedException", undefined)).toBe(false);
		expect(isSuspendedExceptionType(undefined, undefined)).toBe(false);
	});
});
