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

/** 判断响应体是否表示账号被临时/永久封禁。 */
export function isAccountSuspended(body: string): boolean {
	if (body.includes("TEMPORARILY_SUSPENDED")) return true;
	const reason = nestedReason(parseJsonObject(body));
	return reason === "TEMPORARILY_SUSPENDED";
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
