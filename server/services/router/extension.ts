/**
 * 任务感知思维模式路由扩展（NarraFork 内置 router）。
 *
 * 移植自 dsh-routing-suite 的 mode-boost v0.1.0(实测驱动,单一事实源见 ./core.ts):
 * - **白名单门控**:默认不启用任何模型(agent.routerModels 为空);匹配的模型
 *   才路由——不注入 persona、不收窄工具、不注入引导(persona 文本实测面向
 *   DeepSeek V4 Flash,建议只把该家族加入白名单)。
 * - **首轮锚定**:Flash 会话首轮只暴露分类匹配的核心工具,首次工具调用后
 *   恢复用户原配置(实测:可调用的 schema 面决定行为,不是工具名文本)。
 * - **近距离引导**:weak(内路由)带每条真实用户消息后注入一条固定引导
 *   (缓存中性;第 3 轮起 boost 重分类;简单任务快速收敛 / 复杂任务有向深度)。
 * - **寒暄让位**:问候/无任务消息完全不干预(实测:deep persona 套在聊天上
 *   → 338 块思维链)。
 *
 * 语义映射(pi-deepseek-route 事件 → NarraFork 两阶段钩子):
 *   before_agent_start → onSystemPrompt(persona + 首轮锚定)
 *   context            → onRequest(近距离引导)
 *   tool_call / agent_settled → onRequest 的 turnIndex>=1 分支(租约结束)
 *
 * 会话状态为进程内 Map(key = narratorId),不做持久化——重启丢失可接受。
 */

import { matchesReasoningEffortBlocklist } from "../../../shared/reasoning-effort-support";
import type {
	AgentLoopExtension,
	AgentLoopExtensionContext,
	AgentLoopPersonaResult,
	AgentLoopRequestResult,
} from "../../lib/agent/types";
import { logger } from "../../lib/logger";
import { getRouterModels } from "../../lib/settings";
import {
	bandFor,
	classifyTask,
	coreFor,
	guideFor,
	isChatTask,
	messageText,
	personaFor,
} from "./core";

/** Marker used to idempotently locate our persona inside the system prompt. */
export const PERSONA_MARKER = "# Reasoning-mode persona (router)";

/**
 * Whether router routing is enabled for this model id.
 *
 * Whitelist gate: routing is disabled by default (empty `agent.routerModels`)
 * and only activates for models whose id matches one of the configured
 * patterns (case-insensitive substring or `/regex/flags`). Non-whitelisted
 * models get zero intervention.
 */
export function isRouterModel(model: string | undefined | null): boolean {
	if (!model) return false;
	return matchesReasoningEffortBlocklist(model, getRouterModels());
}

interface SessionState {
	/** First REAL user message text of the session (mode anchor, resume-safe). */
	firstUserText: string | null;
	/** Explicit override from router_mode (null = task classification). */
	override: number | "weak" | null;
	/** Whether the first-turn tool narrowing already happened (session-scoped, once). */
	firstTurnNarrowed: boolean;
	/** Active narrowing lease; ends at first tool call (turnIndex>=1 in this loop). */
	lease: { core: string[]; full: string[] } | null;
	/**
	 * Keys of user messages we already appended guidance for (dedupe).
	 * NarraFork history messages may lack a `timestamp` field, so keys fall back
	 * to the content text (pi 版用 timestamp;此处为兼容 history 形状而退化)。
	 */
	guidedKeys: Set<string>;
}

function newState(): SessionState {
	return {
		firstUserText: null,
		override: null,
		firstTurnNarrowed: false,
		lease: null,
		guidedKeys: new Set(),
	};
}

/** Session states keyed by narratorId (process-local; lost on restart). */
const states = new Map<string, SessionState>();

/** Accessor for router_* tools: raw per-session state (may create one). */
export function getRouterSessionState(narratorId: string): SessionState {
	let state = states.get(narratorId);
	if (!state) {
		state = newState();
		states.set(narratorId, state);
	}
	return state;
}

/** Accessor for router_* tools: effective mode (override ?? classification). */
export function getRouterModeFor(
	narratorId: string,
	fallback: number | "weak" | null,
): number | "weak" | null {
	const state = states.get(narratorId);
	if (!state) return fallback;
	return (
		state.override ?? (state.firstUserText === null ? fallback : classifyTask(state.firstUserText))
	);
}

/**
 * Intersect core candidates with the user's active tool set, then add shell.
 * (pi 版 resolveCore 同逻辑:bash/pwsh → NarraFork 的 Bash/Shell)
 */
function resolveCore(full: string[], mode: number | "weak"): string[] {
	const available = new Set(full);
	const core = new Set<string>();
	for (const name of coreFor(mode)) {
		if (available.has(name)) core.add(name);
	}
	if (available.has("Bash")) core.add("Bash");
	else if (available.has("Shell")) core.add("Shell");
	// 兜底:core 为空(用户禁用了全部核心工具)时至少保留读写面。
	if (core.size === 0) {
		for (const name of ["Read", "Write", "Edit"]) {
			if (available.has(name)) core.add(name);
		}
	}
	return [...core];
}

/** Router-owned / provider-internal user messages to exclude from round counting. */
function isRouterNoise(text: string): boolean {
	return text.startsWith("Router:") || text.startsWith("__SYSTEM__:");
}

/** Dedupe key for guided messages: timestamp when present, else content text. */
function guidedKey(text: string, timestamp: unknown): string {
	return typeof timestamp === "number" || typeof timestamp === "string"
		? `ts:${timestamp}`
		: `text:${text}`;
}

/** Count real user messages in history (excluding router/system noise). */
function realUserCount(history: unknown[]): number {
	return history.filter((m) => {
		if ((m as { role?: unknown }).role !== "user") return false;
		const text = messageText((m as { content?: unknown }).content).trim();
		return text.length > 0 && !isRouterNoise(text);
	}).length;
}

export const routerLoopExtension: AgentLoopExtension = {
	/**
	 * 每个 agent loop 调用一次(system prompt 注入前):
	 * 缓存会话首条真实消息 → 寒暄让位 → 首轮锚定(设置工具租约)→ 注入 persona。
	 */
	onSystemPrompt(ctx: AgentLoopExtensionContext): AgentLoopPersonaResult | null {
		// 门控:非 Flash 模型零干预。
		if (!isRouterModel(ctx.model)) return null;
		const state = getRouterSessionState(ctx.narratorId);
		const firstText = state.firstUserText ?? ctx.userText.trim();
		// 寒暄让位:不缓存、不锚定、不引导。
		if (isChatTask(firstText)) return null;
		state.firstUserText = firstText;
		const mode = state.override ?? classifyTask(firstText);
		const band = bandFor(mode);
		// 首轮工具面收窄(仅会话一次;租约在首个工具调用 turnIndex>=1 结束)。
		let coreTools: string[] | undefined;
		if (!state.firstTurnNarrowed) {
			state.firstTurnNarrowed = true;
			const full = ctx.tools.map((t) => t.name);
			const core = resolveCore(full, mode);
			coreTools = core;
			state.lease = { core, full };
		}
		const persona = personaFor(mode, ctx.model);
		logger.info("router: routing activated for session", {
			narratorId: ctx.narratorId,
			model: ctx.model,
			mode: typeof mode === "string" ? mode : mode.toFixed(2),
			band,
			override: state.override !== null,
			firstTurnCore: coreTools ?? null,
			persona: persona.slice(0, 60).replace(/\n/g, " "),
		});
		return { systemPromptSuffix: `${PERSONA_MARKER}\n${persona}` };
	},

	/**
	 * 每轮 provider.chat 前调用:
	 * - 工具租约:turnIndex 0 → 只暴露 core;turnIndex>=1(已有工具调用)→
	 *   清除租约放行全量(对应 pi 的 tool_call / agent_settled 恢复)。
	 * - 近距离引导:仅 weak 带;每条未被引导过的真实 user 消息后注入一条引导
	 *   (appendMessages 进 history 本轮生效;NarraFork 的注入会留在 history 里,
	 *   故额外防御性跳过以 Router: 开头的消息,避免重复注入)。
	 */
	onRequest(ctx: AgentLoopExtensionContext): AgentLoopRequestResult | null {
		// 门控:非 Flash 模型零干预。
		if (!isRouterModel(ctx.model)) return null;
		const state = getRouterSessionState(ctx.narratorId);
		const firstText = state.firstUserText ?? ctx.userText.trim();
		// 寒暄让位。
		if (isChatTask(firstText)) return null;

		// ── 工具租约 ──────────────────────────────────────────────────────────
		let allowTools: Set<string> | null = null;
		if (state.lease) {
			if (ctx.turnIndex === 0) {
				allowTools = new Set(state.lease.core);
				logger.info("router: first-turn tool anchoring", {
					narratorId: ctx.narratorId,
					model: ctx.model,
					core: [...allowTools],
				});
			} else {
				// turnIndex>=1 = 已有工具调用 → 租约结束,放行全量。
				state.lease = null;
				logger.info("router: tool lease released (first tool call)", {
					narratorId: ctx.narratorId,
					model: ctx.model,
					turnIndex: ctx.turnIndex,
				});
			}
		}

		// ── 近距离引导(仅 weak 带) ───────────────────────────────────────────
		let appendMessages: unknown[] | undefined;
		const mode =
			state.override ?? (state.firstUserText === null ? null : classifyTask(state.firstUserText));
		if (mode !== null && bandFor(mode) === "weak") {
			const last = ctx.history[ctx.history.length - 1] as
				| { role?: unknown; content?: unknown; timestamp?: unknown }
				| undefined;
			if (last !== undefined && last.role === "user") {
				const text = messageText(last.content).trim();
				const key = guidedKey(text, last.timestamp);
				if (text.length > 0 && !isRouterNoise(text) && !state.guidedKeys.has(key)) {
					const round = realUserCount(ctx.history);
					const guide = guideFor(round, text);
					state.guidedKeys.add(key);
					appendMessages = [{ role: "user", content: [{ type: "text", text: guide }] }];
					logger.info("router: near-field guidance injected", {
						narratorId: ctx.narratorId,
						model: ctx.model,
						round,
						guideType: guide.includes("NEW task") ? "boost-reclassify" : "classify",
						depth: guide.includes("architecture") ? "deep" : "fast",
					});
				}
			}
		}

		if (allowTools === null && appendMessages === undefined) return null;
		return { allowTools, appendMessages };
	},
};
