/** Kiro API error classifiers shared by chat, MCP, and usage calls. */

function parseJsonObject(body: string): Record<string, unknown> | null {
	try {
		const parsed = JSON.parse(body);
		return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : null;
	} catch {
		return null;
	}
}

function nestedReason(json: Record<string, unknown> | null): string | undefined {
	const reason = json?.reason;
	if (typeof reason === "string") return reason;
	const error = json?.error;
	if (error && typeof error === "object") {
		const nested = (error as Record<string, unknown>).reason;
		if (typeof nested === "string") return nested;
	}
	return undefined;
}

/** 判断响应体是否表示月度/超额请求配额耗尽。 */
export function isMonthlyRequestLimit(body: string): boolean {
	if (body.includes("MONTHLY_REQUEST_COUNT")) return true;
	if (body.includes("OVERAGE_REQUEST_LIMIT_EXCEEDED")) return true;
	const json = parseJsonObject(body);
	const reason = nestedReason(json);
	if (reason === "MONTHLY_REQUEST_COUNT") return true;
	if (reason === "OVERAGE_REQUEST_LIMIT_EXCEEDED") return true;
	const type = json?.__type;
	return (
		type != null &&
		String(type).includes("ServiceQuotaExceededException") &&
		(reason === "MONTHLY_REQUEST_COUNT" || reason === "OVERAGE_REQUEST_LIMIT_EXCEEDED")
	);
}

/** 判断响应体是否表示上下文长度超限。 */
export function isContextLengthExceeded(body: string): boolean {
	if (body.includes("CONTENT_LENGTH_EXCEEDS_THRESHOLD")) return true;
	const reason = nestedReason(parseJsonObject(body));
	return reason === "CONTENT_LENGTH_EXCEEDS_THRESHOLD";
}

/**
 * Wording used by the account-suspension response, which arrives as prose rather
 * than a machine-readable reason.
 *
 * The observed body is:
 *   "Your User ID (...) temporarily is suspended. We've locked your account as a
 *    security precaution. To restore access, please contact our support team..."
 *
 * Note the word order — `temporarily is suspended`, not `is temporarily
 * suspended`. Both orders (and the bare phrase) are accepted because the message
 * is upstream prose that can be reworded, and matching only one spelling is how
 * this went undetected in the first place.
 */
const SUSPENDED_PROSE_RE =
	/temporarily\s+is\s+suspended|is\s+temporarily\s+suspended|temporarily\s+suspended|locked\s+your\s+account/i;

/** Exception types that can carry a suspension verdict. */
const SUSPENDED_EXCEPTION_TYPE_RE = /AccessDeniedException|UnauthorizedException/i;

/**
 * Whether an event-stream exception frame reports an account suspension.
 *
 * Kiro speaks AWS event-stream: a suspension does NOT necessarily arrive as an
 * HTTP status. It can also be an `exception` frame inside a 200 response body,
 * where the type lives in the frame header (`:exception-type`) and the prose in
 * the payload. This is the classifier for that shape; `isAccountSuspended` is the
 * body-only classifier used on HTTP error paths.
 *
 * Both an exception type AND suspension wording are required. A bare
 * `AccessDeniedException` is an ordinary authorization failure (a profileArn the
 * credential may not use, a revoked scope) and must NOT durably disable the
 * credential.
 */
export function isSuspendedExceptionType(exceptionType?: string, body?: string): boolean {
	const typeMatches = exceptionType != null && SUSPENDED_EXCEPTION_TYPE_RE.test(exceptionType);
	if (!typeMatches) return false;
	return body != null && isAccountSuspended(body);
}

/** 判断响应体是否表示账号被临时/永久封禁。 */
export function isAccountSuspended(body: string): boolean {
	if (body.includes("TEMPORARILY_SUSPENDED")) return true;
	const json = parseJsonObject(body);
	if (nestedReason(json) === "TEMPORARILY_SUSPENDED") return true;
	// Prose form: only trust it alongside an exception type that can legitimately
	// carry a suspension, so an unrelated payload quoting the words cannot disable
	// a healthy credential.
	if (!SUSPENDED_PROSE_RE.test(body)) return false;
	const type = json?.__type;
	if (type != null && SUSPENDED_EXCEPTION_TYPE_RE.test(String(type))) return true;
	// Some relays flatten the envelope and keep only the message. The wording above
	// is specific enough (it names the account lock) to stand on its own there.
	return json != null && typeof json.message === "string" && SUSPENDED_PROSE_RE.test(json.message);
}

/**
 * Detect the "suspicious activity" throttling response.
 * Kiro may return this as either HTTP 400 or 429, and retrying immediately
 * tends to extend the throttle, so callers should cool the credential down.
 */
export function isSuspiciousThrottle(body: string): boolean {
	const lower = body.toLowerCase();
	return lower.includes("throttlingexception") && lower.includes("suspicious activity");
}
