/**
 * Command Code Provider API 的接入信息与端点选择。
 *
 * 实测结论（`https://api.commandcode.ai/provider/v1`）：
 * - `GET /models` 返回 OpenAI 风格列表，每个模型带 `supported_endpoints`（权威依据）。
 * - `POST /chat/completions`、`POST /responses` 可用，覆盖 82 个模型（gpt / deepseek / qwen 等）。
 * - `POST /messages` 是 claude 系唯一支持的端点：把它们发到另外两个端点会得到 400
 *   "must be called via /provider/v1/messages"。该端点目前对实测账户恒返回 403（CLI 与 Studio
 *   两把 key、Bearer 与 x-api-key 两种认证都如此，而同一 key 的其它端点均 200），属上游问题。
 */

export const COMMAND_CODE_API_BASE = "https://api.commandcode.ai";
/** Provider API 根：各推理端点挂在其下（`/messages`、`/chat/completions`、`/responses`、`/models`）。 */
export const COMMAND_CODE_PROVIDER_BASE = `${COMMAND_CODE_API_BASE}/provider/v1`;
/**
 * 平台 provider 的前缀。
 *
 * 定义在这里（而不是 provider 装配模块）是因为 settings 加载期的迁移也要用它，
 * 而那条路径不能反过来依赖 agent 层（会形成初始化环）。
 */
export const COMMAND_CODE_PREFIX = "CommandCode";

export type CommandCodeEndpoint = "messages" | "completions" | "responses";

export interface CommandCodeModelInfo {
	id: string;
	name?: string;
	contextLength?: number;
	endpoints: string[];
}

/**
 * 模型该走哪个端点。
 *
 * 默认规则：`claude*` 只支持 `/messages`，其余模型走 OpenAI 协议并默认用 `/chat/completions`。
 * `overrides` 里针对单个模型的显式配置优先，用户可在设置页逐模型切换。
 */
export function resolveCommandCodeEndpoint(
	modelId: string,
	overrides?: Record<string, CommandCodeEndpoint>,
): CommandCodeEndpoint {
	const override = overrides?.[modelId];
	if (override) return override;
	return modelId.trim().toLowerCase().startsWith("claude") ? "messages" : "completions";
}

/** 解析 `/provider/v1/models` 的响应；字段缺失时保守地返回空列表。 */
export function parseCommandCodeModels(value: unknown): CommandCodeModelInfo[] {
	if (typeof value !== "object" || value === null) return [];
	const data = (value as { data?: unknown }).data;
	if (!Array.isArray(data)) return [];
	return data.flatMap((item) => {
		if (typeof item !== "object" || item === null) return [];
		const raw = item as Record<string, unknown>;
		if (typeof raw.id !== "string") return [];
		return [
			{
				id: raw.id,
				name: typeof raw.name === "string" ? raw.name : undefined,
				contextLength: typeof raw.context_length === "number" ? raw.context_length : undefined,
				endpoints: Array.isArray(raw.supported_endpoints)
					? raw.supported_endpoints.filter((e): e is string => typeof e === "string")
					: [],
			},
		];
	});
}
