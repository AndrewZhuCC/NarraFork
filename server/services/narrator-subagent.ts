/**
 * Pure re-export barrel for backward compatibility.
 *
 * The original monolithic narrator-subagent.ts has been split into focused modules:
 * - subagent-alias.ts       — alias registry
 * - subagent-team.ts        — team file tracking + messaging
 * - subagent-executor.ts    — the delegated session's queue views + pure decisions
 * - subagent-runner.ts      — delegation: create, register, start (agent-runtime/run.ts runs it)
 * - subagent-tools.ts       — tool filtering + template base prompt
 *
 * All external imports from "@server/services/narrator-subagent" continue to work
 * via the re-exports below.
 */

// Background completion queue.
//
// Enqueue only: completions now share the ordered `parent-injection-queue` with inbound
// subagent messages, and `narrator-session.deliverPendingInjectionsInOrder` is its single
// consumer — so there is no per-kind drain to re-export.
export { pushBgCompletionNotification } from "./bg-completion-queue";

// Alias registry
export {
	clearAliasRegistry,
	getTaskAlias,
	registerTaskAlias,
	resolveTaskAlias,
} from "./subagent-alias";
// Human/model-facing labels derived from those aliases
export {
	type AgentLabelSource,
	agentLabelFromNarrator,
	clearAgentLabelMemo,
	resolveAgentLabel,
	shortAgentId,
} from "./subagent-label";

// Manual override + conclusion watchers
// Unified continuation/resume orchestration
// Runner (entry points)
export {
	BACKGROUND_TASK_TIMEOUT_MS,
	broadcastSubagentStarted,
	cancelBackgroundTask,
	type RunSubagentInput,
	runSubagent,
} from "./subagent-runner";

// Takeover state
// Team collaboration
export {
	clearTeamFileChanges,
	clearTeamInbox,
	deliverTeamMessage,
	drainTeamInbox,
	getTeamFileChanges,
	hasTeamMessages,
	recordTeamFileChange,
	type TeamMessage,
} from "./subagent-team";

// Tool filtering + system prompt
export { buildDelegationBasePrompt, resolveToolFilter } from "./subagent-tools";
