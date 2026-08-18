/**
 * router 自优化工具:router_status / router_mode / router_subagent。
 *
 * 移植自 pi-deepseek-route index.ts(行为文本与 pi 版对齐):
 * - status:展示会话路由状态(model / 门控 / mode / band / persona / core / override)。
 * - mode:写入会话 override(parseMode 解析;auto → null)。
 * - subagent:在隔离上下文(独立 persona)做一次无工具生成。
 *
 * 注册走 toolRegistry 单例;registerRouterTools() 幂等(重复调用先 unregister)。
 */
import { z } from "zod/v4";
import { agentGenerateWithMeta } from "../../lib/agent";
import { toolRegistry } from "../../lib/agent/tool-registry";
import type { ToolDefinition, ToolResult } from "../../lib/agent/types";
import {
	bandFor,
	clamp01,
	classifyTask,
	MODE_WEAK,
	parseMode,
	personaFor,
	testinessFor,
} from "./core";
import { getRouterModeFor, getRouterSessionState, isRouterModel } from "./extension";

const statusTool: ToolDefinition = {
	name: "router_status",
	description:
		"Show this session's router state: active model, gate status, mode, band, persona, first-turn core tools, and whether an override is active. Only meaningful for whitelisted models.",
	parameters: z.object({}),
	async execute(_args, ctx): Promise<ToolResult> {
		const state = getRouterSessionState(ctx.narratorId);
		const mode = getRouterModeFor(ctx.narratorId, null);
		const model = ctx.model;
		const lines = [
			`model=${model ?? "unknown"}`,
			`router-gate=${model ? (isRouterModel(model) ? "active (whitelisted)" : "disabled (not whitelisted in agent.routerModels)") : "unknown"}`,
			`mode=${mode === null ? "unset" : typeof mode === "string" ? mode : mode.toFixed(2)} (band=${mode === null ? "-" : bandFor(mode)})`,
			`persona=${mode === null ? "-" : personaFor(mode, model).replace(/\n/g, " / ")}`,
			`core=[${state.lease ? state.lease.core.join(", ") : "(full surface restored)"}]`,
			`testiness=${mode === null ? "-" : testinessFor(mode)}`,
			`override=${state.override === null ? "no" : typeof state.override === "string" ? state.override : state.override.toFixed(2)}`,
		];
		return { output: lines.join("\n") };
	},
};

const modeTool: ToolDefinition = {
	name: "router_mode",
	description:
		"Set this session's reasoning mode (whitelisted-router sessions only): spec (plan-first) / weak (internal routing, model decides per task) / mixed (transition, trap) / react (doer). Accepts band names, 0-100, or 0.0-1.0; use auto to return to task classification. The next request applies it. Non-Flash sessions are unaffected.",
	parameters: z.object({
		mode: z
			.string()
			.describe(
				"band name (spec / weak / mixed / react), a 0-100 number, a 0.0-1.0 number, or auto to clear the override",
			),
	}),
	async execute(args, ctx): Promise<ToolResult> {
		if (ctx.model && !isRouterModel(ctx.model)) {
			return {
				output: "router disabled: model not whitelisted in agent.routerModels",
			};
		}
		const raw = typeof args.mode === "string" ? args.mode : String(args.mode ?? "");
		const parsed = parseMode(raw);
		if (parsed === null) {
			return {
				output: `invalid mode "${raw}": use spec/weak/mixed/react, 0-100, 0.0-1.0, or auto`,
				isError: true,
			};
		}
		const state = getRouterSessionState(ctx.narratorId);
		// auto → null(显式清除旧 override)。
		state.override = parsed === "auto" ? null : parsed === "weak" ? MODE_WEAK : clamp01(parsed);
		const current =
			state.override ?? (state.firstUserText === null ? null : classifyTask(state.firstUserText));
		const text = `mode=${current === null ? "unset" : typeof current === "string" ? current : current.toFixed(2)} (band=${current === null ? "-" : bandFor(current)}) — next request applies`;
		return { output: text };
	},
};

const subagentTool: ToolDefinition = {
	name: "router_subagent",
	description:
		"Run one task in a DIFFERENT reasoning mode than this session, in a fresh isolated context (own system prompt). The current session trajectory is untouched. Mode: spec (plan-first) / weak (internal routing) / react (doer) / balanced. Returns the answer text.",
	parameters: z.object({
		mode: z.string().describe("spec / weak / react / balanced (or 0-100)"),
		task: z.string().describe("the task to hand to the mode-isolated subagent"),
		maxTokens: z.number().optional().describe("output cap (default 1024)"),
	}),
	async execute(args, ctx): Promise<ToolResult> {
		if (ctx.model && !isRouterModel(ctx.model)) {
			return {
				output: "router disabled: model not whitelisted in agent.routerModels",
			};
		}
		const rawMode = typeof args.mode === "string" ? args.mode : String(args.mode ?? "");
		const parsed = parseMode(rawMode);
		if (parsed === null || parsed === "auto") {
			return { output: `invalid mode "${rawMode}"`, isError: true };
		}
		const model = ctx.model;
		if (!model) {
			return { output: "no active model", isError: true };
		}
		let maxTokens = Number(args.maxTokens ?? 1024);
		if (!Number.isFinite(maxTokens) || maxTokens <= 0) maxTokens = 1024;
		const persona = personaFor(parsed, model);
		try {
			const result = await agentGenerateWithMeta(String(args.task ?? ""), model, persona, {
				maxOutputTokens: maxTokens,
				signal: ctx.signal,
			});
			const text = (result.text ?? "").trim();
			const head = text.slice(0, 3000);
			return {
				output: `[mode-subagent ${bandFor(parsed)}]\n${head}${text.length > 3000 ? "\n…(truncated)" : ""}`,
				metadata: { mode: bandFor(parsed), length: text.length },
			};
		} catch (err) {
			if (ctx.signal.aborted) {
				return { output: "subagent aborted", isError: true };
			}
			return {
				output: `subagent error: ${err instanceof Error ? err.message : String(err)}`,
				isError: true,
			};
		}
	},
};

const ROUTER_TOOLS: ToolDefinition[] = [statusTool, modeTool, subagentTool];

/** Register the three router_* tools. Idempotent: re-registration replaces. */
export function registerRouterTools(): void {
	for (const tool of ROUTER_TOOLS) {
		if (toolRegistry.get(tool.name)) toolRegistry.unregister(tool.name);
		toolRegistry.register(tool);
	}
}
