/**
 * Command Code 额度查询。
 *
 * 端点与响应结构均来自实测（在 Studio 生成 API key 后请求真实接口）：
 * - GET /alpha/billing/credits → `{ credits: {...}, windowLimits: {...}, sandboxAccess }`
 *   其中 `credits.monthlyCredits` 是月度余额，`windowLimits` 是 5 小时 / 周滚动窗口的用量与上限。
 * - GET /alpha/usage/summary   → 计费周期内的累计用量（请求数、token、成功率等）。
 * 两者都用 CLI 登录后的凭据鉴权（`Authorization: Bearer <auth.json 的 apiKey>`）。
 */

const API_BASE = "https://api.commandcode.ai";
const REQUEST_TIMEOUT_MS = 15_000;
/** 额度接口不该有巨大响应；超限直接放弃，避免把未知体积的响应读进内存。 */
const MAX_BODY_CHARS = 256 * 1024;

export interface CommandCodeCredits {
	/** 月度额度余额。 */
	monthlyCredits: number;
	purchasedCredits: number;
	freeCredits: number;
	creditThreshold: number;
	belowThreshold: boolean;
}

export interface CommandCodeWindow {
	used: number;
	cap: number;
	exceeded: boolean;
	/** 窗口重置时间（毫秒时间戳）。 */
	resetAt?: number;
}

export interface CommandCodeWindowLimits {
	limited: boolean;
	fiveHour?: CommandCodeWindow;
	weekly?: CommandCodeWindow;
}

export interface CommandCodeUsageSummary {
	totalCount: number;
	totalTokens: number;
	totalTokensIn: number;
	totalTokensOut: number;
	successRate: number;
	completedCount: number;
	failedCount: number;
	/** 计费周期内已消耗的月度额度；与 credits.monthlyCredits（余额）相加即总配额。 */
	totalMonthlyCredits: number;
	/** 计费周期起始（ISO 时间字符串）。 */
	periodStart?: string;
}

export interface CommandCodeUsage {
	credits: CommandCodeCredits;
	windowLimits?: CommandCodeWindowLimits;
	sandboxAccess?: boolean;
	summary?: CommandCodeUsageSummary;
}

export class CommandCodeUsageError extends Error {
	constructor(
		readonly status: number,
		message: string,
	) {
		super(message);
		this.name = "CommandCodeUsageError";
	}
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function num(value: unknown): number {
	return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function optionalNum(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function optionalStr(value: unknown): string | undefined {
	return typeof value === "string" ? value : undefined;
}

function bool(value: unknown): boolean {
	return value === true;
}

/** 读取响应体并限制大小；超限或非法 JSON 都按额度接口异常处理。 */
async function readJsonLimited(response: Response): Promise<unknown> {
	const text = await response.text();
	if (text.length > MAX_BODY_CHARS) {
		throw new CommandCodeUsageError(response.status, "额度接口响应过大，已放弃解析");
	}
	try {
		return JSON.parse(text) as unknown;
	} catch {
		throw new CommandCodeUsageError(response.status, "额度接口返回的不是合法 JSON");
	}
}

async function getJson(path: string, token: string): Promise<unknown> {
	const response = await fetch(`${API_BASE}${path}`, {
		headers: { Authorization: `Bearer ${token}`, Accept: "application/json" },
		signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
	});
	if (!response.ok) {
		throw new CommandCodeUsageError(response.status, `额度接口返回 ${response.status}`);
	}
	return readJsonLimited(response);
}

/** 解析 credits 响应；字段缺失一律降级为 0/false，避免上游改动让整个额度卡报错。 */
export function parseCommandCodeCredits(value: unknown): {
	credits: CommandCodeCredits;
	windowLimits?: CommandCodeWindowLimits;
	sandboxAccess?: boolean;
} {
	const root = isRecord(value) ? value : {};
	const raw = isRecord(root.credits) ? root.credits : {};
	const credits: CommandCodeCredits = {
		monthlyCredits: num(raw.monthlyCredits),
		purchasedCredits: num(raw.purchasedCredits),
		freeCredits: num(raw.freeCredits),
		creditThreshold: num(raw.creditThreshold),
		belowThreshold: bool(raw.belowThreshold),
	};

	let windowLimits: CommandCodeWindowLimits | undefined;
	const rawLimits = root.windowLimits;
	if (isRecord(rawLimits)) {
		windowLimits = {
			limited: bool(rawLimits.limited),
			fiveHour: parseWindow(rawLimits.fiveHour),
			weekly: parseWindow(rawLimits.weekly),
		};
	}

	return { credits, windowLimits, sandboxAccess: optionalBool(root.sandboxAccess) };
}

function parseWindow(value: unknown): CommandCodeWindow | undefined {
	if (!isRecord(value)) return undefined;
	return {
		used: num(value.used),
		cap: num(value.cap),
		exceeded: bool(value.exceeded),
		resetAt: optionalNum(value.resetAt),
	};
}

function optionalBool(value: unknown): boolean | undefined {
	return typeof value === "boolean" ? value : undefined;
}

/** 解析 usage summary；缺失字段按 0 处理。 */
export function parseCommandCodeSummary(value: unknown): CommandCodeUsageSummary | undefined {
	if (!isRecord(value)) return undefined;
	return {
		totalCount: num(value.totalCount),
		totalTokens: num(value.totalTokens),
		totalTokensIn: num(value.totalTokensIn),
		totalTokensOut: num(value.totalTokensOut),
		successRate: num(value.successRate),
		completedCount: num(value.completedCount),
		failedCount: num(value.failedCount),
		totalMonthlyCredits: num(value.totalMonthlyCredits),
		periodStart: optionalStr(value.periodStart),
	};
}

/** 查询额度：credits 必取；usage summary 失败不影响整体结果。 */
export async function fetchCommandCodeUsage(token: string): Promise<CommandCodeUsage> {
	const { credits, windowLimits, sandboxAccess } = parseCommandCodeCredits(
		await getJson("/alpha/billing/credits", token),
	);
	let summary: CommandCodeUsageSummary | undefined;
	try {
		summary = parseCommandCodeSummary(await getJson("/alpha/usage/summary", token));
	} catch {
		// 用量摘要失败不阻塞额度展示。
	}
	return { credits, windowLimits, sandboxAccess, summary };
}
