/**
 * Command Code 模型列表（带缓存）。
 *
 * Provider API 的 `GET /provider/v1/models` 是运行时数据（上游随时增删模型），所以不像 codex
 * 那样硬编码，而是拉取后缓存；失败时退回到上次结果或空列表，绝不让 settings 响应因此报错。
 */

import { resolveCommandCodeToken } from "./commandcode-manager";
import { COMMAND_CODE_PROVIDER_BASE, parseCommandCodeModels } from "./commandcode-protocol";

const CACHE_TTL_MS = 10 * 60 * 1000;
const REQUEST_TIMEOUT_MS = 10_000;

let cache: { models: string[]; fetchedAt: number; token: string } | undefined;

/** 列出可用模型 id；未配置凭据或请求失败时返回缓存（可能为空）。 */
export async function listCommandCodeModels(apiKey?: string): Promise<string[]> {
	const auth = resolveCommandCodeToken(apiKey);
	if (!auth.credential) return [];
	const token = auth.credential.token;
	// 换过凭据就必须重拉：上一个 key 的可见模型集合可能完全不同。
	if (cache && cache.token === token && Date.now() - cache.fetchedAt < CACHE_TTL_MS) {
		return cache.models;
	}
	try {
		const response = await fetch(`${COMMAND_CODE_PROVIDER_BASE}/models`, {
			headers: { Authorization: `Bearer ${token}`, Accept: "application/json" },
			signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
		});
		if (!response.ok) return cache?.token === token ? cache.models : [];
		const models = parseCommandCodeModels(await response.json()).map((model) => model.id);
		cache = { models, fetchedAt: Date.now(), token };
		return models;
	} catch {
		return cache?.token === token ? cache.models : [];
	}
}

/** 丢弃缓存，让下一次 `listCommandCodeModels` 重新向上游拉取（设置页的「刷新模型」）。 */
export function invalidateCommandCodeModelsCache(): void {
	cache = undefined;
}
