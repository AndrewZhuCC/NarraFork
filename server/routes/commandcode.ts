/**
 * Command Code 的只读接口：登录状态与额度。
 *
 * 凭据来自 CLI 的 `~/.commandcode/auth.json`，NarraFork 不代为登录 —— 未登录时返回
 * `loggedIn: false` 与可读原因，前端据此提示用户去终端执行 `cmd`。
 */

import { Hono } from "hono";
import { resolveCommandCodeToken } from "../lib/commandcode-manager";
import { invalidateCommandCodeModelsCache, listCommandCodeModels } from "../lib/commandcode-models";
import { CommandCodeUsageError, fetchCommandCodeUsage } from "../lib/commandcode-usage";
import { logger } from "../lib/logger";
import { settings } from "../lib/settings";

export const commandCodeRoutes = new Hono();

/** GET /api/commandcode/status — 凭据是否可用。 */
commandCodeRoutes.get("/status", (c) => {
	const auth = resolveCommandCodeToken(settings.commandCode?.apiKey);
	return c.json({
		loggedIn: !!auth.credential,
		userName: auth.credential?.userName,
		reason: auth.reason,
	});
});

/** GET /api/commandcode/usage — 额度；同时回报登录状态，让前端一次请求就能渲染。 */
commandCodeRoutes.get("/usage", async (c) => {
	const auth = resolveCommandCodeToken(settings.commandCode?.apiKey);
	if (!auth.credential) {
		return c.json({ loggedIn: false, reason: auth.reason });
	}
	try {
		return c.json({ loggedIn: true, ...(await fetchCommandCodeUsage(auth.credential.token)) });
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		const upstreamStatus = error instanceof CommandCodeUsageError ? error.status : 0;
		logger.error("Failed to fetch Command Code usage", { error: message });
		// 上游 401 说明令牌失效，需要用户重新登录；其余按网关错误对待。
		if (upstreamStatus === 401) {
			return c.json({ loggedIn: false, reason: message }, 401);
		}
		return c.json({ loggedIn: true, error: message }, 502);
	}
});

/**
 * POST /api/commandcode/models/refresh — 丢弃模型缓存并重新拉取。
 *
 * 上游随时增删模型，而列表默认缓存 10 分钟；设置页的「刷新模型」按钮走这里。
 * `listCommandCodeModels` 自身吞掉上游错误并返回缓存/空列表，所以这里不会抛。
 */
commandCodeRoutes.post("/models/refresh", async (c) => {
	invalidateCommandCodeModelsCache();
	return c.json({ models: await listCommandCodeModels(settings.commandCode?.apiKey) });
});
