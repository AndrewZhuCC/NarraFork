/**
 * 任务感知思维模式路由(服务端聚合入口)。
 *
 * - routerLoopExtension:AgentLoopExtension 实例(onSystemPrompt / onRequest)。
 * - registerRouterTools():幂等注册 router_status / router_mode /
 *   router_subagent 三个自优化工具。
 * - 转发 core.ts 的纯函数导出,便于宿主/测试直接引用。
 */
import { routerLoopExtension } from "./extension";
import { registerRouterTools } from "./tools";

export {
	bandFor,
	bandOf,
	clamp01,
	classifyTask,
	coreFor,
	GUIDE_BASE,
	GUIDE_BOOST,
	GUIDE_COMMIT,
	GUIDE_DEEP,
	guideFor,
	isChatTask,
	isComplexTask,
	isFlashModel,
	MODE_MIXED,
	MODE_REACT,
	MODE_SPEC,
	MODE_WEAK,
	messageText,
	parseMode,
	personaFor,
	testinessFor,
} from "./core";

export { getRouterModeFor, getRouterSessionState, PERSONA_MARKER } from "./extension";
export { registerRouterTools, routerLoopExtension };
