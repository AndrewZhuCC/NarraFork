/**
 * router core: 纯函数路由逻辑(零依赖)。
 *
 * 移植自 dsh-mode-boost v0.1.0 `lib/core.js`(实测驱动,单一事实源):
 * 全部文本与阈值均为 2026-08-15 官方 API 实测产物
 * (deepseek-v4-flash, reasoning_effort=max, n=2, A/B 同场对比)。
 *
 * Flash-only 裁剪:
 * - 本扩展只对 Flash 家族模型生效(门控在 extension.ts),故 personaFor 弱带
 *   固定返回 WEAK_FLASH,不引入 WEAK_PRO。
 * - guideFor 无决策闭环后缀(GUIDE_CLOSURE 实测对 Flash 中性,省 token)。
 *
 * 行为带(实测,不可调):
 *   mode 0    → pure spec  — plan-first, collective, read-first tools
 *   mode 0.3  → mixed      — transition band (trap; only explicit opt-in)
 *   mode 1    → pure react — doer, produce-verify-fix, test-suppressed
 *   mode W    → weak       — internal routing (model decides per task)
 */

export const MODE_SPEC = 0;
export const MODE_MIXED = 0.3;
export const MODE_REACT = 1;
export const MODE_WEAK = "weak";

const SPEC_PERSONA = "You are a helpful software engineer assistant.";

const MIXED_PERSONA =
	"You are a helpful software engineer assistant.\n" +
	"Work directly: prefer writing or editing code over describing plans. " +
	"Verify your changes by reading and running them.";

const REACT_PERSONA =
	"You are a hands-on software engineer who delivers working output fast.\n" +
	"Work directly: write or edit code, then verify it by reading and running. " +
	"Keep the loop tight — produce, verify, fix — and do not build test " +
	"harnesses, scaffolding, or ceremony the user did not ask for. " +
	"Finish with a usable deliverable and a short summary.";

/**
 * Weak (internal-routing) persona — Pro 专属最优 (w6c, P24):
 * spec 句 + classify 指令，**无 anchors**（P24 实测:给 Pro 套 recall/收敛锚
 * 反而从 87.5% 掉到 83%——Pro 对复杂注入敏感,简单反而好）。
 */
const WEAK_PRO =
	"You are a helpful software engineer assistant.\n" +
	"Before acting, decide the task type (build or fix) and adopt the matching " +
	"style: build → hands-on production; fix → inspect-and-plan.";

/**
 * Weak (internal-routing) persona — Flash 专属最优 (w7, P11/P23):
 * neutral + classify + recall/anti-runaway anchors + deep-first sentence
 * (P20 deep-persona: converge 100%)。spec 句 persona 对 Flash 反路由(P11)。
 */
const WEAK_FLASH =
	"You are a helpful assistant.\n" +
	"Before acting, decide the task type (build or fix) and adopt the matching " +
	"style: build → hands-on production; fix → inspect-and-plan.\n" +
	"Before acting, briefly review what you have already done in this session and continue from where you left off; do not repeat completed steps. Do not run environment checks (echo, whoami, uname, node --version, date) or exhaustive grep/glob scans.\n" +
	"Think deeply first, then produce.";

// ── guidance texts (near-field, appended after real user messages) ────────

/** Round 1-2 baseline: classify + adopt style. */
export const GUIDE_BASE =
	"\n\nRouter: classify this task (build or fix) now, then adopt the matching style — build: direct production; fix: inspect-first.";

/** Rounds 3+: anti-dilution reclassification (P19 boost / P21 b-boost). */
export const GUIDE_BOOST =
	"\n\nRouter: this is a NEW task, different from the previous ones. Classify it fresh (build or fix) and adopt the matching style — build: direct production; fix: inspect-first. Do not follow the previous task's style.";

/** Fast-convergence tail for simple tasks (P30: 1 step, zero waste). */
export const GUIDE_COMMIT = " Think deeply first, then commit and act.";

/** Directed deep tail for complex tasks (P30: depth without rumination). */
export const GUIDE_DEEP =
	" Think deeply about the architecture, edge cases, and integration points. Do not spend reasoning on the environment or tooling. Produce when your information is complete.";

/** Complexity heuristic: long or architecturally-worded tasks are COMPLEX. */
const COMPLEX_RE =
	/(重构|架构|全面|详细|设计|系统|优化|分析|survey|overview|architecture|refactor|comprehensive|detailed|design|system|optimize|analyze)/i;

export function isComplexTask(text: string): boolean {
	return typeof text === "string" && (text.length > 120 || COMPLEX_RE.test(text));
}

/**
 * Conversational first-message detection: greetings / bare acknowledgements /
 * short messages with no task keywords. On such messages the router stands
 * down entirely (no persona injection, no tool narrowing, no guidance) — the
 * deep engineering persona on a chat message produces long reasoning chains
 * with nothing to route (measured on 创造模式, 2026-08-15: 338 reasoning
 * chunks on a greeting + analysis question).
 */
const CHAT_RE =
	/^(你好|您好|hello|hi|hey|嗨|哈喽|在吗|谢谢|感谢|thanks|thank you|早上好|下午好|晚上好|嗯|好|ok|okay|yes|no|嗯嗯|好的)[!。.!？?~～]*$/i;

export function isChatTask(text: string): boolean {
	if (typeof text !== "string") return true;
	const t = text.trim();
	if (t.length === 0) return true;
	if (CHAT_RE.test(t)) return true;
	if (t.length > 24) return false;
	return !t.match(REACT_RE) && !t.match(SPEC_RE); // short + no task keywords → chat
}

const REACT_RE =
	/(开发|创建|写一个|写|生成|从零|做|做一个|做个|游戏|网页|网站|构建|新项目|搭建|实现|做出|上线|落地|脚本|工具|应用|build|create|develop|generate|implement|write a|write an|build a|make a|new project)/gi;
const SPEC_RE =
	/(修复|修一下|调试|重构|维护|排查|报错|出错|崩溃|优化|审查|review|fix|debug|refactor|maintain|repair|broken|break|为什么|异常|故障|迁移|升级|兼容)/gi;

function countHits(regex: RegExp, text: string): number {
	return [...text.matchAll(regex)].length;
}

/**
 * Classify a task text into a mode. Clear keyword evidence picks a stable
 * band (1 react / 0 spec); AMBIGUOUS or unmatched text returns 'weak' —
 * the internal-routing mode, where the model decides per task (P11 optimum).
 */
export function classifyTask(text: string): number | "weak" {
	const react = countHits(REACT_RE, text);
	const spec = countHits(SPEC_RE, text);
	if (react > spec) return 1;
	if (spec > react) return 0;
	return "weak";
}

/** True when the routed model id is a Flash-family model. */
export function isFlashModel(modelId: string | undefined | null): boolean {
	return typeof modelId === "string" && /flash/i.test(modelId);
}

/** Quantize a mode to one of the four measured behavior bands. */
export function bandOf(
	mode: number | "weak" | null | undefined,
): "spec" | "transition" | "react" | "weak" {
	if (mode === "weak") return "weak";
	const m = clamp01(mode);
	if (m < 0.2) return "spec"; // measured stable spec region (0..0.15)
	if (m < 0.5) return "transition"; // measured unstable band — avoid
	return "react"; // measured stable react region (0.5..1 behave alike)
}

/** Human-readable band name for a mode value. */
export function bandFor(mode: number | "weak" | null | undefined): string {
	const b = bandOf(mode);
	return b === "transition" ? "mixed" : b;
}

/** Persona for a mode; weak 带按模型分流(WEAK_PRO / WEAK_FLASH,与 dsh 原版一致)。 */
export function personaFor(mode: number | "weak", modelId?: string | null): string {
	switch (bandOf(mode)) {
		case "spec":
			return SPEC_PERSONA;
		case "transition":
			return MIXED_PERSONA;
		case "weak":
			return isFlashModel(modelId) ? WEAK_FLASH : WEAK_PRO;
		default:
			return REACT_PERSONA;
	}
}

/**
 * Per-message near-field guidance (Flash-only dispatch):
 *   round 1-2 → GUIDE_BASE; rounds 3+ → GUIDE_BOOST (anti-dilution);
 *   simple task → +GUIDE_COMMIT (fast convergence);
 *   complex task → +GUIDE_DEEP (directed depth, no rumination).
 */
export function guideFor(round: number, text: string): string {
	const base = round >= 3 ? GUIDE_BOOST : GUIDE_BASE;
	return isComplexTask(text) ? base + GUIDE_DEEP : base + GUIDE_COMMIT;
}

export function clamp01(v: number | string | null | undefined): number {
	return Math.min(1, Math.max(0, Number(v) || 0));
}

/**
 * First-turn core tool candidates (NarraFork tool names; shell added dynamically
 * by extension.ts). Candidates are intersected with the user's active tool set.
 *   spec → read-first; react → write-first; weak → edit (RL-shape analogue,
 *   shell + editor — NarraFork 的 Edit 即 str_replace_editor 对应物)。
 */
export function coreFor(mode: number | "weak"): string[] {
	switch (bandOf(mode)) {
		case "spec":
			return ["Read", "Edit", "Glob", "Grep"];
		case "transition":
			return ["Read", "Write", "Edit", "Glob", "Grep"];
		case "weak":
			return ["Edit"];
		default:
			return ["Read", "Write", "Edit"]; // write-first
	}
}

/** Test-suppression strength for a mode (informational). */
export function testinessFor(mode: number | "weak" | null | undefined): string {
	switch (bandOf(mode)) {
		case "react":
			return "suppressed";
		case "spec":
			return "normal";
		default:
			return "light";
	}
}

/**
 * Parse a user/agent-supplied mode token: number 0-100, 0.0-1.0, or a band name.
 */
export function parseMode(
	token: string | number | undefined | null,
): number | "weak" | "auto" | null {
	if (token === undefined || token === null) return null;
	const t = String(token).trim().toLowerCase();
	if (t === "auto") return "auto";
	if (t === "weak" || t === "router") return "weak";
	if (t === "spec" || t === "spec-lean") return 0;
	if (t === "balanced" || t === "mixed") return 0.3; // transition-band center
	if (t === "react" || t === "react-lean") return 1;
	const n = Number(t);
	if (!Number.isFinite(n)) return null;
	if (t.includes(".")) return clamp01(n);
	return clamp01(n / 100);
}

/** Extract plain text from an LLM Message content (string or Content[]). */
export function messageText(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.map((c) => {
			if (typeof c === "string") return c;
			const text = (c as { text?: unknown } | null)?.text;
			return typeof text === "string" ? text : "";
		})
		.join(" ");
}
