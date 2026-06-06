# Go Backend Parity Gap Spec

- Created: 2026-05-14
- Owner: main narrator
- Purpose: track the remaining gaps between `go_backend` and the original TypeScript backend until Go can be considered a safe replacement or a clearly scoped compatibility backend.
- Source of truth rule: this spec must be updated by agents as they claim work, verify evidence, and refine completion status.

## Coordination Protocol

1. Each subagent must claim exactly one track by changing its row from `Unclaimed` to `Claimed: <agent alias>`.
2. Each subagent may only edit its assigned track section plus the coordination log.
3. If a finding affects another track, add a short note to `## Cross-track Coordination Log` and, when useful, send a `TeamStatus` message to the relevant sibling agent.
4. Use statuses consistently:
   - `Unclaimed` — no agent has started this track.
   - `Claimed` — agent is investigating and updating the section.
   - `Drafted` — initial gap list is written but not reviewed against code evidence.
   - `Verified` — evidence paths/line references are checked.
   - `Blocked` — investigation needs a decision or unavailable context.
   - `Done` — spec section has prioritized gaps, notes, and acceptance criteria; it does **not** mean the documented gaps are fixed or that Go is replacement-ready.
5. Do not modify `drizzle/`, do not connect to or mutate `~/.narrafork/narrafork.db`, and do not run migrations.
6. Prefer concrete repo evidence over inference. Mark inference explicitly.

## Progress Board

| Track | Owner | Status | Last update | Notes |
|---|---|---|---|---|
| API Contract Parity | spec-api | Verified | 2026-05-18 | Route inventory checked; frontend-driven JSON/SSE/WS contract matrix added; P0/P1 diff gates remain |
| DB / Schema / FTS Parity | spec-db | Done | 2026-05-14 | Main DB is TS-owned/no Go migrations; FTS/search parity has P0 gap; schema usage gaps listed |
| Domain Lifecycle Parity | spec-domain | Done | 2026-05-14 | Verified lifecycle P0/P1/P2 gaps for projects, chapters, fork/merge/review/ruler/graph/git |
| Narrator / Agent Parity | spec-narrator | Done | 2026-05-14 | Verified parity gaps: message/tool persistence partial; permission/plan/subagent/compact/buffer/goals partial; hooks/sidecars/usage-cost/provider runtime incomplete |
| Runtime / System Integration Parity | spec-runtime | Done | 2026-05-14 | Runtime/system parity section completed; P0 remains WS/terminal/container/runtime side-effect parity |
| Frontend Contract / Smoke Tests | spec-frontend | Verified | 2026-05-14 | Frontend API/WS contract mapped to Go routes; smoke checklist and P0/P1/P2 break/degrade list documented |
| Final Synthesis | spec-synthesis | Done | 2026-05-14 | Replacement is unsafe/not ready; cross-track remediation plan recorded |
| TS-to-Go Readiness Matrix | main narrator | Drafted | 2026-05-18 | Cross-track matrix added for API, DB, Agent, runtime, and release cutover gaps |
| 2026-06-06 Codex Gap Audit | main narrator + nug2 swarm | Drafted | 2026-06-06 | Fresh multi-agent gap inventory appended; local spot-checks only, no runtime test run |

## Cross-track Coordination Log

- 2026-05-14 — Spec initialized. Agents should append short dated notes here when a gap spans multiple tracks.
- 2026-05-14 — Domain Lifecycle Parity notes cross-track coverage needed for Go lifecycle smoke tests: Git-dependent project/chapter/fork/merge/review/ruler/git routes must cover Git-missing behavior against TS `GIT_NOT_INSTALLED` guard, and frontend/runtime smoke should include narrator WS/event side effects for lifecycle flows (merge/review/batch merge/ruler AI resolve).
- 2026-05-14 — Narrator/Agent track notes that frontend/runtime parity must smoke-test narrator WS event shapes (`permission_resolved`, tool progress/output, compact progress, subagent/background events, `goals_set`) because Go currently broadcasts a reduced event surface compared with TS. API/auth-boundary and Git-missing findings from sibling tracks do not change narrator P0s directly, but should be included in replacement smoke coverage for narrator tools that call Git or privileged APIs.
- 2026-05-14 — spec-api: API contract review found cross-track blockers: Go was believed to expose `/api/notification-sounds` POST/DELETE publicly while TS mounts mutations after auth, and Go lacked TS's `GIT_NOT_INSTALLED` guard for Git-dependent routes. Runtime/frontend/domain tracks should account for setup/offline behavior and auth-boundary smoke tests. Later revalidation shows both findings are obsolete in current Go: notification-sounds routes are authenticated and the TS-style Git-missing guard is implemented; keep both smoke rows active.
- 2026-05-14 — spec-runtime: runtime/system review found replacement blockers in terminal WS replay/dtach/VNet, container proxy/port side effects, and volume snapshot real Podman archive flows. Frontend/API smoke tests should assert explicit fallback or disabled UI for gateway, VNet, update apply, storage cleanup, MCP external runtimes, benchmarks, and snapshot behavior.
- 2026-05-14 — spec-synthesis: final synthesis completed after all tracks reached Verified/Done. Replacement remains unsafe until cross-track P0s are remediated or explicitly disabled with frontend-visible fallbacks and smoke evidence.
- 2026-05-15 — main narrator: revalidated the previous spec against current code and fresh `codex:gpt-5.5` audit outputs; appended Wave H/I implementation dispatch plan. Older findings marked as potentially stale where current code evidence differs.
- 2026-05-15 — Wave H08 implementation subagent: first-phase runtime/ws/terminal/static ops parity fixes added for JSON heartbeat, explicit VNet disabled fallback, terminal backpressure/last-input resize/capabilities, static frontend/API-only fallback, health version/commit/capabilities, changelog override-dir loading, and update manual-only metadata. Remaining H08 runtime gap: true bundled/embedded Go changelog generation and any future full VNet runtime implementation.
- 2026-05-15 — Wave H01 implementation: aligned current Go admin-only boundaries with TS evidence (`server/routes/chapters.ts`, `storage.ts`, `benchmarks.ts`): `POST /api/chapters/podman/install`, all `/api/storage/*` routes, and benchmark results/summary/compare now reject non-admin users with 403 `FORBIDDEN` before side effects. Added focused Go route tests in app/chapters/systemroutes. Verification commands attempted from `go_backend`: `go test ./internal/app`, `go test ./internal/chapters`, `go test ./internal/systemroutes`, `go test ./internal/misccompat`; app/chapters/systemroutes are blocked before H01 tests by unrelated `internal/ws` compile errors (`queueSend*`, active terminal client helpers missing), and misccompat is blocked by unrelated `internal/narrators` duplicate helper declarations.
- 2026-05-15 — Wave H03 implementation: Go narrator agent replay now rebuilds history from `narrator_messages.content_json` plus completed `narrator_tool_calls`, preserving text, `tool_use`, `tool_result`, reasoning/redacted thinking where supported, compact summary boundaries, and trailing tool results for provider adapters. Added focused fixture tests in `go_backend/internal/narrators` and `go_backend/internal/agentbasic`; verified with `go test ./internal/narrators` and `go test ./internal/agentbasic`. Provider-specific gaps remain for images/sidecars/native web-search/image-generation exact replay.
- 2026-05-15 — Wave H05 implementation subagent: first-phase Go tool inventory parity added for core/optional/review/reflection categories, enabled optional tools, disabled-tools trait filtering, run-start tool inventory events, and explicit unsupported external MCP injection metadata.
- 2026-05-18 — Goal 5 implementation: Go now injects connected stdio MCP tools into narrator provider tool lists, executes `mcp__server__tool` calls through per-call JSON-RPC stdio sessions, applies TS-compatible MCP behavior permissions, filters MCP tools for subagents, and reports stdio/per-call MCP parity in capabilities. Remaining MCP risk: persistent MCP runtimes and non-stdio transports are still follow-up work.
- 2026-05-15 — Wave H04 implementation subagent: Go subagent message insertion now binds every child user/assistant/system/draft message to the parent Agent `parent_tool_use_id`, including foreground/background Agent launches and later Send/TeamStatus messaging. Parent narrator subscribers receive mirrored child message broadcasts with `childNarratorId`/`parentToolUseId`, and focused tests cover DB rows, tree/catch-up completeness, Agent foreground/background, Send/Await, TeamStatus, and WS package compatibility. Verified with `go test ./internal/narrators -run 'TestH04|TestSubagent|TestMessagesAfter|TestMessagesCursor|TestBackgroundAgent'`, `go test ./internal/narrators`, and `go test ./internal/ws` from `go_backend`.
- 2026-05-18 — main narrator: synthesized fresh miao:gpt-5.5 track reviews into a cross-track TS-to-Go backend readiness matrix. Current judgment remains unsafe for default replacement until matrix P0 gates are closed or explicitly accepted as disabled-by-design with frontend-visible fallbacks and smoke evidence.
- 2026-05-18 — main narrator: used three `miao:gpt-5.5` read-only subagents plus local frontend/API inspection to add a frontend-driven TS-vs-Go JSON/SSE/WebSocket contract matrix. New matrix ties core UI surfaces to transport type, current Go status, and required diff assertions.
- 2026-05-18 — rollback/edit-regenerate implementation: Go narrator rollback and edit-and-regenerate routes moved from `FEATURE_DISABLED` to active service logic with message-ref truncation, block truncation, tool-call invalidation, optional file-state rebuild, copy-on-write user edits, WS broadcasts, and optional agent rerun coverage. Focused tests were added in `go_backend/internal/narrators/rollback_edit_test.go`; health capability metadata now reports the feature as supported.
- 2026-05-18 — main narrator: Go beta engineering path now has explicit build/release/rollback entrypoints (`build:go`, `release:go:beta`, `rollback:go:drill`, `dev:go`) and the release flow uploads metadata + zstd patch only while keeping TS default/legacy commands unchanged.
- 2026-05-26 — main narrator: Go narrator deletion remains disabled-by-design (`FEATURE_DISABLED`) because recursive shared-message cleanup is not safe yet, but `/api/health` now exposes `capabilities.narrator.delete` and the archived narrators UI disables the destructive delete action with the backend reason instead of offering a misleading mutation.
- 2026-05-26 — main narrator: Go Kiro chat SSE remains disabled-by-design (`POST /api/kiro/chat` returns `FEATURE_DISABLED`), and `/api/health` now exposes `providers.kiro.routes.chat=false` with a route-specific reason so frontend/provider capability gates do not advertise the Kiro chat stream as available.
- 2026-05-26 — main narrator: tightened Go `/api/search` parity around the DB/FTS gate: chapter LIKE fallback now uses the TS `substr(COALESCE(description,title,''),1,240)` snippet contract, and focused search tests cover ready FTS metadata/rank/snippets, CJK trigram substring hits across chapters/messages/narrators, TS-compatible chapter LIKE snippets, FTS-missing degraded metadata, short-query non-degraded LIKE, invalid entities, and sanitization. Verified with `go test ./internal/search`; `gofmt -l internal/search/handlers.go internal/search/handlers_contract_test.go` produced no output.
- 2026-05-26 — main narrator: narrowed narrator WS event parity by preserving TS optional fields when Go legacy agent events are converted for frontend consumption: `tool_call` → `tool_started` now retains `input`, `streamStartedAt`, and `parentToolUseId`; `tool_result` → `tool_completed` now retains `durationMs`, `updatedInput`, `metadata`, `sideCars`, and `parentToolUseId`. Verified with `go test ./internal/ws`; `gofmt -l internal/ws/hub.go internal/ws/hub_narrator_events_test.go` produced no output.
- 2026-05-26 — main narrator: aligned Go narrator goal REST mutations with TS frontend/runtime expectations: `POST /goals` now normalizes/deduplicates objectives and activates the first open goal; PATCH active demotes the previous active goal; DELETE soft-cancels instead of hard-deleting and activates the next pending goal; clear soft-cancels open goals; reorder filters/appends known open goals; all mutating paths broadcast `goals_set` for `useNarratorWS`/recent-tabs consumers. Focused coverage added in `go_backend/internal/narrators/handlers_patches_test.go`; verified with `go test ./internal/narrators -run 'TestGoalRESTMutationsBroadcastGoalsSetAndSoftCancel|TestPostMessageGoalSlashAddListResumeComplete|TestAddGoalHandlerReturnsQueryErrorWithoutInsertingFallbackState|TestRunBasicAgentPlanAndGoalReflectionToolsWorkThroughEventLoop|TestRunBasicAgentUpdateGoalRejectsLegacyNonActiveGoalID|TestRunBasicAgentGoalReflectionRevisePersistsNextSteps'` and `go test ./internal/narrators -count=1`; `gofmt -l internal/narrators/service.go internal/narrators/handlers.go internal/narrators/handlers_patches_test.go` produced no output.
- 2026-05-26 — main narrator: added bounded Go goal-continuation runtime parity for active goals. After a successful non-background/non-plan run, Go now persists a TS-shaped system message containing a `goal_continuation` block, broadcasts both the message and `goal_continuation`, injects that prompt into the next provider history turn, and suppresses continuation after three consecutive no-tool continuation turns. UpdateGoal reflection now idempotently records the tool-call row before reflection metadata updates, avoiding empty `permission_suggestions` races. Focused coverage added in `go_backend/internal/narrators/agent_event_integration_test.go`; verified with `go test ./internal/narrators -count=1`, `go test ./internal/ws -count=1`, and clean `gofmt -l`/`git diff --check` on touched files.
- 2026-05-26 — main narrator: narrowed active-goal usage accounting parity. Go narrator runs now merge provider `context_usage` token snapshots, compute TS-style non-cached input plus completion deltas across bounded continuation turns, add wall-clock turn seconds to the active goal, and broadcast `goals_set` after counter updates. Focused coverage `TestRunBasicAgentAccountsActiveGoalUsageAndBroadcastsGoalsSet` verifies token/time counters and frontend-visible broadcast updates; verified with the focused Go test, `go test ./internal/narrators -count=1`, and `git diff --check` on touched files.
- 2026-05-26 — main narrator: narrowed narrator API-request usage persistence parity. Go agent turns now emit provider-call `api_request_start`/`api_request_end` lifecycle events around real adapter calls, persist `api_requests` rows from those events, link finished requests to the assistant message once the draft is finalized, and update assistant message usage metadata (`tokens_in`, output/cache/reasoning tokens, provider/model, turn usage JSON, and known-model cost). The focused usage test now covers active-goal counters, `api_requests`, and assistant-message usage metadata; `TestRunAgentLoopMapsProviderLifecycleEvents` covers the provider lifecycle event surface.
- 2026-05-27 — main narrator: aligned Go narrator `/usage-stats` with the TS `UsageHistoryStats` shape consumed by the details panel. The route now defaults to including direct subagents, honors `includeSubagents=false`, returns `totalRequests`/`totalInputTokens`/`totalCost`/average timing fields while keeping legacy aliases, and fails open with zero usage when `api_requests` or newer usage columns are absent.
- 2026-05-27 — main narrator: narrowed global usage-history frontend contract parity. Go `/api/usage-history` list/detail/stats/timeseries now build dynamic queries against available `api_requests` columns, fail open for missing usage columns, expose frontend record fields (`credentialId`, cache 5m/1h tiers, narrator/chapter/project titles/ids), keep raw dumps out of list payloads, and parse `rawDump` only on detail responses.
- 2026-05-27 — main narrator: narrowed narrator API request raw-dump parity. Go `api_request_end` persistence now honors `agent.requestDumpEnabled`, `agent.requestDumpErrorsOnly`, and `agent.requestDumpMaxSize` when provider events carry `rawDump` metadata; raw dumps remain disabled by default and oversized dumps are replaced with valid truncation metadata instead of storing partial JSON.
- 2026-05-27 — main narrator: fixed a chapter delete schema-compatibility gap exposed by all-internal tests. Go chapter deletion now archives/detaches chapter narrators using only available narrator columns, so compact/older test schemas without `narrators.status` still clean `recent_tabs` and delete the chapter instead of failing before tab cleanup.
- 2026-05-27 — main narrator: repaired Go DB/TS fixture verification after the TS repository switched to a single baseline Drizzle migration while bun-final compatibility artifacts still preserve the 41-entry historical journal. Go migration compatibility now accepts both the bun-final historical journal and the current repository baseline journal after canonical schema readiness, and the TS provider fixture test now runs Bun from the repository root with an isolated HOME so `./drizzle` resolves safely.
- 2026-05-27 — main narrator: narrowed ask-in-passing permission safety parity. Go now rejects `/api/narrators/:id/permission-mode` changes for ask-in-passing narrators until promotion, matching TS's read-only lock instead of allowing accidental escalation to `bypassPermissions`.
- 2026-05-27 — main narrator: narrowed malformed tool-input safety parity. After permission/danger checks but before execution, Go now rejects malformed/truncated tool arguments (including `_raw`) and empty `Write`/`Edit` inputs with failed tool results, preventing file mutation even under `bypassPermissions`/`trust-all`. Focused coverage `TestRunBasicAgentRejectsMalformedAndEmptyWriteInputsBeforeMutation` verifies no file is created and tool-call rows fail.
- 2026-05-27 — main narrator: narrowed plan-mode permission redirection parity. Non-relaxed Go plan mode now redirects `Write`/`Edit` calls aimed at Markdown files to the designated `.narrafork/plan-{id}.md`, allows that plan-file write under read-only plan mode, appends a notice to the tool result, and persists the redirected `input_json` so stored tool-call audit matches the actual path. Focused coverage `TestRunBasicAgentPlanModeRedirectsMarkdownWriteToPlanFile` verifies original Markdown path is untouched and the plan file receives the content.
- 2026-05-27 — main narrator: narrowed pending-permission reprocessing/broadcast parity. When Go permission mode switches to `bypassPermissions`, non-question pending tool calls for that narrator are re-resolved through the normal Go permission resolver instead of remaining stuck in `pending`; allow/deny decisions update DB metadata and broadcast `permission_resolved`. Reprocessing now also includes child subagent pending rows when the parent narrator is the broadcast target, while still resolving each row through its owning narrator's permission policy. Subagent pending permission requests now broadcast to the parent narrator target, REST resolution targets the parent broadcast channel, `GET /narrators/:id/permissions` includes child pending permissions with `subagentNarratorId`, and parent-scoped tool-call detail / permission-file-preview routes can read child tool calls using the owning subagent cwd, matching the TS `broadcastTargetId` model; resolved events include `subagentNarratorId` when owner and broadcast target differ. Focused coverage `TestRunBasicAgentBypassPermissionModeReprocessesPendingTool`, `TestSubagentPendingPermissionBroadcastsToParentNarrator`, `TestParentPermissionsListIncludesSubagentPendingPermission`, `TestParentCanReadSubagentToolCallDetailAndPermissionPreview`, and `TestParentBypassReprocessIncludesSubagentPendingPermission` verifies waiting writes resume after bypass-mode reprocess and subagent permission prompts/list/detail/preview/reprocess events resolve through the parent channel.
- 2026-05-27 — main narrator: narrowed plan-mode soft-deny parity. Non-relaxed Go plan mode now routes the first non-plan-file `Write`/`Edit` implementation attempt through a pending permission with a plan-file hint; when the user approves, Go enables `relaxed_plan`, broadcasts `relaxed_plan_changed`, and executes the original write. Repeated unapproved attempts are denied, and exiting/re-entering plan mode resets the one-shot prompt state. Focused coverage `TestPlanModeStoresPlanStateAndRestoresPermission`, `TestPlanModeRestrictsWriteEditToPlanFile`, and `TestRunBasicAgentPlanModeApprovalEnablesRelaxedPlanAndExecutesWrite` locks the unit and runtime paths.
- 2026-05-27 — main narrator: narrowed ExitPlanMode approval parity. Go no longer lets `ExitPlanMode` directly leave plan mode after readiness reflection; it now prepares the plan/reflection metadata, creates a pending user permission, keeps `plan_mode=1` until approval, and only exits after the permission is approved. Manual plan-mode exit now cancels pending `ExitPlanMode` approvals so the agent does not wait for the full permission timeout. Approval input updates now refresh the tool arguments before execution, so an `updatedPlan` merged into `input_json` is the plan used after approval; Go also stores a TS-style line diff in `input_json.planDiff` and returns it from the tool payload for later continuation prompts. Deny/cancel routes no longer merge `updatedPlan` or `compactAfter`, matching TS where updated input is persisted only on allow, and `permission_resolved` now omits `updatedInput` when no update exists. Plan compact persistence now honors `compactAfter`: Go compacts the approved plan only when requested and otherwise skips `context_summary` mutation. The success payload now includes TS-style `continueMode` and `continuePrompt` metadata for normal, edited-plan, and compact continuations, and approved `ExitPlanMode` now aborts the current run after the tool result, persists the explicit continuation user prompt, and restarts the agent loop to begin execution. `ExitPlanMode` and `AskUserQuestion` are excluded from generic danger reflection to avoid routing plan/user approval tools through the wrong reflection loop. Focused coverage `TestRunBasicAgentExitPlanReflectionWorksThroughEventLoop`, `TestManualPlanModeExitCancelsPendingExitPlanModeApproval`, `TestCompleteExitPlanModeSkipsPlanCompactWithoutCompactAfter`, `TestExitPlanContinuePromptIncludesDiffAndCompactMode`, `TestMergePermissionInputUpdatesStoresExitPlanDiff`, `TestPermissionApproveRouteMergesExitPlanUpdatedPlanAndCompactAfter`, `TestPermissionDenyRouteDoesNotMergeExitPlanUpdatedPlan`, and `TestPermissionDenyBroadcastsResolvedAndPreservesPendingSideEffectState` verifies plan mode remains active until approval, edited plans are applied through helper/route allow paths only, pending approvals are cancelled on manual exit, no compact is written without `compactAfter`, continuation metadata is emitted and consumed through abort/restart, edited-plan diff metadata is persisted, and deny broadcasts avoid null updated input fields.
- 2026-05-27 — main narrator: narrowed Bash file side-effect parity. Go built-in Bash execution now mirrors TS's before/after `git status --porcelain` snapshot hook: it captures newly dirty paths after Bash, stores first snapshots in `narrator_file_snapshots` with committed `HEAD` content for tracked files and null originals for newly created files, and stores bounded `output_json.bashSnapshots` metadata so rollback/rebuild can replay Bash-created/modified file contents alongside Write/Edit chains without polluting user-visible/model-visible Bash output. Focused coverage `TestBashToolSnapshotsGitStatusDiffFiles` verifies tracked and untracked Bash mutations snapshot and rebuild correctly.
- 2026-05-27 — main narrator: narrowed post-tool git-status side-effect parity. After successful Go `Write`/`Edit`/`Bash` execution for chapter-bound narrators, Go now broadcasts a TS-shaped `git_status` payload with `chapterId`, `toolUseId`, dirty counts, ahead count, base branch, and line totals so frontend git panels can refresh after file-mutating tools. Focused coverage `TestBroadcastGitStatusAfterFileMutatingTool` verifies the broadcast contract.
- 2026-05-27 — main narrator: narrowed narrator hook runtime parity. Go narrator execution now reads enabled global/project `hooks` rows, filters by matcher/tool name, sends TS-shaped `PreToolUse`/`PostToolUse` hook input payloads, executes command hooks with stdin JSON and exit-code-2 blocking semantics, executes HTTP hooks with JSON POST and `{decision:"block"}` parsing, blocks only explicit `PreToolUse` decisions, and treats `PostToolUse` as non-blocking/fail-open after tool completion. Focused coverage `TestRunBasicAgentPreToolUseCommandHookBlocksTool`, `TestPreToolUseHTTPHookBlockDecision`, `TestRunBasicAgentPostToolUseCommandHookReceivesPayloadAndDoesNotBlock`, and `TestPostToolUseHTTPHookReceivesErrorPayload` verifies command/http blocking and post-tool non-blocking payload behavior.
- 2026-05-29 — main narrator: revalidated PostToolUse hook parity against TS `server/services/hook-service.ts`, `server/lib/agent/tool-executor.ts`, and Go `go_backend/internal/narrators/service.go`. Current Go already triggers non-blocking/fail-open PostToolUse hooks after handled contextual/external/built-in tool completion, includes TS-shaped `tool_output`/`tool_is_error`/`tool_input` payloads, and broadcasts `hook:executed` telemetry. Rechecked with `go test ./internal/narrators -run 'TestAgentHookExecutionBroadcastsTSCompatibleTelemetry|TestRunBasicAgentPreToolUseCommandHookBlocksTool|TestPreToolUseHTTPHookBlockDecision|TestAgentHTTPHookHonorsHeadersAndTimeoutTelemetry|TestRunBasicAgentPostToolUseCommandHookReceivesPayloadAndDoesNotBlock|TestPostToolUseHTTPHookReceivesErrorPayload' -count=1`.
- 2026-05-27 — main narrator: narrowed sidecar read/hydration parity. Go message loading now hydrates `narrator_sidecars` onto message-level `sideCars`, tool-call `sideCars`, and matching `contentJson` `tool_use.sideCars`, including tool-use-only historical sidecars, so frontend message bubbles and tool inspectors can render TS-compatible sidecar notices. Tool-call detail now also returns `sideCars` when present. Focused coverage `TestMessagesHydrateNarratorSideCars` verifies message/tool/content block hydration.
- 2026-05-27 — main narrator: narrowed update-download preflight contract parity. Go `/api/update/download` now matches TS by returning JSON before opening SSE when trusted update metadata is unavailable (`404 {error:"No update available"}`) or the requested version is stale (`409` with requested/latest versions), while preserving SSE for actual download attempts. Focused coverage `TestUpdateDownloadWithoutTrustedSourceReturnsTSStyleNoUpdateJSON`, `TestUpdateDownloadTrustedNoUpdateReturnsTSStyleNoUpdateJSON`, `TestUpdateDownloadRejectsRequestedVersionMismatch`, and `TestUpdateDownloadRefetchesTrustedMetadataAndIgnoresClientURLs` verifies no misleading SSE success is advertised for preflight failures.
- 2026-05-27 — main narrator: narrowed `/api/settings` optional provider-array response parity. Go now returns TS-style response-only empty `nkpProviders`, `nugProviders`, and `clineProviders` arrays when those optional arrays are absent from persisted settings, while keeping Go config defaults unpersisted. Focused coverage `TestGetSettingsIncludesTSOptionalProviderArraysWithoutPersistingDefaults` plus `go test ./internal/settings -count=1` verifies the response shape and no default persistence.
- 2026-05-27 — main narrator: narrowed dev-build update check/download parity. Go now treats non-release current versions such as raw `go build`'s `go-backend-dev` as non-downloadable while preserving server `latestVersion`, so `/api/update/check` does not expose `releaseInfo`/download sizes and `/api/update/download` returns TS-style JSON 404 instead of opening SSE for a dev/contract build. Release-like semver builds still download from the trusted configured source. Focused coverage `TestUpdateCheckDevBuildDoesNotAdvertiseDownloadableUpdate`; verified with `go test ./internal/systemroutes -count=1`, app update focused tests, and the TS-vs-Go contract harness.
- 2026-05-27 — main narrator: narrowed `/api/settings` fresh fixture shape parity. Go now mirrors TS by returning `kiroModels: null` when Kiro credentials/models are unavailable, preserves array output when Kiro model metadata exists, and does not expose the Go-only top-level `nugQuotas` field in the settings response. The contract diff harness now canonicalizes object-key order before shape comparison, so `json.settings.shape differs` no longer appears; focused coverage `TestGetSettingsMatchesTSKiroAndNugQuotaResponseShape` plus `go test ./internal/settings -count=1` verifies the behavior.
- 2026-05-27 — main narrator: narrowed Search/FTS TS-created fixture parity. The TS-Go contract harness now seeds a minimal project/chapter/narrator/message fixture directly into the TS-migrated SQLite DB after TS startup/FTS initialization, then samples CJK trigram search (`叙事分叉`) and short-query LIKE search (`Al`). Semantic assertions require CJK results to include chapter/message/narrator and short-query results to include chapter on both backends; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: expanded WebSocket auth-boundary coverage. The TS-Go contract harness now samples no-token `/ws/narrator` and `/ws/terminal` connections and semantically asserts they do not open on either backend; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: expanded auth-boundary contract coverage. The TS-Go contract harness now samples no-token access to core protected routes (`/api/projects`, `/api/settings`, `/api/storage/cached`, `/api/runtime/scan`, `/api/benchmarks/suites`) and semantically asserts 401 on both backends; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: expanded benchmark route empty/error-state coverage. The TS-Go contract harness now samples empty suite/run lists, missing-run results/summary, and compare validation (`/api/benchmarks/compare` without `runIds`), with semantic assertions for missing summary 404, compare 400, and missing results array behavior; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: expanded update UI empty-state coverage. The TS-Go contract harness now samples `POST /api/update/cleanup`, `GET /api/update/directory`, `GET /api/update/status`, and `GET /api/update/version` in addition to check/download/apply, with semantic assertions that cleanup succeeds without fake update readiness and status remains `ready:false`; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: expanded provider missing-state contract coverage for NUG. The TS-Go contract harness now samples `GET /api/nug/providers/missing/quota`, `/usage`, `/usage/summary?range=30days`, and `/channels/health`, with semantic assertions that each returns 404 `Provider "missing" not found`; full harness remains `diffs: []`.
- 2026-05-27 — main narrator: narrowed frontend storage-cleanup capability consumption for Go fallback payloads. `getStorageCleanupOperationCapabilities` now has an integrated Go-shaped fixture proving uploads remain storage-safe (`preservesMessageImageRefs=true`), worktree cleanup is rerouted to runtime when storage cleanup is disabled, and container storage cleanup remains disabled even when runtime container cleanup exists as an alternative. Focused coverage `bun test frontend/hooks/usePlatform.test.ts -t "consumes Go storage cleanup fallback capabilities"` locks the UI-side gate so Go's disabled-by-design storage cleanup does not look directly executable.
- 2026-05-27 — main narrator: narrowed storage scan SSE parity and main-thread safety. The TS-vs-Go contract harness now streams SSE bodies until terminal `complete`/`error` events instead of waiting for a potentially long-lived response close, and samples `GET /api/storage/scan`. That exposed a Go single-connection SQLite deadlock where `databaseTableSummaries` held `sqlite_master` rows open while issuing per-table `COUNT(*)` queries; Go now collects table names, closes the rows, then counts tables, restoring the TS-compatible `progress/category/.../complete` event sequence. Focused coverage `TestStorageCachedReturnsLastCompletedScan` now uses `SetMaxOpenConns(1)` to lock the deadlock regression, and the full harness reports `diffs: []`.
- 2026-05-29 — main narrator: narrowed Search result item contract proof. The TS-Go contract harness now asserts concrete fixture result rows, not only result types: CJK chapter/message/narrator hits must expose expected ids plus frontend-consumed `snippet`, `matchField`, `matchScore`, status/role/messageRole/narrator/chapter linkage fields, and short `Al` chapter LIKE fallback must keep `matchField:"title"`. Revalidated with `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts` and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`); remaining Search risk is larger realistic relevance ordering rather than baseline item shape.
- 2026-05-29 — main narrator: narrowed Update UI admin-boundary, degraded-error, and trusted-check proof. The TS-Go contract harness now registers an admin and a regular user, asserting regular users can still read `/api/update/check` and `/version` (200) while `/directory`, `/status`, `/cleanup`, `/apply`, and SSE `/download` return 403 on both TS and Go. It also runs a local trusted update server fixture to sample positive `/api/update/check` for admin and regular users, locking `updateAvailable:true`, `latestVersion`, `releaseInfo.version/path`, and `downloadSize/totalSize` response shape, plus a trusted download-error fixture that locks SSE `progress → error` terminal shape without exercising the TS dev-mode zstd success path. A transient trusted-update network error exposed that Go omitted `latestVersion` from `/api/update/check` error payloads while TS always exposes a current/latest version string; Go now initializes `latestVersion` to `currentVersion` before remote lookup, so failed checks stay frontend-shaped and non-downloadable. The download-error fixture also exposed a Go-only extra `checking` progress event; Go now starts update download streams with TS-compatible `downloading` progress before the terminal event. Revalidated with focused `TestUpdateCheckErrorStillIncludesLatestVersion`, `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed admin-boundary proof for benchmark/runtime/storage surfaces. The TS-Go contract harness now asserts regular users receive 403 for `/api/benchmarks/suites`, `/api/benchmarks/compare`, `/api/runtime/cached`, `/api/runtime/scan`, `/api/runtime/cleanup`, `/api/storage/cached`, `/api/storage/cleanup`, `/api/storage/database/vacuum`, and SSE `/api/storage/scan`, while admin empty/fallback samples remain clean. Revalidated with `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts` and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`); no Go route change was needed because Go already wraps these with `adminOnly`/storage admin guards.
- 2026-05-29 — main narrator: narrowed settings/provider secret contract parity. The TS-Go contract harness now seeds real OpenAI/Anthropic/NKP/NUG provider secrets through TS `/api/settings`, samples admin and regular-user settings reads, and applies a masked-provider `PATCH` to both backends; semantic assertions require `auth.jwtSecret` to stay hidden and provider `apiKey`/`oauthClientSecret` values to remain masked in all responses. The new fixture exposed a Go `PATCH /api/settings` response drift where optional empty provider arrays such as `nkpProviders` were present on TS but omitted on Go; Go PATCH responses now reuse the GET response normalization (`ensureOptionalProviderArraysForResponse` and provider compatibility normalization) before sanitizing secrets. Verified with focused `go test ./internal/settings -run 'TestGetSettingsRedactsProviderSecretsAndPreservesMaskedPatch|TestGetSettingsIncludesTSOptionalProviderArraysWithoutPersistingDefaults' -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed `settings.customApiQuotas` cache-shape parity for narrator quota badges. The TS-Go harness now preloads `custom-api-quotas.json` before either backend starts and asserts admin/user `/api/settings` reads preserve TS's public custom quota payload with `quotaBalance`, `detailedQuotaBalance`, and `fetchedAt` (unlike NKP/NUG public quota endpoints, custom API settings intentionally expose `fetchedAt`; a subsequent masked provider PATCH may purge the seeded entry on both backends). Focused Go coverage `TestGetSettingsIncludesCustomApiQuotaCacheShape` locks the same response shape.
- 2026-05-29 — main narrator: narrowed `settings.nkpQuotas` parity for the narrator panel's NKP quota badge. After fetching a fixture NKP provider quota, the TS-Go harness now samples `/api/settings` again and asserts `nkpQuotas.nkp-secret-fixture` has exactly `quotaBalance` and `quotaTotalGranted` with no internal `fetchedAt`, matching `/api/nkp/quotas`. Focused Go coverage `TestGetSettingsIncludesNkpQuotaCacheShape` seeds `nkp-quotas.json` with an internal `fetchedAt` and verifies the settings response strips it.
- 2026-05-29 — main narrator: narrowed frontend split capability consumption safety. The TypeScript backend health response has no `capabilities` object and the TS chapter router does not mount `POST /api/chapters/:id/split`, while the Go backend exposes split through explicit `capabilities.chapters.split.routes.splitAtCommit=true`. `getChapterSplitCapability(undefined)` now defaults to unsupported instead of optimistic supported, so TS/older backends do not show split buttons that would produce 404s; Go remains enabled only when health explicitly advertises split route support, and missing `routes.splitAtCommit` metadata is treated as unsupported rather than optimistic. Focused frontend hook coverage locks the default-disabled, missing-route-disabled, and Go-supported/compressed-fallback cases.
- 2026-05-29 — main narrator: aligned Go split capability metadata with the implemented route semantics: health/graph domain capabilities now report `chapters.split.mode="split-at-commit"` instead of the earlier stale basic-split label, while compressed AI summaries remain explicitly fallback-gated.
- 2026-05-30 — main narrator: locked split health metadata at both route-test and TS-Go semantic-contract levels. `TestPhase0HealthCapabilitiesExposeKnownGoFallbacks` now asserts `capabilities.chapters.split.routes.splitAtCommit=true`, and the TS-Go contract harness now asserts Go health advertises `routes.splitAtCommit=true`, `mode="split-at-commit"`, and the frontend-consumed compressed AI summary fields (`supported=false`, `fallback=true`, deterministic placeholder mode, and reason) before frontend split gating can enable the action. Frontend split capability consumption now also treats missing `partials.compressedAISummary` metadata as not supported instead of optimistic support, so older/partial Go capability payloads still show the compressed-summary fallback warning. Focused split fixture coverage now also asserts the route JSON describes the historical prefix, continuation, and new fork commit boundaries, and compressed split inherit records an explicit unsupported `compressedContext` fallback, bounded copied prefix refs, persisted fallback metadata, and a deterministic fallback summary instead of silently pretending an AI summary was generated. Frontend update capability consumption now treats `update.manualOnly=true` as overriding missing or optimistic self-update/apply fields, preventing partial Go/manual-only payloads from re-enabling auto-apply UI.
- 2026-05-30 — main narrator: narrowed MCP transport capability fallback safety. `getMcpTransportsCapability(undefined)` still preserves TS/legacy behavior by defaulting all transports to supported when no capability object exists, but once an `mcp` capability object is present, non-stdio transports now require explicit `supported:true`; missing Go `mcp.transports.sse` or `streamableHttp` metadata no longer enables unsupported persistent/non-stdio runtimes. Focused frontend coverage locks legacy defaults, partial-Go missing metadata, and explicit unsupported metadata.
- 2026-05-30 — main narrator: narrowed narrator capability fallback safety for browser sessions, deletion, and rollback/edit-regenerate. Legacy TS/no-capability payloads still default to supported, but once a `narrator` capability object exists, `browserSessions`, destructive narrator `delete`, and rollback/edit-regenerate now require explicit `supported:true`; partial Go narrator capability payloads missing these fields no longer trigger browser-session REST fetches, re-enable archived narrator deletion, or expose rollback/edit-regenerate actions in `NarratorPanel`. Full `frontend/hooks/usePlatform.test.ts` now locks these explicit-gate cases alongside existing disabled/supported metadata cases.
- 2026-05-30 — main narrator: narrowed gateway capability fallback safety. Legacy TS/no-capability payloads still default to persistent gateway runtimes and Weixin QR support, but once a `gateway` capability object exists, `persistentRuntimes` and `weixinQr` now require explicit support metadata; partial Go gateway payloads missing those fields no longer make Telegram/Weixin appear runnable while preserving explicit webhook support and Go's supported/unsupported platform lists.
- 2026-05-30 — main narrator: narrowed benchmark container-execution capability fallback safety. Legacy TS/no-capability payloads still default benchmark container execution to supported, but once a `benchmark` capability object exists, `containerExecution.supported=true` is required; partial Go benchmark payloads missing `containerExecution` no longer make the settings/runtime UI advertise Podman-backed benchmark execution.
- 2026-05-30 — main narrator: narrowed runtime maintenance capability fallback safety. Legacy TS/no-capability payloads still default runtime scan/cache/cleanup targets to supported, but once a `runtime` capability object exists, `scan`, `cached`, and each cleanup target (`terminals`, `containers`, `browsers`, `worktrees`) now require explicit `supported:true`; partial Go runtime payloads missing cleanup target metadata no longer advertise maintenance actions.
- 2026-05-30 — main narrator: narrowed storage cleanup capability fallback safety. Legacy TS/no-capability payloads still default ordinary storage cleanup/database cleanup targets to supported while keeping VACUUM disabled, but once a capability payload exists, storage scan/cache, upload/share/worktree/container cleanup, and database cleanup targets require explicit support. Partial Go storage payloads missing cleanup metadata no longer advertise destructive cleanup actions or reroute worktree cleanup to legacy runtime defaults.
- 2026-05-30 — main narrator: narrowed filesystem/settings system-action capability fallback safety. Legacy TS/no-capability payloads still default filesystem browse/shortcuts/mkdir/preview/reveal and settings TLS generation to supported, but once a capability payload exists, each FS action requires explicit support and TLS generation requires `settings.tlsGeneration=true`; partial Go payloads missing FS/TLS metadata no longer expose reveal/path actions or the TLS generation button.
- 2026-05-30 — main narrator: narrowed provider capability fallback safety. Legacy TS/no-capability payloads still default provider routes, model refresh, quota, and provider agent mode to supported, but once provider capability metadata exists, model refresh requires `models.refreshSupported=true`, quota/agent mode require explicit `supported:true`, and provider routes require each route key to be explicitly `true`; partial Go provider payloads missing route or model/quota metadata no longer expose external provider calls or credential/runtime actions by default.
- 2026-05-30 — main narrator: narrowed chapter container and batch-merge capability fallback safety. Legacy TS/no-capability payloads still default these chapter actions to supported, but once capability metadata exists, container routes now require each route key to be explicitly `true`, and async batch merge requires explicit start/session routes, response fields, decision WS, stale-session cleanup, and created-target rollback support; partial Go chapter payloads missing these fields no longer expose Podman/container mutations or async merge flows by default.
- 2026-05-30 — main narrator: narrowed MCP capability fallback safety beyond transports. Legacy TS/no-capability payloads still default MCP protocol/tools, built-in tools, server-settings storage, external server management, and external tool/agent injection to supported, but once MCP capability metadata exists each feature now requires explicit `supported:true`; partial Go MCP payloads missing these fields no longer expose MCP service-management UI, external tool injection, agent injection, or builtin `tools/list`/`tools/call` paths by default.
- 2026-05-30 — main narrator: narrowed narrator runtime/tool capability fallback safety. Legacy TS/no-capability payloads still default plan mode, compaction, retry/continue/interrupt recovery, permission approve/deny, review tools, subagent controls, WebFetch, and Browser optional-tool affordances to supported, but once capability metadata exists these helpers now require explicit support and explicit action booleans; partial Go narrator payloads missing these fields no longer expose permission decisions, review mutations, subagent detach/background controls, retry/recovery actions, or browser/WebFetch tool surfaces by default.
- 2026-05-30 — main narrator: narrowed remaining frontend capability fallback safety for terminal/update/VNet/settings/uploads/shares/content/storage preview plus stricter split/gateway/MCP transport group absence. Legacy TS/no-capability payloads still default these UI surfaces to supported where historically expected, but once any capability payload exists, terminal runtime, update download/apply, VNet relay, settings storage/patch/secrets/TLS/retry rules, upload serving, share create/download/preview, project routines/skills, storage database preview, split route support, gateway platforms, benchmark containers, and all MCP transports require explicit support metadata.
- 2026-05-30 — main narrator: strengthened Go health capability contract coverage after tightening frontend gates. `go_backend/internal/app/route_contract_test.go` now asserts explicit `/api/health` fields consumed by the stricter frontend defaults, including gateway webhook, settings patch/security flags, upload avatars, share create/public download, and terminal `supported:true`, reducing the risk that Go silently disables implemented features under the new partial-payload gates. The TS-Go API contract harness now also asserts a compact list of explicit Go health capability fields used by the frontend gates (chapter container routes, terminal/VNet/update/gateway/MCP/settings/uploads/shares/storage/benchmark/narrator/content support), so future diff runs catch missing health metadata before UI regressions.
- 2026-05-30 — main narrator: narrowed provider/runtime allowlist safety. Go provider resolution no longer silently falls back from unsupported/unconfigured Kiro or Codex model prefixes to a different OpenAI/Anthropic-compatible provider by default; automatic fallback now requires explicit `agent.allowGoProviderFallback=true`, otherwise resolution fails before the provider request with a clear mismatch error. Focused and full `internal/agentbasic` tests cover Kiro/Codex default rejection plus explicit opt-in fallback; `internal/narrators` now also covers the POST `/messages` path persisting an idle/error state and making zero OpenAI-compatible fallback requests for an unsupported Kiro prefix.
- 2026-05-30 — main narrator: refreshed stale P0-A documentation against current Go agent history implementation. The previous `content_text`-only replay warning is obsolete: current Go `agentHistory` rebuilds provider context from stored `content_json`, completed tool-call rows, compact summaries, sidecars, and supported reasoning/redacted blocks. The remaining P0-A risk is now tracked as provider-specific rich block fidelity and TS-created long-session fixture smoke, not core tool transcript loss.
- 2026-05-30 — main narrator: narrowed rich-block replay for uploaded user images and text-file attachments. Go `agentHistory` now resolves persisted `image` content blocks from `~/.narrafork/uploads/{narratorId}/{imageId}*`, embeds bounded base64/media metadata into in-memory provider history, and the OpenAI Chat/Responses plus Anthropic adapters convert those blocks to provider-visible image inputs. It also preserves `contentText`'s `<attached_files>` prompt for persisted `text_file` blocks instead of replaying only the raw user text block, so provider history keeps the TS-visible Read-tool attachment hint after restart/fork. Focused and full `internal/agentbasic`/`internal/narrators` tests cover adapter mapping, DB-backed upload replay, and attachment-hint replay.
- 2026-05-30 — main narrator: narrowed native provider-block replay. Go provider adapters no longer drop persisted `web_search` blocks: Anthropic and OpenAI Chat Completions inject TS-style provider-visible `[Web search: ...]` user context before the assistant turn, while OpenAI Responses rebuilds native `web_search_call` input items without replaying the rejected `id` field. OpenAI Responses also rebuilds `image_generation_call` items from inline `result` or bounded `savedPath` file bytes, preserving `id`/`revised_prompt` and adding the TS-style saved-path developer instruction. Focused and full `internal/agentbasic` tests cover these replay paths.
- 2026-05-30 — main narrator: locked subagent history replay parity with a DB fixture. Current Go behavior matches the TS provider-history contract for `parentToolUseId`: primary narrators filter child-tree messages out of model history, while subagent narrators treat their own `parent_tool_use_id`-tagged user/assistant/tool-result transcript as top-level provider history. Focused and full `internal/narrators` tests cover the parent filter and subagent replay path.
- 2026-05-30 — main narrator: narrowed PreToolUse lifecycle ordering parity. Go now matches TS `tool-executor.ts` by running `PreToolUse` hooks after permission resolution but before malformed/`_raw` and empty `Write`/`Edit` guard handling, and hook payload construction preserves malformed raw input as `tool_input._raw` instead of silently passing `{}`. Focused coverage verifies a blocking `PreToolUse` hook sees `_raw` and wins before the malformed-input guard; full `internal/narrators` remains green.
- 2026-05-30 — main narrator: closed several special-tool and path fallback permission bypasses. Go now mirrors TS `resolvePermissionDecision`/`shouldAutoAllowSendWithinScope` for `Agent`, `Send`, `TeamStatus`, `AddGoal`, and `Terminal`, and no longer executes these tools before permission resolution where TS requires approval. `readOnly` narrators may only launch `explore`/`plan` Agent subagents, read-only Agent workdirs outside cwd ask for approval, write-capable Agent workdirs ask/deny according to permission mode, directory blacklists deny Agent workdirs before `bypassPermissions`, `Send` auto-allows only when every resolved target's permission rank is less than or equal to the caller's rank, and mode-only special tools such as `TeamStatus`/`AddGoal`/`Terminal` ask in `default`/`acceptEdits` while denying in `readOnly`/`dontAsk`. Go's generic fallback is now path-aware like TS: `Read`/`Glob`/`Grep`/`ShareFile` auto-allow only for in-cwd paths (external paths ask/deny by mode unless whitelisted), `.git` write targets are hard-denied for `Write`/`Edit`/Browser screenshots and common Bash destructive/redirection writes even in `bypassPermissions`, and Bash destructive operations on the project root, project-root ancestors, and `.worktrees` are hard-denied when the narrator is chapter-bound and `project.git_path` is available. Bash command whitelist decisions are now mode-sensitive like TS: `default` asks, `dontAsk` denies, `acceptEdits`/`bypassPermissions` allow, and `readOnly` only auto-allows commands that do not look write-capable. Common Bash command argument paths and redirection targets now participate in directory blacklist/whitelist checks with read-vs-write operation sensitivity and are resolved relative to the Bash `workdir` like TS. A TS-style catastrophic Bash hard-deny subset now blocks recursive deletes of critical paths, `dd` writes to block devices/system paths, recursive permission changes on system directories, and simple fork bombs before `bypassPermissions`. Safe Bash commands now follow TS mode behavior for the covered subset: read-only safe commands can auto-allow in `readOnly`, and safe worktree-local write helpers such as `touch` can auto-allow in `acceptEdits` while dangerous commands such as `rm` still ask. `WebFetch` now follows TS mode behavior after URL policy checks, including `bypassPermissions` auto-allow and `dontAsk` deny. `AskUserQuestion` and `ExitPlanMode` now suppress premature running tool-call records in every mode and resolve as always-ask like TS. `WebSearch` remains always-allow, and unknown tools such as `Browser` ask in default/acceptEdits and deny in readOnly. Tool-call event bookkeeping also treats non-always-allow tools as permission-gated in default/acceptEdits so asks do not first create stale running records. Focused unit/runtime coverage proves readOnly general `Agent`, readOnly-to-higher-rank `Send`, readOnly `TeamStatus`, external `Read`, and readOnly `Browser` are denied/asked before side effects, and full `internal/narrators` tests remain green.
- 2026-05-29 — main narrator: narrowed settings subroute contract coverage. The TS-Go contract harness now samples `/api/settings/context-thresholds`, unresolved `/api/settings/test-model`, invalid `/api/settings/retry-rules`, and valid retry-rule creation. It exposed that Go returned 21-character retry-rule IDs while TS uses `generateShortId()`; Go now uses `id.Short()` so retry-rule responses carry 8-character IDs while preserving 201 status, `enabled:true`, and submitted trigger fields. Verified with `go test ./internal/settings -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`). Positive `/api/settings/generate-tls` remains intentionally unsampled by the diff harness because it schedules a server restart and mutates TLS state, but focused Go coverage now locks TS-required response fields (`certPath`, `keyPath`, RFC3339 `expiresAt`, HTTPS `newUrl`, and `serverRestarting:true`) plus persisted TLS settings without launching a real replacement process.
- 2026-05-29 — main narrator: narrowed provider route auth/admin boundary plus OpenAI/Anthropic/NKP/NUG positive data parity. TS exposes OpenAI/Anthropic/NKP/NUG model refresh and NUG login/API-key/OAuth-start routes to any authenticated user (settings itself is authenticated-user mutable), while Kiro/Codex/Cline remain admin-only except Codex `quota-overview`. Go had stricter `adminOnly` guards on the OpenAI/Anthropic/NKP/NUG refresh/auth routes, which could break regular-user provider settings flows. Go now matches TS for those authenticated-user routes while preserving admin-only Kiro/Codex/Cline boundaries. The TS-Go harness samples regular-user OpenAI positive provider refresh against a local OpenAI-compatible fixture, asserting `fromCache:false` plus sorted/deduplicated fixture ids, then verifies cached `/api/openai/models` returns `fromCache:true`; it samples regular-user Anthropic positive provider refresh against a local Anthropic fixture that exercises the `/v1` fallback, asserting `fromCache:false`, sorted/deduplicated fixture ids, and `resolvedBaseUrl`; it samples regular-user NKP positive provider refresh against a local NKP/Kiro-compatible fixture, asserting `fromCache:false`, sorted/deduplicated `model_id` values, NKP normalization, and cached `/api/nkp/models` `fromCache:true`; and it samples regular-user NUG positive model refresh/cache plus quota, cached quotas, channels health, usage, and usage-summary normalization against a local NUG fixture. The harness still samples missing-provider refresh probes across OpenAI/Anthropic/NKP/NUG, NUG login/API-key/OAuth-start missing-provider probes, Codex quota-overview, and regular-user 403 for Codex/Cline/Kiro status. Focused Go coverage splits `TestProviderSensitiveRoutesRequireAdminBoundary` (Kiro/Codex/Cline) from `TestProviderModelAndNugRoutesRemainAuthenticatedUserCompatible` (OpenAI/Anthropic/NKP/NUG auth-user routes), and provider-focused tests cover model refresh plus NUG quota/channel/usage normalization. Verified with focused provider tests and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed OpenAI/Anthropic/NKP global models refresh mixed-provider result parity. The TS-Go harness now seeds each provider family with one valid provider and one no-key provider, then samples regular-user `POST /api/openai/models/refresh`, `POST /api/anthropic/models/refresh`, and `POST /api/nkp/models/refresh`. Semantic assertions lock the TS aggregate shape `{results,models,fromCache:false}`: valid providers report `count:2`, no-key providers report `count:0` with provider-name-specific errors (`OpenAI API key not configured for provider "OpenAI No Key Fixture"`, `Anthropic API key not configured for provider "Anthropic No Key Fixture"`, `NKP API key not configured for provider "NKP No Key Fixture"`), and aggregate model arrays remain sorted/deduplicated (`id` for OpenAI/Anthropic, `model_id` for NKP). Focused coverage `TestProviderGlobalRefreshReportsMissingKeyResults` locks the Go aggregate result behavior, and the full TS-Go harness remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed OpenAI/Anthropic/NKP provider-specific no-key refresh parity. The same TS-Go harness fixture now samples regular-user `POST /api/openai/providers/openai-no-key-fixture/models/refresh`, `POST /api/anthropic/providers/anthropic-no-key-fixture/models/refresh`, and `POST /api/nkp/providers/nkp-no-key-fixture/models/refresh`, asserting TS-compatible `500` status plus provider-name-specific `error` strings. Go already returned the matching status/error through the generic provider refresh path; focused coverage extends `TestProviderGlobalRefreshReportsMissingKeyResults` to lock both aggregate result rows and provider-specific failure payloads, and the full harness remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed NKP/NUG quota positive/cache parity for the provider settings UI. The TS-Go harness samples regular-user `GET /api/nkp/providers/nkp-secret-fixture/quota` and `GET /api/nug/providers/nug-secret-fixture/quota` against local fixtures, then validates cached `GET /api/nkp/quotas` and `GET /api/nug/quotas`. Assertions lock the frontend-consumed numeric fields only: NKP exposes `quotaBalance`/`quotaTotalGranted`, NUG exposes `balance`/`totalGranted`, and both public cache endpoints intentionally omit internal `fetchedAt`. Go already matched the direct responses and public cached value shape; focused coverage in `TestProviderModelRefreshReturnsFrontendContractShape` and `TestNugProviderEndpointsExposeFrontendContractShape` locks positive quota fetch/cache shape, and the full harness remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed NUG credential login/API-key positive parity. The TS-Go harness now seeds a separate no-secret NUG provider, drives `/api/nug/providers/:id/login` and `/api/nug/providers/:id/api-key` through a local credential fixture, and asserts the returned `apiKey`/`user` shape plus the TS side-effect contract that login/API-key responses do not implicitly persist credentials into provider settings; the frontend remains responsible for the explicit settings save. This exposed a Go drift where `nugLogin` persisted the generated API key and user metadata server-side. Go now only returns `{apiKey,user}` for login, while OAuth callback still persists credentials through its dedicated callback path. Verified with focused `go test ./internal/systemroutes -run 'TestNugLoginReturnsAPIKeyWithoutMutatingSettings|TestProviderModelAndNugRoutesRemainAuthenticatedUserCompatible' -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed NUG global models refresh multi-provider result parity. The TS-Go harness now samples regular-user `POST /api/nug/models/refresh` after seeding one valid provider plus two no-key providers, asserting the mixed `{results,models,fromCache:false}` shape: the valid provider reports `count:2`, no-key providers report `count:0` with TS's provider-name-specific `NUG API key not configured for provider "..."` error text, and the aggregate `models` array remains sorted/deduplicated. This exposed Go's aggregate refresh using the lower-level generic `Provider API key not configured` error string without provider names. Go now handles missing API keys at the aggregate result layer with TS-compatible per-provider error text while preserving provider-specific endpoint behavior. Focused coverage extends `TestNugProviderEndpointsExposeFrontendContractShape`, and the full harness remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed NUG provider-specific no-key model refresh parity. The TS-Go harness now samples regular-user `POST /api/nug/providers/nug-login-fixture/models/refresh` for a configured provider with no API key, asserting TS-compatible `500 {error:"NUG API key not configured for provider \"NUG Login Fixture\""}` before any upstream call. This exposed Go's provider-specific refresh path returning a generic lower-level 422. Go now checks the provider API key at the route layer for this refresh endpoint and emits the TS provider-name error while leaving cached reads and missing-provider behavior unchanged. Focused coverage extends `TestNugProviderEndpointsExposeFrontendContractShape`; the full harness remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed NUG credential input validation coverage for the provider settings flow. The TS-Go harness now samples regular-user `POST /api/nug/providers/:id/login` with a valid provider but missing username/password and `POST /api/nug/providers/:id/api-key` with missing `sessionToken`, asserting TS-compatible `400` errors (`Missing username or password`, `Missing sessionToken`) before any upstream call or settings mutation. Go already matched these semantics; focused coverage `TestNugAuthMissingFieldsMatchTSValidation` now locks the no-upstream validation boundary, and the full harness remains `diffs: []`.
- 2026-05-29 — main narrator: audited NUG quota public cache shape alongside the NKP quota follow-up. TS keeps `fetchedAt` internally in the in-memory NUG quota cache but `getAllNugCachedQuotas()` intentionally returns only `{balance,totalGranted}` for `/api/nug/quotas`. Go already mirrors this public shape while still persisting `fetchedAt` in `nug-quotas.json`; the existing TS-Go harness assertion for `userNugQuotas.nug-secret-fixture` requires exactly `balance,totalGranted`, and focused Go coverage checks the cached response has exactly two public keys.
- 2026-05-29 — main narrator: narrowed NUG OAuth start/callback positive parity. The TS-Go harness now drives `/api/nug/providers/:id/oauth/start` as a regular user, validates the generated authorize URL/state against a local NUG OAuth fixture, then calls the public `/api/nug/oauth/callback` with manual redirect handling and verifies the `302 /settings/providers?oauth_success=...` location plus persisted settings side effects (`apiKey` masked in settings, `oauthDeviceId`, `nugUsername`, and `nugUserId`). Focused Go coverage now locks the same redirect-uri reuse and credential persistence path in `TestNugOAuthCallbackPersistsCredentialsAndRedirects`. Verified with focused systemroutes tests and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline provider safe-route coverage. The TS-Go contract harness now samples admin Cline cached/error-state endpoints that do not start external OAuth or import credentials: `/api/cline/models`, `/providers/missing/models`, `/providers/missing/models/refresh`, `/pool/search`, `/pool/count`, invalid `/enabled-models`, valid `/enabled-models`, post-enable `/api/settings` provider view, `/auth/cancel`, invalid `/auth/callback`, and `/auth/logout`. Semantic assertions lock cache shapes, missing-provider 404 text, invalid body errors, valid `{ok:true,count,modelContextWindows}` response shape, persisted `clineProviders[0].enabledModels`, and auth cleanup responses; full `bun go_backend/testtools/ts-go-api-contract-diff.ts` remains `diffs: []`. Focused Go coverage also locks `/api/cline/recommended-models` fallback arrays and cached recommended/free model shapes for frontend consumers. Remaining Cline risk is browser auth start plus localhost callback-server flow, live external provider refresh, live recommended-model fetch against the Cline service, and credential mutation side effects beyond these safe probes.
- 2026-05-29 — main narrator: narrowed Cline populated cache and enabled-model add parity. The TS-Go harness now pre-seeds a deterministic `cline-cache-fixture` provider plus `cline-models.json` before either backend starts, so both TS and Go load a populated cached model pool without external OpenRouter/Cline calls. Contract samples now assert `/api/cline/models`, `/pool/search?q=fixture`, and `/pool/count` expose the two cached fixture models, then `POST /api/cline/enabled-models` enables `cline/free-fixture` and verifies `{ok:true,count:1}`, `modelContextWindows["clinecache:cline/free-fixture"] === 123456`, `totalModels:2`, and persisted settings `clineProviders[0].enabledModels`. Verified with focused `go test ./internal/systemroutes -run 'TestClineEnabledModelsStoresContextWindowsFromCache|TestClineProviderEndpointsExposeFrontendContractShape' -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline pasted OAuth callback and credential side-effect parity. Audit found Go imported callback tokens but missed TS's post-callback auto-add of free Cline models. Go now reuses the enabled-model/context-window updater from callback and browser-local callback paths, so callback import stores credentials, writes the provider access token, auto-enables free models from the current Cline recommended cache/fallback, and preserves frontend-safe masked accessToken output in settings. The TS-Go harness now posts a base64 callback URL to `/api/cline/auth/callback`, verifies `{ok,email,displayName}`, authenticated status and `providers[0].hasToken`, masked `clineProviders[0].accessToken`, non-empty auto-added `enabledModels`, and exact TS-vs-Go enabled-model equality. The harness also reseeds only the Cline model-pool cache before switching to Go so live recommended-model fetches from the TS phase do not pollute deterministic `/pool/*` assertions while the recommended-model side effect remains comparable. Verified with focused `go test ./internal/systemroutes -run 'TestClineAuthCallbackPersistsCredentialsAndAutoAddsFreeModels|TestClineEnabledModelsStoresContextWindowsFromCache|TestClineProviderEndpointsExposeFrontendContractShape' -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline browser auth start + localhost callback-server parity. The contract harness now exposes a local Cline `/api/v1/auth/authorize` fixture, drives `POST /api/cline/auth/browser` with `apiBaseUrl` pointing at that fixture, follows the returned `authorizeUrl` back to the fixed local `http://localhost:19876/auth/callback`, and waits for TS/Go callback side effects before sampling status/settings. Assertions lock the returned authorize URL shape (`state=cline-browser-fixture`, preserved callback URL), callback success HTML, authenticated status/email/provider `hasToken`, masked provider access token, non-empty auto-added free models, TS-vs-Go enabled-model equality, and logout cleanup. Focused Go coverage `TestClineBrowserAuthFlowPersistsCredentialsAndAutoAddsFreeModels` verifies the same local callback-server token import, free-model auto-add, and context-window fill from cache. Verified with focused `go test ./internal/systemroutes -run 'TestClineBrowserAuthFlowPersistsCredentialsAndAutoAddsFreeModels|TestClineAuthCallbackPersistsCredentialsAndAutoAddsFreeModels|TestClineEnabledModelsStoresContextWindowsFromCache|TestClineProviderEndpointsExposeFrontendContractShape' -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline provider refresh positive parity with an offline OpenRouter fixture. TS hardcodes `https://openrouter.ai/api/v1/models`, so the harness now starts a local OpenRouter-shaped fixture and runs the TS backend with a Bun preload that redirects only that fetch (plus the Cline recommended-model URL) to the fixture; Go uses matching test-only URL overrides via `NARRAFORK_CLINE_OPENROUTER_MODELS_URL` / `NARRAFORK_CLINE_RECOMMENDED_MODELS_URL`. The fixture intentionally returns sorted/dedup-sensitive model data with a duplicate id and pricing/context metadata. This uncovered and fixed a drift: TS provider-specific Cline cache preserves duplicate upstream rows while aggregate `/api/cline/models` dedupes by id; Go previously deduped too early during refresh. Go now preserves provider-specific duplicates from refresh and relies on aggregate cache reads to dedupe, matching TS. The harness samples `POST /api/cline/providers/cline-cache-fixture/models/refresh`, provider-specific cached models, and aggregate cached models, asserting `{count:3,fromCache:false}` with no `models` in the refresh response, provider cache ids `cline/refresh-a,cline/refresh-a,cline/refresh-b`, aggregate ids `cline/refresh-a,cline/refresh-b`, and normalized context/pricing metadata. Focused Go coverage `TestClineProviderRefreshFetchesOpenRouterShapeAndCachesModels` locks the same behavior. Verified with focused `go test ./internal/systemroutes -run 'TestClineProviderRefreshFetchesOpenRouterShapeAndCachesModels|TestClineProviderRefreshResponseMatchesTSCountShape|TestClineBrowserAuthFlowPersistsCredentialsAndAutoAddsFreeModels|TestClineAuthCallbackPersistsCredentialsAndAutoAddsFreeModels|TestClineEnabledModelsStoresContextWindowsFromCache|TestClineProviderEndpointsExposeFrontendContractShape' -count=1`, `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline recommended/free model refresh parity with the same offline Cline fixture path. The TS-Go harness now samples `GET /api/cline/recommended-models?refresh=1` before any Cline auth side effects and then samples the cached `GET /api/cline/recommended-models`, asserting a fixture recommended model with description/tags plus two free models (`cline/free-fixture`, `cline/free-live-fixture`) for both TS and Go. This locks the TS behavior where the route fetches the external Cline recommended-model service on first access, while Go uses `refresh=1` to force the same fetch and then serves the persisted cache on the non-refresh read. Focused Go coverage `TestClineRecommendedModelsRefreshFetchesAndPersistsCache` verifies the refresh response and cache read remain frontend-shaped arrays with metadata preserved. Verified with focused Cline recommended/provider/browser tests and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline user-info refresh and balance positive flow with an offline account fixture. The TS-Go harness now redirects TS `https://api.cline.bot/api/v1/users/*` calls through the same local Cline fixture, samples post-callback status, explicit `POST /api/cline/user-info/refresh`, `GET /api/cline/balance`, and status-after-refresh, and asserts `email`, `displayName`, `userId`, and `balance` side effects match on both backends. Go now has `NARRAFORK_CLINE_API_BASE_URL` for account/auth endpoint overrides, uses the helper for auth browser/user-info/balance calls, and refreshes user-info during pasted and browser callback import so the TS background user-info side effect is visible in status/settings parity. Focused coverage `TestClineUserInfoRefreshAndBalanceUseAccountFixture` locks the local `/api/v1/users/me` plus `/api/v1/users/:id/balance` flow and `workos:` bearer header contract. Verified with focused Cline auth/user-info tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline expired-token refresh positive flow. The TS-Go harness now redirects TS `https://api.cline.bot/api/v1/auth/refresh` to the offline Cline fixture, imports an expired pasted callback credential, then samples `GET /api/cline/balance` and status afterward. The fixture only serves the refresh-user balance when the request uses the refreshed `Bearer workos:cline-refreshed-access-token`, so the sampled balance/status path proves both TS and Go performed token refresh before account balance. Focused Go coverage `TestClineExpiredCredentialsRefreshBeforeBalance` writes an expired credential file, verifies `/api/v1/auth/refresh` receives the TS-compatible `refreshToken`/`grantType` body, verifies balance uses the refreshed bearer token, and checks the new access/refresh tokens plus account metadata are persisted. Verified with focused Cline token/user-info tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Cline invalid-refresh credential cleanup safety. The TS-Go harness now imports an expired callback credential whose refresh token intentionally receives a fixture `401`, then samples `GET /api/cline/balance` and `/api/cline/status`; both backends must return the TS-safe balance `422` without clearing the NarraFork JWT and then report `authenticated:false`, proving credentials were cleared instead of leaving stale auth state. Focused Go coverage `TestClineInvalidRefreshClearsCredentialsAndKeepsBalance422` locks the refresh request body, 401 cleanup, credential-file removal, and 422 balance response. Verified with focused Cline invalid/valid refresh tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Codex settings/status route parity. The TS-Go contract harness now samples admin `/api/codex/status`, `/default-reasoning-effort`, missing-body `/use-websocket`, invalid `/use-websocket`, invalid `/load-balancing-mode` (`tiered_sticky`), invalid `/tier-order`, and valid duplicate tier-order normalization. This exposed Go accepting inputs TS rejects and rejecting one TS-defaultable input: missing `useWebSocket` now defaults to `true`, `tiered_sticky` is no longer accepted as load-balancing mode, and invalid tier values/max validation now fail before normalization. Focused `TestCodexSettingsMutationsMatchTSValidation` plus `go test ./internal/systemroutes -count=1` locks the Go validation edges; full `bun go_backend/testtools/ts-go-api-contract-diff.ts` remains `diffs: []`.
- 2026-05-29 — main narrator: narrowed Codex import validation parity for provider settings credential import. The TS-Go harness now samples admin `POST /api/codex/import` with an empty body and with credentials that contain no refresh/access token, asserting TS-compatible `400` errors (`No credentials provided` and `No valid credentials: each credential must have a non-empty refreshToken or accessToken`). This exposed a Go drift where invalid imports returned `200 {added:0,duplicates:0,skipped:n}`. Go now validates empty/invalid imports before returning success while preserving duplicate detection and existing access-token-only/Sub2Api import behavior. Focused coverage `TestCodexImportValidationMatchesTS` locks the 400 errors, and existing Codex import tests continue to cover valid access-token and text import paths. Verified with focused Codex import tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Codex credential missing-id mutation parity. The TS-Go harness now samples admin missing-credential disable/enable/reset/update/delete, refresh, usage, and batch-delete routes. This exposed Go's refresh/usage missing paths returning generic 404s while TS returns `400 {error:"Credential not found: missing"}`, and mutation missing paths returned generic `Codex credential not found` payloads instead of TS's id-specific error. Go now emits TS-compatible `{error:"Credential not found: <id>"}` with 404 for mutation/delete and 400 for refresh/usage; batch delete continues returning `{removed:[],notFound:[id]}`. Focused coverage `TestCodexCredentialMissingResponsesMatchTS` locks all missing-id statuses and payloads. Verified with focused Codex credential tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Codex credential update input validation parity. The TS-Go harness now seeds a deterministic offline `codex-contract` credential and samples `PATCH /api/codex/credentials/:id` with an empty body, malformed JSON, and malformed JSON for a missing id. This exposed Go returning parse `400`s before credential lookup, while TS catches parse failures as `{}` and proceeds. Go now treats empty/malformed update payloads as no-op update bodies for this route only, preserving the existing request-size guard and still returning the TS-compatible missing-id `404 {error:"Credential not found: missing"}`. Focused coverage `TestCodexCredentialUpdateEmptyAndInvalidBodyMatchesTS` locks the no-op update and missing-id behavior. Verified with focused Codex credential tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Codex persisted credential loading parity. The TS-Go harness now seeds a valid offline `codex-contract` credential plus an invalid no-token row and a duplicate-refresh row, then asserts `GET /api/codex/status` exposes only the valid first credential (`total=1`, `availableTotal=1`, `currentId="codex-contract"`). This exposed Go status/credential mutation loading raw persisted rows while TS filters rows without refresh/access tokens and skips duplicate dedupe keys during `CodexManager.loadCredentials()`. Go now normalizes Codex credential loads before status/mutation/import operations: no-token rows are dropped, duplicate dedupe keys are skipped, missing ids/priorities/disabled flags receive TS-style defaults, and snake-case token aliases are canonicalized. Focused coverage `TestCodexStatusFiltersInvalidAndDuplicatePersistedCredentials` locks the filter/default behavior; the pagination fixture now seeds token-bearing rows so it continues testing paging after TS-compatible filtering. Verified with focused Codex credential tests, Biome on the harness, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed workspace positive CRUD side-effect parity for the narrator split-workspace UI. The TS-Go contract harness now creates a workspace, reads it back, updates title/tree, verifies the updated row appears in the list, deletes it, and verifies the detail route returns 404 after deletion. This exposed a Go response-shape drift where workspace `createdAt`/`updatedAt` came back as millisecond numbers while TS serializes Drizzle `timestamp_ms` values as ISO strings; Go workspace SELECTs now format those timestamps as TS-compatible RFC3339 strings without changing stored integer values. Verified with `go test ./internal/misccompat -run 'TestRuler|TestWorkspace|TestHooks' -count=1`, `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed custom-subagents positive filesystem CRUD parity for the routines UI. The TS-Go contract harness now creates a custom subagent, reads it back, renames/updates it, verifies the old name returns 404 and the new definition appears in the list, deletes it, and verifies the detail route returns 404 after deletion. Semantic assertions lock `toolAccess`, `customTools`, `defaultModel`, prompt/description changes, rename side effects, and delete `{ok:true}`. No Go route change was needed because the filesystem-backed implementation already matched TS for this flow. Verified with focused `go test ./internal/misccompat -run TestCustomSubagentsUseFilesystemStorage -count=1`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed global skills positive CRUD/toggle parity for the routines UI. The TS-Go contract harness now creates a global skill, reads it back, rejects duplicate create, rejects toggle without boolean `enabled`, disables and re-enables the skill, updates its displayed name/content, verifies the old name returns 404 and the updated skill appears in the list, deletes it, and verifies missing update/delete/toggle and detail-after-delete return 404. This exposed Go unsafe-success drifts: create returned 200 and overwrote existing directories, update-missing created a new skill, delete-missing returned `{ok:true}`, and toggle missing `enabled` silently disabled the skill. Go now returns TS-compatible 201/400/404 statuses, preserves TS display-name vs sanitized-directory behavior, updates existing files in place, and requires explicit boolean toggle input. Verified with focused `TestGlobalSkillCRUDMatchesTSStatusAndSideEffects`, `go test ./internal/simplecontent -count=1`, `bunx @biomejs/biome check go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed project routine toggle parity for the project routines modal. The TS-Go contract harness now samples `GET /api/routines/project/:projectId`, invalid toggle action, missing routine, missing project, and the full terminal routine `enable → list → disable → list → reset → list` flow. Semantic assertions lock `enabled`, `override`, and `globalEnabled` transitions (`global/false`, `enabled/true`, `disabled/false`, then back to `global/false`) plus `{ok:true}` mutation responses. No Go route change was needed because the existing project-routine handler already matched TS for tool-routine overrides. Verified with focused `TestProjectRoutineStatusIncludesOverrideAndGlobalEnabled`, `bunx @biomejs/biome check --write go_backend/testtools/ts-go-api-contract-diff.ts`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed hooks positive CRUD/update parity for the routines hooks tab. The TS-Go contract harness now creates a command hook, reads it back, updates it to an HTTP hook with empty matcher, headers, shorter timeout, and disabled state, verifies list/all include the updated hook, rejects clearing the active HTTP URL, checks missing update returns 404 before body validation, deletes the hook, and verifies detail-after-delete returns 404. This exposed Go update-path drifts: update decoded/validated before missing-row checks, accepted invalid enum/type/timeout/body shapes, could not set empty string fields like `matcher`, ignored `headers`, and could not explicitly clear nullable fields. Go hook update now loads the existing hook first, applies TS-compatible update validation/context checks, persists headers and explicit null/empty-string values, and returns TS-compatible 400/404 boundaries for the sampled safe cases. Focused `TestHookPositiveCRUDAndUpdateValidationMatchTS` locks the Go side; the harness remains `diffs: []`. Note: TS currently returns 500 for some raw Zod refine failures such as create-missing-command and type-change-without-payload, so those TS-500 cases are intentionally not frozen into the Go parity harness; Go keeps safer 400 validation for them.
- 2026-05-29 — main narrator: narrowed positive stdio MCP server connection/tool-injection parity for the routines MCP tab and tool-permissions UI. The TS-Go contract harness now writes a local stdio JSON-RPC helper, samples positive `POST /api/mcp/servers/test`, creates an enabled stdio server, verifies subsequent server list shows `status:"connected"` with discovered `read_file` tools, verifies `/api/mcp/tools` exposes the tool with TS-compatible `source:"external"`, `serverName`, and `serverId`, then deletes the server. This exposed Go runtime side-effect drifts: create/update/import of enabled stdio servers only persisted settings and did not probe/connect, `/api/mcp/tools` used the server name as `source` instead of TS's `external`, and manually connected disabled stdio servers were visible in the UI but not injected into the agent tool inventory. Go now probes enabled stdio servers on create/update/import, clears runtime state on explicit disable, keeps manually connected disabled stdio tools injectable like TS, and preserves bounded subprocess stdout/stderr/tool-output limits. Verified with focused `TestCreateEnabledStdioServerConnectsAndListsExternalTools`, `TestManualConnectDisabledStdioServerStillInjectsLikeTS`, full `go test ./internal/mcpcompat -count=1`, and full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`).
- 2026-05-29 — main narrator: narrowed Kiro MCP populated bridge parity for the routines MCP tab. Focused Go coverage now stubs a successful upstream Kiro MCP `tools/list` and `tools/call` response, verifies `GET /api/kiro/mcp-tools` preserves JSON-RPC `result.tools` while also mirroring top-level `tools` for the current frontend `kiroData?.tools` consumer, and verifies `POST /api/kiro/mcp-search` returns the raw JSON-RPC tool-call result. This exposed Go request-envelope/header drifts against the TS `KiroMcpClient`: MCP calls used credential `region` instead of TS's config `apiRegion ?? region`, omitted `amz-sdk-invocation-id` / `amz-sdk-request`, and sent the Go OS as both OS and OS-version instead of splitting `systemVersion`. Go now follows the TS region/header/system-version contract, keeps profileArn injection, and retains fallback `reason/error/message` diagnostics for unconfigured Kiro. Verified with focused `TestKiroMcpPopulatedBridgeMatchesFrontendShapeAndTSHeaders`, existing fallback tests, and full `go test ./internal/systemroutes -run 'TestKiroMcp' -count=1`. Live AWS/Kiro E2E remains out of the TS-Go harness because the contract harness has no real Kiro plugin/credential backend.
- 2026-05-27 — main narrator: narrowed provider route data-safety and response-shape parity. Go now mirrors TS's Cline startup/module-load behavior by creating the default Cline provider during provider-route registration, while Cline read endpoints (`GET /cline/status`, `/cline/models`, `/cline/pool/search`, `/cline/pool/count`) no longer call `ensureDefaultClineProvider` per request and therefore do not create/replace providers while serving reads. Go's provider-specific Cline refresh response now matches TS's `{count, fromCache}` shape, and the frontend API type was corrected from the stale `{models, fromCache}` declaration. The unauthenticated Cline auth-state contract is also locked: `POST /cline/user-info/refresh` returns 200 `{authenticated:false}` while `GET /cline/balance` returns TS-safe 422 instead of 401 so the frontend does not clear the NarraFork JWT. Generic missing provider errors now include the provider id (`Provider "id" not found`) like TS provider routes. Focused coverage `TestClineReadEndpointsDoNotMutateDefaultProvider` verifies route registration creates the default provider once and read endpoints leave it unchanged; `TestClineProviderRefreshResponseMatchesTSCountShape` locks the TS provider-refresh shape; `TestClineUnauthenticatedUserInfoAndBalanceMatchTSStatus` locks unauthenticated auth-state statuses; `TestProviderMissingResponseIncludesProviderID` locks missing-provider error text.
- 2026-05-27 — main narrator: narrowed user-preferences/recent-tabs API parity. The TS-Go contract harness now samples `GET /api/user-preferences`, a valid `PUT /api/user-preferences/recent-tabs`, and a malformed recent-tab upsert missing `lastVisitedAt`; Go now preserves TS's `graphViewports` raw JSON-string response shape, parses it only for save operations, validates recent-tab payloads against the TS Zod-required fields/enums, strips unknown/upsert-only fields before persistence, and the full sampled contract remains `diffs: []`. Focused coverage `TestRecentTabUpsertValidatesTSShapeAndStripsUnknownFields` plus `go test ./internal/userprefs` locks the behavior.
- 2026-05-29 — main narrator: narrowed recent-tabs user-scoped WS snapshot parity. Go `/api/user-preferences/recent-tabs` mutations now broadcast TS-shaped `user:recent_tabs_snapshot` payloads (`tabs`, `revision`) to the authenticated user's narrator WebSocket connections via `Hub.BroadcastUser`, including `updateOnly` no-op paths, while keeping JSON responses as arrays. Focused coverage `TestRecentTabMutationsBroadcastUserScopedSnapshot` locks handler payload/user id semantics and `TestBroadcastUserSendsOnlyToMatchingNarratorConnections` locks Hub user-scope fanout; verified with `go test ./internal/userprefs -count=1`, focused `go test ./internal/ws`, and app router smoke.
- 2026-05-27 — main narrator: narrowed sidecar model-context parity. Go agent history now formats hydrated sidecars with TS-compatible `<side_car source="...">` blocks, appends `tool_result` sidecars to model-visible tool outputs, and emits `user_message` sidecars as follow-up user context after assistant turns. Go agent events now also accept `SideCars`, include them in `tool_completed`/`sidecars` broadcasts, and persist `tool_result` / standalone `user_message` sidecars into `narrator_sidecars` when the table exists. Go runtime also has sidecar collector paths for provider-boundary `tool_result` and `after_tools` `user_message` injection; TODO reminder sidecars enter the same active loop's fourth completed non-`TaskCreate` tool result, silent-progress sidecars inject after a configurable run of tool calls without visible text, relaxed-plan sidecars remind on non-read-only tools while `plan_mode && relaxed_plan` with a lightweight Go shell write classifier covering common TS `analyzeShellCommand(...).hasWriteOperation` cases (redirection while ignoring `[[ ... > ... ]]` / `(( ... > ... ))` comparison operators, mutating path commands, sed/find/xargs/git/package-runner/package-manager writes), priority buffered messages including command text, image blocks, and text-file attachment hints can be consumed as `buffered_user` after-tools sidecars in the same active loop while non-priority buffers wait for run completion, and the busy-narrator REST buffer path now preserves raw slash-command `command_text` plus persists multipart text attachments under `~/.narrafork/buffered-files` before materializing them back into the worktree without duplicating `<attached_files>` hints when consumed. Go hook execution now emits a TS-compatible `hook:executed` telemetry broadcast with `hookId`, `event`, `hookType`, `outcome`, `narratorId`, and `durationMs` for both blocking `PreToolUse` and async `PostToolUse` hooks, and focused tests lock HTTP hook custom headers plus timeout fail-open telemetry. Active target TeamStatus send/broadcast messages can be delivered through an in-memory inbox and drained as `team_message` after-tools sidecars, completed background agent tasks are queued and drained as `bg_agent` after-tools sidecars, and `Bash(run_in_background=true)` now creates awaitable `background_tasks` rows, broadcasts live `background_task_output` byte counts while stdout/stderr grow, drains completion notices as `bg_bash` after-tools sidecars, and the cancel route cancels the registered background Bash process tree instead of only flipping DB status: Unix commands run in their own process group and Windows falls back to `taskkill /T /F` plus process kill. External goal-list changes while a narrator run is active are drained as one-shot `goal_update` after-tools sidecars, and after-tools sidecars are emitted as `sidecars` events plus next-turn user context for adapters that support user text with tool results. Go event persistence now also resets assistant draft state on `turn_complete`, so a tool-use assistant message and the final answer assistant message are stored as separate rows across multi-turn tool loops. Focused coverage `TestAgentHistoryIncludesStoredSideCarsInModelContext`, `TestAgentEventsPersistAndBroadcastSideCars`, `TestRunAgentLoopCollectsToolResultSideCarsForModelAndEvents`, `TestRunAgentLoopInjectsSilentProgressSideCar`, `TestRunAgentLoopCollectsAfterToolsUserSideCars`, `TestAgentSideCarCollectorInjectsRelaxedPlanReminder`, `TestRunBasicAgentInjectsTodoReminderSideCarIntoNextToolTurn`, `TestRunBasicAgentInjectsPriorityBufferedMessageAsAfterToolsSideCar`, `TestAgentSideCarCollectorDoesNotConsumeNonPriorityBufferAfterTools`, `TestPostMessageBuffersExpandedSlashCommandWithRawCommandText`, `TestPostMessageBuffersMultipartTextFileWithoutDuplicatingAttachmentHint`, `TestAgentHookExecutionBroadcastsTSCompatibleTelemetry`, `TestAgentHTTPHookHonorsHeadersAndTimeoutTelemetry`, `TestAgentSideCarCollectorDrainsTeamMessageSideCar`, `TestAgentSideCarCollectorDrainsBackgroundCompletionSideCar`, `TestBackgroundBashRunInBackgroundProducesAwaitableSideCar`, `TestBackgroundBashCancelStopsRunningCommand`, `TestBackgroundBashBroadcastsLiveOutputBytes`, `TestAgentSideCarCollectorDrainsGoalUpdateSideCar`, `TestShellCommandLooksWriteLikeMatchesCommonTSCases`, and `TestRunBasicAgentPersistsSeparateAssistantMessagesAcrossToolTurns` verifies DB-backed hydration/history injection, event persistence/broadcast, provider-boundary tool-result injection, silent-progress injection, after-tools user sidecar injection, relaxed-plan reminder gating, TODO reminder production, priority buffered-user sidecar consumption including command/attachment metadata plus non-priority wait-until-run-complete behavior, busy REST slash-command queueing, hook execution telemetry/header/timeout edges, team-message sidecar draining, background-completion sidecar draining, background Bash await/sidecar/cancel/live-output production, goal-update sidecar draining, common TS shell-write classification cases including comparison-vs-redirection edge cases, and multi-turn assistant message separation.
- 2026-06-06 — main narrator: launched five read-only `nug2:codex:gpt-5.5` agents (`gap-api`, `gap-db`, `gap-services`, `gap-agent`, `gap-contracts`) to refresh the Go-vs-TS backend gap inventory. Local spot-checks confirmed several active gaps/fallbacks around DB bootstrap/FTS ambiguity, Kiro chat SSE, settings validation/migration, storage cleanup, MCP transports, tool runtime partials, terminal raw replay, and agent event-stream parity. The detailed audit is appended in `## 2026-06-06 Codex Multi-Agent Gap Audit`.
- 2026-06-06 — DB transition implementation: SQLite `ts-owned-db-compatibility` is now documented as a **transition** stage, not the end-state architecture. Current SQLite main-schema and repair ownership remains TypeScript/Drizzle + TS FTS setup with Go read-only verification; the target state is `pure-go-db-management`, where Go owns main schema migrations, startup backfills/repair, FTS/search lifecycle, and schema drift release gates. Go's default SQLite mode is now `read-only-ts-owned`: missing TS DB fails without creating `.narrafork`/`narrafork.db`, default startup no longer bootstraps Drizzle/FTS, and FTS drift is reported as `ErrCanonicalSchemaMismatch` without repair. Postgres capability is explicitly Go-owned/experimental with `tsParityBaseline:false`, so it is not confused with TS SQLite parity.
- 2026-06-06 — parity hardening wave outside DB: five `nug2:codex:gpt-5.5` implementation agents landed bounded fixes for J03/J04/J06/J07/J08/J09/J14. Go settings load now migrates high-value legacy TypeScript shapes before saving; Kiro chat has a frontend capability guard, Kiro MCP tools use route capability, MCP import skips unsupported non-stdio transports, Codex usage-queue clear returns `FEATURE_DISABLED` instead of a false success, tool inventory exposes a machine-readable degraded capability contract, narrator event transcript golden tests cover text/reasoning/tool/permission flows, and review creation rollback now removes partial DB/Git artifacts when narrator initialization fails. Remaining gaps stay explicit: full TS Zod parity, Kiro chat SSE, non-stdio MCP runtimes, complete agent loop hook parity, review auto-start session parity, and full-narrator AI conflict resolve.

## Replacement Readiness Summary

Status: **Unsafe / not replacement-ready**.

Readiness levels:

- **Unsafe to replace TS backend:** any P0 gap remains unresolved or unaccepted.
- **Compatibility beta:** all P0 gaps fixed or explicitly disabled with UI fallback; major P1 gaps documented.
- **Replacement candidate:** all P0 fixed, critical P1 fixed, smoke checklist passes, DB compatibility guard exists.

Current readiness judgment: **Unsafe to replace TS backend**. Every track now has verified/done evidence, and unresolved P0 blockers remain across API security/setup guards, DB/FTS/bootstrap, domain lifecycle semantics, narrator safety/runtime semantics, runtime/container/terminal/snapshot side effects, and frontend-visible contracts. Go may be a scoped compatibility backend only after the listed P0s are fixed or explicitly disabled with frontend-visible fallbacks and smoke coverage.

## TS-to-Go Backend Migration Readiness Matrix

Last synthesized: 2026-05-18 by main narrator from fresh miao:gpt-5.5 track reviews and local Go verification.
Verification snapshot: `go test ./...` passed from `go_backend` on 2026-05-18.

Status values:

- `Blocked` — cannot claim Go replacement readiness until the P0 gate is fixed or accepted as disabled-by-design.
- `Partial` — Go has broad implementation, but P1/P2 gaps or contract drift risks remain.
- `Ready-gated` — implementation may exist, but replacement readiness requires fixture/smoke evidence.
- `Deferred` — not required for compatibility beta if explicitly disabled and hidden from unsupported UI flows.

| Area | Current readiness | P0 gate | P1 gaps | P2 / follow-up | Acceptance gate | Owner goals |
|---|---|---|---|---|---|---|
| Overall readiness / cutover decision | Blocked | No single matrix yet proves P0 closure across API, DB, Agent, runtime, and release. | P1/P2 prioritization must be tied to user-visible flows. | Keep this matrix updated as goals close. | All rows have status, evidence, priority, and an explicit decision: fix, fallback, disabled-by-design, or defer. | Goal 1 |
| DB / schema / FTS | Blocked for standalone replacement; partial for TS-migrated compatibility mode | Go is not main schema owner and must not be advertised as standalone DB bootstrap; FTS/schema mismatch must fail safely or degrade with diagnostics. | canonical schema coverage, migration journal classification, TS repair instructions, old-data/backfill detection. | centralized enum/string constants, index coverage, project backup/import scope. | TS-created latest DB starts Go; old/missing schema is blocked with actionable errors; FTS runtime failures degrade safely; Go first-use does not prevent TS rollback. | Goals 2, 3 |
| API / frontend contract | Ready-gated | Any auth-boundary mismatch, misleading fallback success, or missing Git guard on Git-dependent flows blocks replacement. | endpoint-level response parity for provider routes, storage/update/gateway/benchmark fallbacks, chapter/narrator JSON/SSE surfaces. | additive Go-only routes and health/version differences documented. | Contract matrix maps each frontend-used endpoint to TS and Go behavior; P0 JSON/SSE/WS diffs are zero or accepted disabled-by-design. | Goal 4 |
| Narrator / Agent runtime | Blocked for replacement; partial for beta | MCP persistent runtimes/non-stdio transports and Codex stateful recovery can still block agent-heavy workflows unless disabled in UI. Browser sessions, Go subagent detach/reattach parent-blocking, and rollback/edit-regenerate now have targeted parity, subject to documented runtime boundaries; frontend narrator runtime/tool helpers now require explicit capability metadata for partial-Go payloads. | provider-specific history replay, optional tools parity, permission edge cases, output bounds, rollback/edit TS-vs-Go fixture breadth, and stale state recovery. | richer usage/cost telemetry and native provider edge parity. | Agent parity tests cover MCP, Browser, subagent detach/reattach, rollback/edit-regenerate, Codex quota/failover, permission pause/resume, and WS hydration. | Goals 5, 6, 7, 8, 9 |
| Chapter / domain lifecycle | Blocked for full parity | Go chapter split-at-commit is active with route/capability and Go fixture semantic proof for prefix/continuation/new-fork plus compressed summary fallback; full replacement still needs broader TS-vs-Go lifecycle proof because TS has no matching split route surface. Graph/ruler/narrator ref edge semantics remain not replacement-equivalent until proven or hidden. | merge/rebase/review/ruler edge cases need TS-vs-Go fixture comparison. | additive Go chapter routes documented. | split tests cover prefix/continuation/new fork, trunk special case, chapter edges, narrator refs, invalid commit, failure compensation, route JSON semantics, compressed fallback metadata, and frontend capability consumption of partial split fallbacks. | Goal 10 |
| Runtime / system integration | Blocked for full parity; partial for compatibility beta with UI fallbacks | Container lifecycle, VNet, Windows terminal, update apply, gateway persistent runtimes, and benchmark container execution must either be implemented or disabled before mutation with visible fallback. | per-chapter container locks, bounded streaming logs, platform-specific terminal behavior, relay-only VNet degradation, self-update rollback. | xterm serialized replay parity, advanced container proxy/browser auto-enable. | capability metadata, route fallback, frontend disabled state, and smoke tests agree for every unsupported runtime; implemented runtime paths have resource limits and cleanup tests. | Goals 11, 12, 13 |
| Engineering / CI / release switch | Blocked for default cutover; parallel beta channel added | Default `dev`, `start`, and `release` must not switch to Go until P0 matrix gates and rollback drill pass. Go beta must remain opt-in and TS legacy must remain available. | `dev:go`, `test:go`, `build:go`, `release:go:beta`, TS-Go contract CI, artifact naming, rollback drill. | eventual TS legacy deprecation plan after beta proves safe. | Go beta artifact is built in CI, has backend/version/build-channel metadata, uploads metadata+patch only, passes key path smoke, and can be rolled back to TS with the same DB. | Goal 14 |

### Prioritized Execution Queue

1. **Close Goal 1 and Goal 4 first:** establish the durable matrix plus JSON/SSE/WS contract diff harness, because every later claim depends on it.
2. **Close Goals 2 and 3 before any replacement claim:** DB compatibility and TS rollback safety are the hard floor for all release decisions.
3. **Close Goals 5 through 9 for Agent-heavy beta:** MCP, Browser, subagent, rollback/edit-regenerate, and Codex parity determine whether real narrator workflows survive migration; rollback/edit-regenerate now has focused Go coverage but still needs broader TS-vs-Go fixture comparison before replacement readiness.
4. **Close Goals 10 through 13 for runtime parity:** chapter split, containers, VNet/terminal, update/gateway/benchmark remove the largest user-visible disabled/runtime gaps.
5. **Close Goal 14 last:** only after P0 gates are fixed or accepted as disabled-by-design should Go artifacts move from parallel beta to default release.

### Matrix Maintenance Rules

- Any new Go backend feature must update this matrix if it changes a row's readiness state, P0/P1/P2 classification, or acceptance gate.
- A row may move from `Blocked` to `Ready-gated` only with concrete tests or smoke evidence named in that row's track section.
- A P0 may be accepted without implementation only through the explicit disabled-by-design contract below.
- Default cutover remains forbidden while any matrix row is `Blocked` without an accepted fallback decision.

### Explicit Disable / Fallback Contract

A P0 feature may be accepted as `DISABLED-BY-DESIGN` only when all of the following are true:

1. **API refuses before mutation:** unsupported calls return a stable 4xx/501-style response before any DB, git, filesystem, process, container, or network side effect. Partial success is not allowed.
2. **Stable response shape:** the response includes machine-readable status such as `{ ok:false, code:"FEATURE_DISABLED", feature, reason }` or the existing TS-compatible error shape for that route. It must not look like a successful TS operation.
3. **Capability/readiness exposed:** `/api/health`, readiness/capability metadata, or the relevant resource payload exposes the disabled capability so clients can decide before invoking the action.
4. **Frontend-visible disabled state:** the UI hides or disables the action and explains the unsupported/degraded state; it must not offer a button that later reports misleading success.
5. **Smoke evidence:** smoke artifacts prove no 5xx, no misleading success, no unauthorized mutation, and no side effects for the disabled flow.

### TS Baseline / Compatibility Definition

`TS-compatible` means the same fixture is executed once against the TypeScript backend and once against the Go backend, then compared at the observable boundaries:

- HTTP status, content type, required response fields, and error shape/code.
- SSE/WS control and event names, required payload fields, reconnect behavior, and close/error semantics.
- DB rows created/updated/deleted for the relevant canonical tables.
- Git/worktree/filesystem/container/terminal side effects visible to users.
- Frontend observable state: rendered disabled/success/error state, toasts, uncaught exceptions, and route stability.

Extra fields are allowed if the frontend and existing API consumers ignore them; missing required TS fields, incompatible error codes, misleading success payloads, or extra mutations are not compatible.

## Track: API Contract Parity

Owner: spec-api
Status: Verified
Last update: 2026-05-14

### Scope

- Compare `server/app.ts`, `server/routes/*.ts` with `go_backend/internal/app/router.go` and
  `go_backend/internal/*/handlers.go`.
- Track missing routes, stub/degraded routes, auth/public boundary mismatches, and response shape
  mismatches. This section records API contract gaps only; deeper behavior belongs to domain,
  narrator, runtime, DB, and frontend tracks.

### Findings

- The Go router covers the main public routes and most TS route groups: health/auth/status,
  dependencies, shares, changelog, projects, chapters, graph/ruler/project backup, narrators,
  terminals, settings, fs, search, chapter edges, favorites, learning, routines, skills,
  workspaces, hooks, custom subagents, uploads, storage, notifications, reviews, MCP, admin,
  update, runtime, usage history, gateway, VNet, benchmarks, provider routes, and WebSockets.
  Evidence: `server/app.ts:95-153`, `server/app.ts:205-253`,
  `go_backend/internal/app/router.go:62-143`.
- Several Go routes are intentionally compatible-but-degraded rather than full TS equivalents.
  Examples include gateway/VNet/update self-apply, benchmark runner execution, Kiro chat SSE,
  storage cleanup, review fallbacks, and ruler compatibility routes with partial/degraded lifecycle
  semantics. Evidence: `go_backend/internal/systemroutes/handlers.go:501-584`,
  `go_backend/internal/systemroutes/handlers.go:648-790`,
  `go_backend/internal/systemroutes/handlers.go:1013-1024`,
  `go_backend/internal/misccompat/handlers.go:658-680`,
  `go_backend/internal/app/router.go:98-103`,
  `go_backend/internal/misccompat/handlers.go:2046-2054`.
- Go includes API routes that are not mounted by the current TS app (`/api/exploration-groups`,
  chapter batch-fork/split/cherry-pick/dependency-status/sync-upstream). Treat these as additive
  unless frontend starts relying on them under TS parity. Evidence:
  `go_backend/internal/app/router.go:115`, `go_backend/internal/chapters/handlers.go:41-46`;
  no matching mounts in `server/app.ts:205-253` or lines found in `server/routes/chapters.ts:44-493`.
- Go now implements the TS Git-unavailable request gate: `gitRequiredGuard` selectively returns
  `503` with `code: "GIT_NOT_INSTALLED"` for Git-dependent routes and avoids over-blocking setup,
  settings, provider, search, and standalone narrator routes. Evidence: `server/app.ts:155-203`,
  `go_backend/internal/app/router.go:209-233`, `go_backend/internal/app/route_contract_test.go:284`.
- Public/auth boundary mostly matches TS for health/auth/status/dependencies/changelog/shares and
  authenticated API groups. The older notification-sounds mutation mismatch is obsolete: TS mounts
  `/api/notification-sounds` after `requireAuth`, and current Go registers notification-sounds inside
  the authenticated API group; `TestNotificationSoundRoutesRequireAuth` locks unauthenticated
  POST/GET/DELETE to 401. Evidence: `server/app.ts:138-153`, `server/app.ts:220`,
  `server/routes/notification-sounds.ts:55-111`, `go_backend/internal/app/router.go:84-90`,
  `go_backend/internal/app/router.go:158-172`, `go_backend/internal/app/router_test.go:240`.
- The TS route group `/api/project-db` is mounted only under `/api/projects` for backup/import
  plus separately documented route file; Go mounts a small `/api/project-db` compatibility status
  group and also puts project backup/import under `/api/projects`. This is likely additive but
  should be kept documented. Evidence: `server/app.ts:249-250`,
  `server/routes/project-db.ts:17-46`, `go_backend/internal/app/router.go:136`,
  `go_backend/internal/systemroutes/handlers.go:118-125`,
  `go_backend/internal/projects/handlers.go:21-23`.

### P0

- **Resolved — Notification-sound mutation auth boundary now matches TS.** Current TS and Go both mount
  `/api/notification-sounds` under authenticated API routing. Keep `AUTH-NS-01` as a smoke row because
  the handler would fall back to an anonymous user if accidentally mounted publicly, but this is no longer
  an open P0 implementation blocker. Evidence: `server/app.ts:152-153`, `server/app.ts:220`,
  `server/routes/notification-sounds.ts:55-111`, `go_backend/internal/app/router.go:158-172`,
  `go_backend/internal/app/router_test.go:240`.
- **Resolved — Git-unavailable API contract guard matches TS shape.** TS returns a stable 503 payload with
  `code: "GIT_NOT_INSTALLED"` only for known Git-dependent routes when Git is absent. Current Go has the
  equivalent router-level guard and focused tests for guarded chapter/git routes plus non-Git over-block
  checks. Keep `API-GIT-01` as smoke coverage for Git-present/Git-missing fixtures. Evidence:
  `server/app.ts:155-203`, `go_backend/internal/app/router.go:209-233`,
  `go_backend/internal/app/route_contract_test.go:284`.

### P1

- **P1 — `/api/cline` auth/provider endpoint parity is narrowed but still partial.** Go registers the
  Cline route group and the TS-Go harness now covers status/balance plus safe cached/error-state
  routes for models, provider-cache miss, provider refresh miss, populated pool search/count,
  invalid enabled models, enabled-model add against a deterministic local cache, settings
  persisted-provider view, pasted OAuth callback credential import/free-model/user-info side effects,
  browser auth start plus localhost callback-server token import/free-model/user-info side effects,
  explicit user-info refresh, expired-token refresh, invalid-refresh cleanup, and balance flows against
  a local account fixture, offline fixture-backed positive provider refresh/cache persistence against the OpenRouter model shape,
  recommended/free model refresh/cache persistence against the Cline service shape, auth
  cancel/logout, and invalid callback. Remaining compatibility proof is mostly live upstream edge
  cases beyond the local fixture-backed credential/model paths. Evidence: `server/app.ts:225`,
  `server/routes/cline.ts:408-684`, `go_backend/internal/systemroutes/handlers.go:2097-2114`,
  `go_backend/testtools/ts-go-api-contract-diff.ts`.
- **P1 — `/api/nug` exact contract is narrowed but still partial.** Go registers NUG provider
  paths and the public OAuth callback, and the TS-Go harness now covers positive model refresh/cache,
  quota/cache, channels health, usage, usage-summary normalization, provider login, API-key creation,
  and OAuth start/callback success against local NUG fixtures. Login/API-key responses are locked to
  TS-compatible no implicit settings persistence; OAuth callback is locked to the TS persisted
  credential path with masked settings reads. Remaining unproven NUG parity is live upstream edge
  cases beyond the local fixture. Evidence: `server/app.ts:149-150`, `server/app.ts:228`,
  `server/routes/nug.ts:245-559`, `go_backend/internal/app/router.go:75`,
  `go_backend/internal/systemroutes/handlers.go:855-865`.
- **P1 — Degraded/stub-compatible responses may satisfy path existence but not TS behavior.**
  Gateway, update apply, benchmark execution, Kiro chat, and storage cleanup return fallback
  or unavailable payloads in Go where TS has real runtimes. Kiro chat is now explicitly capability-gated
  as `providers.kiro.routes.chat=false` in Go health, matching its `FEATURE_DISABLED` handler.
  VNet is tracked in the Runtime P0 WebSocket row as an active Go relay-only runtime with UDP rendezvous
  disabled; API consumers must tolerate degraded/fallback payloads or the routes need full
  implementations. Evidence:
  `server/routes/gateway.ts:30-226`, `server/routes/vnet.ts:7`,
  `server/routes/update.ts:25-143`, `server/routes/benchmarks.ts:111-204`,
  `server/routes/kiro.ts:263-315`, `server/routes/storage.ts:13-155`,
  `go_backend/internal/systemroutes/handlers.go:501-584`,
  `go_backend/internal/systemroutes/handlers.go:648-790`,
  `go_backend/internal/systemroutes/handlers.go:1013-1024`,
  `go_backend/internal/misccompat/handlers.go:614-680`.
- **P1 — Chapter/narrator surface differs beyond path matching.** Go has many matching endpoints
  and some additive chapter endpoints, but response schemas and streaming/SSE semantics need
  domain/narrator verification for messages, compact, plan mode, fork/merge, review, container,
  and commit-file routes. Evidence: `server/routes/chapters.ts:44-493`,
  `server/routes/narrators.ts:261-3290`, `go_backend/internal/chapters/handlers.go:29-66`,
  `go_backend/internal/narrators/handlers.go:32-142`.

### P2

- **P2 — Additive Go routes should be documented as Go-only compatibility extensions.** Current
  Go adds `/api/exploration-groups`, chapter batch-fork/split/cherry-pick/dependency-status/
  sync-upstream, and `/api/project-db` status helpers without TS mounts. Evidence:
  `go_backend/internal/app/router.go:115`, `go_backend/internal/chapters/handlers.go:41-46`,
  `go_backend/internal/systemroutes/handlers.go:118-125`.
- **P2 — Error body consistency should be spot-tested.** TS global errors use `{ error, code }`
  for `AppError` and `{ error }` for unknown errors; Go's `httpx` helpers appear similar but route
  handlers sometimes write ad-hoc `{ok:false}`, `{success:false}`, or fallback payloads. Evidence:
  `server/app.ts:255-262`, `go_backend/internal/app/router.go:145-148`,
  `go_backend/internal/systemroutes/handlers.go:349-360`,
  `go_backend/internal/systemroutes/handlers.go:735-790`.
- **P2 — Health/version metadata differs.** TS reports `APP_VERSION` and `GIT_COMMIT`; Go reports
  `go-backend-dev` and empty commit unless env/config fills them. This is acceptable for a
  compatibility backend if frontend only displays it, but should be documented. Evidence:
  `server/app.ts:100-111`, `go_backend/internal/app/router.go:156-176`.

### Frontend-driven TS-vs-Go Contract Matrix

This matrix is the Goal 4 gate: each row must have either TS-vs-Go diff evidence, a stable Go
fallback, or an explicit disabled-by-design decision before Go can be promoted beyond compatibility
beta. It is derived from `frontend/lib/api/*.ts`, frontend hooks/components, Go route contract tests,
and three 2026-05-18 `miao:gpt-5.5` audits of core UI, realtime, and provider/browser surfaces.

Verification snapshot on 2026-05-18:

- `go test ./internal/app -run 'TestFrontendContractSmokeHTTPPaths|TestPhase0HealthCapabilitiesExposeKnownGoFallbacks|TestH02GitMissingGuardReturnsTSShapeOnlyForGitRoutes' -count=1`
- `go test ./internal/ws -run 'TestFrontendUseNarratorWSContractSubscribeCatchupPresenceAndEvents|TestFrontendUseTerminalWSContractSubscribeInputResizeAndFanout' -count=1`
- `go test ./internal/systemroutes -run 'TestUpdateDownloadWithoutURLReturnsTerminalErrorSSE|TestUpdateDownloadStreamsProgressAndCompleteSSE|TestUpdateDownloadRejectsSHA512Mismatch|TestUpdateVersionExposesManualOnlyContract|TestUpdateDirectoryCleanupAndApplyExposeUnsupportedContract' -count=1`
- `bun go_backend/testtools/ts-go-api-contract-diff.ts` starts the TS backend, registers an admin, samples JSON/SSE/WS, stops TS, starts a compiled Go backend against the same HOME/DB/token, samples the same surfaces, and emits a compact diff report.

Latest executable TS-vs-Go diff result from `bun go_backend/testtools/ts-go-api-contract-diff.ts`:

- Current sampled contract pass is clean: after switching the harness to key-order-insensitive TS-required shape coverage plus safety-critical value/capability assertions (for example `success:false`, `vacuumRan:false`, unauthenticated 422s, cached-empty states, Go runtime cleanup `errors:[]`, Go health storage cleanup fallback metadata, unauthenticated WS rejection, and SSE terminal events), `bun go_backend/testtools/ts-go-api-contract-diff.ts` reports `diffs: []` for the sampled JSON/SSE/WS surfaces. The sampled JSON set now includes TS-created search fixture probes for CJK trigram and short-query LIKE behavior, no-token auth-boundary probes for core protected routes (`/api/projects`, `/api/settings`, `/api/storage/cached`, `/api/runtime/scan`, `/api/benchmarks/suites`), regular-user admin-boundary probes for update, benchmark, runtime, and storage surfaces, benchmark empty/error probes (`GET /api/benchmarks/suites`, `/runs`, `/runs/missing/results`, `/runs/missing/summary`, and `/compare` without `runIds`), provider empty-state/status probes for `GET /api/cline/status`, `GET /api/cline/balance`, `GET /api/nkp/providers/missing/quota`, NUG missing-provider quota/usage/usage-summary/channel-health routes, regular-user provider boundary probes for OpenAI positive refresh/cache, Anthropic positive refresh through the `/v1` fallback with `resolvedBaseUrl`, NKP positive refresh/cache with `model_id` normalization, NUG positive model refresh/cache plus quota/cache/channels/usage/summary normalization, NUG login/API-key positive credential responses plus no implicit settings persistence after login, NUG OAuth start/callback positive redirect and persisted masked-settings side effects, OpenAI/Anthropic/NKP/NUG missing refresh plus NUG login/API-key/OAuth-start missing states, Codex `quota-overview`, regular-user 403 for Codex/Cline/Kiro status, Cline safe-route and populated-cache probes for models/provider-cache/pool/provider-refresh-cache persistence/recommended-free-model refresh-cache persistence/enabled-models/context-window side effects/settings-persisted-provider-view/pasted callback auth/user-info side effects/browser auth start plus localhost callback-server user-info side effects/explicit user-info refresh/expired-token refresh/invalid-refresh cleanup/balance/auth cleanup/callback validation, and Codex status/default-reasoning/use-websocket/load-balancing/tier-order/import validation probes, runtime admin probes for `GET /api/runtime/cached`, `GET /api/runtime/scan`, and `POST /api/runtime/cleanup` with `target=terminals`, storage admin probes for `GET /api/storage/cached` and `POST /api/storage/database/vacuum` to lock the shared request-path VACUUM-disabled safety contract, and update UI probes (`POST /api/update/apply`, `POST /api/update/cleanup`, `GET /api/update/directory`, `GET /api/update/status`, `GET /api/update/version`, trusted-server positive `GET /api/update/check`, and trusted download-error SSE) to lock no-prepared-update/manual apply, status empty states, positive release metadata, and `progress → error` terminal download streams, in addition to health/auth/projects/settings/search/update-check. The sampled SSE set now includes `GET /api/storage/scan`, verifying the TS-compatible `progress/category/.../complete` sequence plus the existing update download terminal semantics.
- Previously observed missing-key/settings-shape labels are gone: Go now emits `settings.nkpProviders`, `settings.nugProviders`, and `updateCheck.latestVersion`; `settings.kiroModels` is `null` when unavailable; the Go-only `settings.nugQuotas` field is not exposed in the settings response.
- User preferences/recent-tabs are now part of the sampled contract: `GET /api/user-preferences` covers default booleans/language, decoded `recentTabs`/`gatewayConfig`, and TS-compatible raw-string `graphViewports`; valid recent-tab upsert returns an array on both backends; malformed upsert missing `lastVisitedAt` returns 400 on both backends. Favorite directories are now sampled too: empty list, positive create, duplicate create, invalid create (`missing path`, `label:null`), missing update/delete, and invalid reorder (`missing ids`, empty ids) all compare clean. Settings/provider secret safety is now sampled with seeded OpenAI/Anthropic/NKP/NUG secrets: admin and regular-user `GET /api/settings` plus masked-provider `PATCH /api/settings` must return masked secrets and hidden `auth.jwtSecret` on both backends. Settings subroute coverage now includes `context-thresholds`, unresolved `test-model`, invalid retry-rule validation, and valid retry-rule creation with TS-compatible 8-character IDs.
- Skills/routines/MCP permissions UI now has sampled empty/seeded plus positive global-skill, project-routine, hook, and stdio MCP contract coverage: `GET /api/routines`, project routine status/toggle enable-disable-reset/missing/invalid flows, `GET /api/routines/global-prompt`, seeded `GET /api/skills/global`, global-skill create/detail/duplicate/toggle/update/list/delete/missing-state side effects, seeded project-skill list/detail, hook create/detail/update/list/all/delete plus safe update validation, `GET /api/mcp/servers`, `GET /api/mcp/tools`, invalid MCP create/test validation, disabled stdio server create, positive stdio connection test, enabled stdio create→connected list/tools side effect, and MCP tool source/server metadata all compare clean. Fixed Go global-skill unsafe-success drifts: create now returns 201 and rejects duplicates instead of overwriting, update/delete missing return 404 instead of creating/fake success, and toggle requires explicit boolean `enabled`. A real drift was also fixed in Go MCP server validation: create/test now reject TS-Zod-invalid `args`, `toolPermissions`, `enabled`, name/env/url shapes while preserving `allow` → `readWrite` normalization. Go MCP stdio server lifecycle now probes enabled creates/updates/imports and reports `/api/mcp/tools` with TS-compatible `source:"external"` metadata; Go Kiro MCP populated tools/search bridge has focused coverage for TS-compatible request headers/region and frontend top-level tools mirroring.
- Custom subagents / hooks / workspaces now have sampled coverage for safe boundaries plus custom-subagent, hook, and workspace positive side effects: custom-subagent list/missing/delete-missing/invalid-create, custom-subagent create/detail/update-rename/list/delete/detail-after-delete, workspace list/missing/short-tree/update-missing/delete-missing, workspace create/detail/update/list/delete/detail-after-delete, admin hook list/all/missing/delete-missing, hook create/detail/update-to-http/list/all/delete/detail-after-delete, and regular-user hook 403 all compare clean. Fixed Go drifts: `/api/hooks*` is admin-only like TS, hook delete-missing returns 404, hook update validates after missing-row lookup, persists headers and explicit null/empty string values, workspace short `tree` rejects with 400, workspace update/delete missing rows return 404 instead of fake success, and Go workspace `createdAt`/`updatedAt` responses now match TS ISO-string serialization for `timestamp_ms` columns.
- Project graph / chapter-edge main UI coverage now has a seeded two-chapter graph fixture with a dependency edge and exploration group. The TS-Go harness samples `GET /api/projects/:id/graph`, chapter-edge list by project/type/chapter, missing-scope validation, `type=review` validation, and missing delete. Fixed Go route drift: public chapter-edge list/create validation no longer accepts `review`, matching TS route validators even though review remains a DB/graph edge enum.
- Ruler segment read coverage now samples `GET /api/projects/:id/ruler/segment?detail=summary` and full segment detail with seeded chapter positions/edges. Fixed Go ruler display drift: `narratorStatus` now uses the same status + `substatus` priority folding as TS (`reflecting`, working/waiting, non-idle, then error/suspended/manual_override/interrupted/unread), so unread/error states render correctly in the Pixi ruler instead of falling back to raw `idle`.
- Graph/ruler position mutation coverage now samples TS-invalid and valid side-effect flows: graph missing `axisOffset` returns 400, graph valid update persists `axisOffset`/`crossOffset`/panel dimensions, ruler negative `crossOffset` returns 400, and ruler valid update persists `width`/`height` into `panelWidth`/`panelHeight`. Fixed Go validation drift: graph/ruler positions now reject missing required numeric fields and invalid ruler anchor/cross/size before mutation instead of silently saving zero/default values.
- FS directory picker / path-input coverage is now sampled with a filesystem fixture: shortcuts, default browse, explicit browse, hidden-dir toggle, missing path, mkdir missing/path-only/path-separator validation, positive mkdir, and Linux reveal unsupported/missing-path behavior all compare clean. Fixed Go drifts: `POST /api/fs/mkdir` no longer accepts Go-only `{path}` bodies, and Linux `POST /api/fs/reveal` now fails before spawning `xdg-open` with the TS-compatible validation error; health capabilities mark `fs.reveal` unsupported/fallback on unsupported platforms so frontend reveal actions are gated.
- Notification webhook validation and notification-sound lifecycle coverage is now sampled: invalid DingTalk/Feishu test bodies, missing-file upload, unsupported MIME upload, positive upload, authenticated download, delete, and missing delete all compare clean. Fixed Go drifts: sound upload now returns TS-compatible 200, enforces MIME/1MB validation before write, and sound GET/DELETE no longer search across all users' sound directories.
- Upload serving and public share missing-state baselines are now sampled with seeded narrator/avatar upload files: authenticated `GET /api/uploads/:narratorId/:imageId`, authenticated avatar serving, missing upload, public missing share download, and public missing share preview all compare clean. No Go route change was needed for these sampled baselines.
- Volume snapshot list/detail/update/applications/missing-state coverage is now sampled with a seeded snapshot/application row: project list, service filter, detail, metadata update, application history, missing get/update/delete/apply all compare clean. Fixed Go route drift: missing apply now returns 404 before Podman fallback, create validates required fields and chapter ownership before fallback, nullable description updates can clear to `null`, and unsupported Podman snapshot mutations now return `501` fallback instead of `200`, preventing frontend success toasts for disabled runtime operations.
- Browser-session empty list coverage is now sampled with the seeded narrator fixture: `GET /api/narrators/:id/browser-sessions` returns the TS-compatible direct array shape when no sessions exist. Fixed Go route/runtime drift: browser-session observer broadcasts lifecycle/count events even when optional Go-owned persistence is disabled, live close/TTL/screenshot/stop-tracing attempts now run before storage fallback, persisted missing close/TTL rows now return 404 instead of `200 {ok:false}`, TTL update response uses TS-style `{ok, session}`, stop/close responses use `{ok:true}`, and screenshots now include `Cache-Control: no-store`.
- Previously observed `POST /api/update/download` SSE mismatch is gone in the latest harness: both TS and Go return `404 application/json {error:"No update available"}` with no SSE events for the fresh dev/contract build no-update case; Go still keeps SSE for actual trusted release downloads.
- WS baseline pass remains: `/ws/narrator` and `/ws/terminal` both open successfully with the TS-issued token after switching to the Go backend on the same DB/settings.
- Current raw diff labels after TS-required coverage comparison: none.

| Frontend surface | Primary routes / protocol | Transport | Current Go status | Required diff assertions | Priority |
|---|---|---|---|---|---|
| App shell, auth, version, capability gates | `/api/health`, `/api/auth/status`, `/api/changelog`; capability hooks in `usePlatform` | JSON | Mostly ready, but health/version metadata differs and disabled features must be explicit | Compare status/content-type, `version`, `commit`, `gitAvailable`, and `capabilities.*`; missing capability fields must not default unsupported Go features to enabled UI | P0 |
| Dashboard / project list / project create | `/api/projects`, `/api/projects/:id`; clone/create stream from `POST /api/projects` | JSON + SSE for clone | Route exists; clone SSE and lifecycle side effects need page-level proof | JSON CRUD shape; clone SSE `progress`, `credential_required`, `complete`, `error`; no misleading success on git/credential failure | P0 |
| Project graph / ruler | `/api/projects/:id/graph`, `/api/projects/:id/graph/positions`, `/api/projects/:id/ruler*`, chapter edges | JSON | Seeded project graph read, chapter-edge list/error/delete-missing contracts, ruler segment summary/full reads, and graph/ruler position mutation side effects are sampled clean; Go now folds ruler `narratorStatus` from status + substatus like TS; ruler lifecycle mutations remain partial/degraded | Broader graph/ruler drag/panel page smoke; ruler mutation fallback responses and lifecycle smoke | P0/P1 |
| Chapter detail / graph actions / git panel | `/api/chapters*`, fork/merge/dormant/wake/cleanup/batch-merge, `/git/*`, commits/files | JSON | Broad route coverage, but lifecycle parity partial; split-at-commit is active with explicit partial fallback metadata for compressed AI summaries; frontend gates container routes and async batch-merge fields on explicit capability metadata | List/detail/update field parity; TS `GIT_NOT_INSTALLED` guard; merge-check/merge/fork response shape; commit/file diff truncation; `split` must keep health partials and route responses aligned with frontend gating | P0 |
| Narrator panel core | `/api/narrators/:id`, `/messages`, `/permissions`, `/goals`, `/buffer`, `/plan-mode`, `/compact`, `/retry`, `/continue`, `/rollback/:messageId`, `/edit-and-regenerate/:messageId`, `DELETE /api/narrators/:id`, `/api/workspaces*` | JSON + `/ws/narrator` | Partial/full mixed; rollback/edit routes are active with focused Go tests; workspace empty/missing/invalid-tree boundaries plus positive create/detail/update/list/delete side effects are sampled clean; narrator deletion is disabled-by-design and frontend-gated | Cursor pagination, `around`/catch-up shape, multipart message submit, permission approve/deny, goals/buffer/draft updates; broader workspace page smoke under recent-tab grouping; rollback/edit must keep TS-compatible message refs, file-state, WS, and rerun side effects; unsupported delete must stay `FEATURE_DISABLED` with disabled archived-page UI | P0 |
| Narrator realtime protocol | `/ws/narrator` subscribe/catch-up/presence/stats, message/tool/permission events | WebSocket | Core smoke exists; event surface still wider than current matrix | `subscribe`, `streaming_snapshot`, `catch_up`, `sync_check`, `sync_ok`, `full_reload`, `message_updated`, `messages_deleted`, `tool_*`, `permission_*`, reflection, compact, goals, buffer, subagent/background, quota/queue, browser/session/container/user events | P0/P1 |
| Terminal panel | `/api/terminals*`, tabs/view-state/processes, `/ws/terminal` | JSON + WebSocket | Unix baseline supported; Windows PTY and serialized xterm replay degraded | HTTP CRUD/tab/view-state shape; WS `subscribe`, `scrollback`, `bufferState`, `input`, `resize`, `output`, `requestResize`, `exit`, `error`; classify non-frontend events (`ready`, `snapshot`, `created`, `killed`, `renamed`) | P0/P1 |
| Search page | `/api/search?q=&entities=` | JSON | Baseline TS-created fixture coverage is clean for CJK, short-query LIKE, concrete result ids, snippet/matchField/matchScore, and relationship fields; remaining P1 risk is broader realistic ordering/relevance drift and degraded metadata under larger data | Result entity shape, `entities` filtering, larger relevance ordering, degraded metadata, realistic CJK/trigram fixtures | P1 |
| Settings, preferences, recent tabs, favorites | `/api/settings`, `/api/user-preferences`, recent-tabs routes, `/api/favorites*`, model/test/TLS/retry-rules | JSON + user WS events | Settings provider-secret masking/regular-user reads/masked-provider PATCH, context-thresholds, unresolved test-model, retry-rules valid/invalid, TLS generation focused response/persistence coverage, core preferences/recent-tabs shape, recent-tab validation, favorite directory empty/create/duplicate/validation/missing/reorder errors, and user-scoped `user:recent_tabs_snapshot` WS fanout are sampled or focused clean | Provider prefix conflicts, secret persistence beyond response masking, TS-Go positive TLS smoke remains intentionally out-of-harness, recent-tabs/favorites page smoke under reconnect and reorder positive flows | P0/P1 |
| Skills / routines / MCP permissions UI | `/api/skills*`, `/api/routines*`, `/api/hooks*`, `/api/custom-subagents*`, `/api/mcp/servers*`, `/api/mcp/tools`, `/api/kiro/mcp-tools` | JSON | Seeded global/project skills, global-skill positive create/detail/duplicate/toggle/update/list/delete/missing-state side effects, routines/global-prompt empty-state, project routine status/toggle enable-disable-reset/missing/invalid side effects, custom-subagent positive create/detail/update-rename/list/delete side effects, hook create/detail/update-to-http/list/all/delete side effects plus safe invalid update boundaries, MCP server/tool lists, disabled stdio create, positive stdio test/create→connected list/tools side effects, and invalid create/test validation are sampled clean; hooks are admin-only like TS; Go stdio MCP now probes enabled creates/updates/imports, reports `source:"external"`, and focused tests cover agent tool injection/execution; Kiro MCP populated tools/search bridge is focused-tested with TS-compatible region/headers and frontend top-level tools mirror | Live AWS/Kiro MCP E2E remains out-of-harness; non-stdio MCP transports plus MCP protocol/service-management/tool-injection paths remain disabled/fallback-gated via explicit capabilities | P1 |
| Browser sessions UI | `/api/narrators/:id/browser-sessions*`, `browser_session_count` | JSON + `/ws/narrator` | Supported by Go Rod runtime with Go-owned optional storage; empty list is sampled clean and focused tests cover live-first/fallback/missing-row safety | List is direct array with numeric ms fields; close/ttl/stop-tracing/screenshot manage live runtime before DB fallback; persisted missing mutations return 404; WS count/lifecycle broadcasts now fire even when persistence is default-off; capability reports `go-rod`/narrator-bound/artifact persistence/resource limits | P0/P1 |
| Update UI | `/api/update/check`, `/download`, `/status`, `/apply`, `/cleanup`, `/version` | JSON + SSE download | Download empty-state/admin-boundary samples are clean; trusted positive check samples cover release metadata/download sizes; trusted download-error SSE now matches TS `progress → error`; self-apply remains disabled/manual-only in Go; check error payloads preserve `latestVersion`/current fallback; frontend capability consumption treats `manualOnly=true` as disabling auto-apply even if self-update/apply details are absent | Check response extras tolerated; download success SSE still needs a safe compiled/zstd fixture; apply returns stable disabled/manual-only payload; future positive-download success fixture still needed once TS dev/compiled zstd path can be safely exercised | P0 |
| Provider settings | `/api/openai*`, `/api/anthropic*`, `/api/nkp*`, `/api/nug*`, `/api/kiro*`, `/api/codex*`, `/api/cline*` | JSON, plus Kiro chat SSE and Codex runtime transport toggles | Partial; regular-user OpenAI positive provider refresh/cache, regular-user Anthropic positive provider refresh with `/v1` fallback `resolvedBaseUrl`, regular-user NKP positive provider refresh/cache with `model_id` normalization, regular-user NUG positive model refresh/cache plus quota/cache/channels/usage/summary normalization, NUG login/API-key positive credential responses with no implicit settings persistence, NUG OAuth start/callback positive redirect plus masked persisted-settings side effects, and OpenAI/Anthropic/NKP/NUG auth boundaries now match TS; Codex quota-overview/status/settings/import validation is sampled, Kiro/Codex/Cline admin-only status boundaries are sampled, and Cline safe error plus populated cached-model pool/provider refresh/cache persistence/recommended-free-model refresh/cache persistence/enabled-model/context-window/settings persisted-provider view/pasted callback and browser-local callback credential side-effect routes/user-info refresh/expired-token refresh/invalid-refresh cleanup/balance flow are sampled; Go Kiro chat SSE is disabled-by-design and health-gated via `providers.kiro.routes.chat=false`; frontend provider capability consumption now requires explicit partial-Go support for routes/model refresh/quota/agent mode | Broader live populated-state numbers, Codex credential import/refresh/usage side effects, Cline/Kiro live credential flows beyond local-fixture cases; Kiro chat SSE must remain disabled in UI unless implemented | P0/P1 |
| Storage / runtime / gateway admin | `/api/storage/*`, `/api/runtime/*`, `/api/gateway/*`, benchmarks | JSON + storage scan SSE | Storage/runtime/benchmark unauthenticated and regular-user admin boundaries plus storage scan SSE are sampled clean; several runtime paths remain fallback/manual-only; frontend capability consumption now requires explicit partial-Go support for storage/database cleanup/preview, runtime scan/cache/cleanup, update download/apply, VNet, gateway platforms, uploads/shares/content, settings features, and benchmark container execution | Fallback codes for unsupported cleanup targets, benchmark positive runner behavior, broader storage cleanup fixtures | P1/P2 |

#### Required Diff Harness Shape

- **JSON fixtures:** run the same request sequence against TS and Go with the same seeded DB and compare
  status code, content type, required fields, error code, and side effects. Extra Go fields are allowed
  only when frontend consumers ignore them.
- **SSE fixtures:** compare ordered event names plus terminal-result semantics for project clone,
  update download, storage scan, and Kiro chat. Error paths must emit a parseable terminal `error`
  event instead of abruptly ending the stream.
- **WebSocket fixtures:** compare frontend-consumed message names and required fields, not every
  backend-only internal event. `/ws/narrator` recovery (`sync_check` → `sync_ok`/`catch_up`/
  `full_reload`) and `/ws/terminal` scrollback/buffer/input/resize are P0.
- **Capability-gated fallbacks:** for MCP non-stdio/persistent runtimes, update apply,
  VNet relay/UDP degradation, and unsupported runtime paths, verify fallback metadata and frontend-visible
  degraded/disabled state before treating the row as accepted. Rollback/edit-regenerate are now capability-gated as supported and require side-effect fixture parity instead of fallback acceptance. MCP stdio external tools are capability-gated as partial/per-call support; Browser sessions are capability-gated as supported Go Rod runtime with Chrome required.

#### P0 Diff Gates

- Health capability fields are explicit for every unsupported Go feature; absent fields must not make
  the frontend enable terminal/VNet/update runtimes, MCP transports/service-management/tool-injection paths, chapter container/batch-merge mutations, narrator runtime/tool actions, compressed split summaries, update apply, browser-session REST fetches, destructive narrator deletion, rollback/edit-regenerate actions, gateway persistent/Weixin/webhook runtimes, benchmark container execution, runtime maintenance cleanup actions, storage/database cleanup/preview actions, upload/share/content actions, filesystem system actions, settings storage/patch/TLS/security helpers, or provider route/model/quota/agent-mode calls. Focused frontend capability tests now cover partial-Go missing metadata for terminal runtime, update download/apply, VNet, MCP transports/protocol/tools/server management/injection, chapter split/container/batch-merge route/response/WS/rollback fields, narrator plan/compact/retry/permissions/review/subagents/toolInventory gates, browser sessions, narrator deletion, rollback/edit-regenerate, gateway platform/runtime gates, benchmark container execution, runtime maintenance scan/cache/cleanup targets, storage cleanup/database cleanup/preview targets, upload/share/content actions, filesystem actions, settings features, and provider route/model/quota/agent-mode gates. Rollback/edit-regenerate should be enabled only when `capabilities.narrator.rollbackEditRegenerate.supported=true` and still needs TS-vs-Go side-effect fixture evidence. Browser sessions should be enabled only when `capabilities.narrator.browserSessions.supported=true` and shown with Chrome-required copy.
- Project create clone SSE, narrator WS recovery, terminal WS baseline, update download SSE, and Codex
  `status`/`use-websocket` JSON all have TS-vs-Go fixture evidence.
- Auth/admin boundaries match TS for settings/provider secrets, update download/apply/cleanup, storage,
  benchmarks, dependency installs, and credential-management routes.
- Browser-session REST/WS fallback is stable and visible to UI; REST list/action shapes do not imply a
  usable runtime when `capabilities.narrator.browserSessions.supported=false`.

### Acceptance Criteria

- Public/auth boundaries match TS exactly, especially `/api/notification-sounds` mutation routes
  and dependency install admin-only behavior.
- Go implements the TS Git-unavailable guard contract for all TS `GIT_REQUIRED_RULES` routes, with
  the same 503 `GIT_NOT_INSTALLED` response shape and no over-blocking of setup/provider/search
  routes.
- A generated or manually reviewed route inventory maps every TS `server/app.ts` mount and
  `server/routes/*.ts` endpoint to one of: full parity, intentional fallback, additive Go-only,
  or unsupported. Unsupported/fallback entries must be accepted by the relevant owner track.
- For P1 degraded groups, endpoint-level smoke tests verify status code, JSON/SSE content type,
  required fields, and auth requirements against TS-compatible expectations.
- No Go-only route changes are treated as parity unless the corresponding frontend or migration
  plan explicitly adopts them.

### Notes

- Status is `Verified` for route-level evidence because paths and auth boundaries were checked
  against the requested files. This section may be marked `Done` once its priority list, notes, and
  acceptance criteria are considered complete; `Done` would not imply the P0 auth/guard gaps are fixed.
  P1 response-shape checks still require runtime/domain/narrator/frontend track confirmation.
- Cross-track context from spec-frontend aligns with this track: Go registers many frontend paths
  but several are fallback/degraded, especially ruler, routines, MCP/custom subagents, terminal WS,
  narrator WS, and field/event semantics.
- No source files other than this spec were modified.

## Track: DB / Schema / FTS Parity

Owner: spec-db
Status: Done
Last update: 2026-05-14

### Scope

- Compared `server/db/schema.ts`, `server/db/fts.ts`, and migration strategy with `go_backend/internal/db/*` and direct SQL in `go_backend/internal/**`.
- Tracked schema field mismatches, enum mismatches, unsupported tables, FTS/search behavior, and migration/no-migration implications.
- Investigation was read-only: no real database connection, no migration execution, no `drizzle/` edits.

### Evidence Summary

- Canonical TS schema defines 38 main tables in `server/db/schema.ts:4`, `server/db/schema.ts:26`, `server/db/schema.ts:49`, `server/db/schema.ts:133`, `server/db/schema.ts:161`, `server/db/schema.ts:205`, `server/db/schema.ts:307`, `server/db/schema.ts:335`, `server/db/schema.ts:381`, `server/db/schema.ts:407`, `server/db/schema.ts:432`, `server/db/schema.ts:492`, `server/db/schema.ts:520`, `server/db/schema.ts:537`, `server/db/schema.ts:562`, `server/db/schema.ts:584`, `server/db/schema.ts:592`, `server/db/schema.ts:653`, `server/db/schema.ts:668`, `server/db/schema.ts:684`, `server/db/schema.ts:703`, `server/db/schema.ts:727`, `server/db/schema.ts:750`, `server/db/schema.ts:773`, `server/db/schema.ts:796`, `server/db/schema.ts:814`, `server/db/schema.ts:833`, `server/db/schema.ts:859`, `server/db/schema.ts:879`, `server/db/schema.ts:909`, `server/db/schema.ts:926`, `server/db/schema.ts:976`, `server/db/schema.ts:995`, `server/db/schema.ts:1028`, `server/db/schema.ts:1062`, `server/db/schema.ts:1071`, `server/db/schema.ts:1099`, `server/db/schema.ts:1137`.
- Go main DB intentionally does not create or migrate schema: `go_backend/internal/db/schema.go:5` returns nil, and `go_backend/internal/db/db.go:42` states the Go compatibility backend must use the TS-created schema.
- Go SQLite setup still mutates connection/database pragmas (`journal_mode = WAL`, `foreign_keys = ON`, `busy_timeout = 250`) in `go_backend/internal/db/db.go:49`; no table/column migration is present in `go_backend/internal/db/*`.
- TS FTS creates trigram FTS5 virtual tables and triggers for `chapters`, `narrator_messages`, and `narrators` in `server/db/fts.ts:47`, `server/db/fts.ts:52`, `server/db/fts.ts:57`, `server/db/fts.ts:64`, `server/db/fts.ts:86`, `server/db/fts.ts:108`; it also detects non-trigram legacy FTS tables and rebuilds after migration/unclean shutdown in `server/db/fts.ts:34` and `server/db/fts.ts:133`.
- TS search uses sanitized FTS5 `MATCH`, `snippet(...)`, and rank ordering with LIKE fallback for short queries in `server/services/search-service.ts:31`, `server/services/search-service.ts:54`, `server/services/search-service.ts:84`, `server/services/search-service.ts:122`, `server/services/search-service.ts:171`.
- Go search does not use the FTS virtual tables; it uses `LIKE` scans for projects, chapters, messages, and narrators in `go_backend/internal/search/handlers.go:41`, `go_backend/internal/search/handlers.go:73`, `go_backend/internal/search/handlers.go:100`, `go_backend/internal/search/handlers.go:118`.
- Go project backup/import has an independent project-local schema for a subset of tables only (`projects`, `exploration_groups`, `chapters`, `chapter_edges`, `chapter_commits`, `narrators`, `narrator_goals`, `project_sync_metadata`) in `go_backend/internal/projects/service.go:359` and `go_backend/internal/projects/service.go:379`; this is not the main DB migration path.
- Go direct SQL uses newer TS tables such as `workspaces`, `hooks`, `volume_snapshots`, `volume_snapshot_applications`, `api_requests`, `background_tasks`, `gateway_session_mappings`, and narrator permission/snapshot tables, relying on their presence in the TS-created DB (`go_backend/internal/misccompat/handlers.go:123`, `go_backend/internal/misccompat/handlers.go:228`, `go_backend/internal/misccompat/handlers.go:2425`, `go_backend/internal/systemroutes/handlers.go:2565`, `go_backend/internal/narrators/handlers.go:2491`, `go_backend/internal/systemroutes/handlers.go:590`).
- Go direct SQL references optional/non-`schema.ts` tables with table-existence gates, notably `custom_subagents` and `mcp_servers`, and degrades to empty/NotImplemented when absent (`go_backend/internal/misccompat/handlers.go:293`, `go_backend/internal/misccompat/handlers.go:316`, `go_backend/internal/mcpcompat/handlers.go:299`, `go_backend/internal/mcpcompat/handlers.go:915`).

### Findings

- **Main schema ownership is intentionally TS-only.** This is compatible with a “compatibility backend” if users always start from a TS-migrated database, but it is not a standalone replacement: a fresh Go-only install will not create the 38 canonical tables or FTS objects.
- **FTS/search parity is incomplete.** TS relies on FTS5 trigram tables with `MATCH`, `snippet`, and rank-based scoring; Go returns superficially similar search result shapes but falls back to unindexed `LIKE`, fixed scores, no FTS snippets, no rank ordering, and potentially worse CJK/substring behavior.
- **Schema table coverage is mostly by assumption, not by Go guard.** Because Go does not run migrations, direct SQL against canonical tables will fail if the TS schema is older than the Go code expects. Some optional tables have guards (`custom_subagents`, `mcp_servers`), but many TS schema tables used directly by Go do not have version/capability checks.
- **Enum parity is only partially enforced.** SQLite has no CHECK constraints in either Drizzle definitions or Go direct SQL; Go writes several enum-like literals (`review`, `merge`, `fork`, `running`, `cancelled`, etc.) that match observed TS enum values, but invalid values are not prevented at DB level. Tests also contain non-canonical literals such as `permission_mode = 'trust-all'` and `narrator_tool_calls.status = 'approved'` in `go_backend/internal/narrators/agent_event_integration_test.go:458` and `go_backend/internal/narrators/agent_event_integration_test.go:537`; these are test-only evidence of drift risk, not observed production writes.
- **Project-local backup DB is a deliberate subset.** It should not be mistaken for schema parity: it omits messages, tool calls, terminals, user preferences, review conclusions, volume snapshots, hooks, API requests, background tasks, etc.

### P0

- **P0 — Search/FTS behavior is not replacement-equivalent.**
  - Partial: Go `/api/search` now attempts TS-created FTS5, falls back to LIKE with explicit degraded metadata when FTS is unavailable, treats short-query LIKE mode as normal non-degraded behavior, preserves invalid-entity empty-result behavior, and has focused tests for ready FTS metadata/rank/snippets plus CJK trigram substring hits. Remaining gap is endpoint-level TS-vs-Go smoke on a TS-created fixture DB, especially exact ordering/relevance over larger realistic data.
  - Evidence: TS FTS setup in `server/db/fts.ts:47`, `server/db/fts.ts:52`, `server/db/fts.ts:57`; TS search in `server/services/search-service.ts:54`, `server/services/search-service.ts:84`, `server/services/search-service.ts:122`; Go search in `go_backend/internal/search/handlers.go:79-155`, `go_backend/internal/search/handlers.go:187-223`, `go_backend/internal/search/handlers.go:268-303`, `go_backend/internal/search/handlers.go:340-382`; tests in `go_backend/internal/search/handlers_contract_test.go:13-219`.
  - Impact: Search replacement risk is reduced to smoke/fixture proof and larger-data relevance drift; missing/corrupt FTS is now controlled by degraded metadata rather than unhandled 5xx.
  - Acceptance: Keep Go search on existing TS-created FTS tables with compatible sanitized `MATCH`/snippet/rank semantics and LIKE fallback for <3 chars, and complete DB-FTS-01 endpoint smoke against a TS-created database with CJK/substring/message/narrator/chapter cases.

- **P0 — No Go-side schema/bootstrap path for standalone replacement.**
  - Gap: `go_backend/internal/db/schema.go` intentionally does nothing; `go_backend/internal/db/db.go` explicitly forbids schema creation/migration. This is safe for compatibility mode but unsafe for a Go-only fresh install or a TS DB older than required Go direct SQL.
  - Evidence: `go_backend/internal/db/schema.go:5`, `go_backend/internal/db/db.go:42`; canonical table list in `server/db/schema.ts` evidence above.
  - Impact: Fresh installs or stale DBs can fail at first request with “no such table/column”; Go cannot be considered a full TS backend replacement without a migration/version policy.
  - Acceptance: Decide and implement one of: (a) Go replacement invokes/embeds canonical migrations and FTS setup safely; (b) startup performs read-only schema/FTS capability checks and refuses replacement mode with actionable errors; or (c) product explicitly documents Go backend as TS-migrated-DB-only compatibility mode.

### P1

- **P1 — No schema version/capability guard for directly used canonical tables/columns.**
  - Gap: Go directly reads/writes many TS schema tables and newer columns (`api_requests`, `background_tasks`, `workspaces`, `hooks`, `volume_snapshots`, `gateway_session_mappings`, narrator compact/buffer/background columns) without a centralized compatibility check.
  - Evidence: TS definitions in `server/db/schema.ts:909`, `server/db/schema.ts:926`, `server/db/schema.ts:976`, `server/db/schema.ts:995`, `server/db/schema.ts:1028`, `server/db/schema.ts:1137`; Go use in `go_backend/internal/misccompat/handlers.go:123`, `go_backend/internal/misccompat/handlers.go:228`, `go_backend/internal/systemroutes/handlers.go:2565`, `go_backend/internal/narrators/handlers.go:2472`, `go_backend/internal/narrators/handlers.go:2491`.
  - Acceptance: Add a startup/readiness schema capability report covering required tables, columns, indexes, and FTS objects for the enabled Go routes; expose actionable degraded-mode errors rather than ad hoc SQL failures.

- **P1 — FTS lifecycle parity is absent in Go.**
  - Gap: TS detects old non-trigram FTS, creates triggers, rebuilds on migration/unclean shutdown, and uses a clean shutdown marker. Go neither verifies nor repairs FTS objects.
  - Evidence: `server/db/fts.ts:34`, `server/db/fts.ts:64`, `server/db/fts.ts:86`, `server/db/fts.ts:108`, `server/db/fts.ts:133`, `server/db/fts.ts:156`; no `_fts` creation or `MATCH` use found under `go_backend/internal/**` except no-op/fallback search evidence above.
  - Acceptance: If Go uses FTS, it must either call an equivalent safe FTS verifier/rebuilder or require TS to have completed FTS setup and surface a clear readiness failure when missing/corrupt.

- **P1 — Optional/non-canonical table handling is inconsistent.**
  - Gap: Go guards `custom_subagents` and `mcp_servers` with `tableExists`, but these tables are not present in `server/db/schema.ts`; behavior depends on migrations or external storage not visible in the canonical schema file.
  - Evidence: no `custom_subagents`/`mcp_servers` export in `server/db/schema.ts`; Go guards in `go_backend/internal/misccompat/handlers.go:293`, `go_backend/internal/misccompat/handlers.go:316`, `go_backend/internal/mcpcompat/handlers.go:299`, `go_backend/internal/mcpcompat/handlers.go:915`.
  - Acceptance: Document these as optional feature stores or add canonical schema/migration ownership; API/frontend tracks should confirm empty/NotImplemented fallback is acceptable.

- **P1 — Project backup DB schema is a subset and may surprise lifecycle/import parity.**
  - Gap: `projectBackupTables` includes only seven project-domain tables plus metadata and omits narrator messages/tool calls, reviews, terminals, hooks, API requests, etc.
  - Evidence: `go_backend/internal/projects/service.go:359`, `go_backend/internal/projects/service.go:379`.
  - Acceptance: Either rename/document this as scoped project metadata sync, or expand it to the TS-backed project export/import contract if replacement readiness requires full project portability.

### P2

- **P2 — Enum-like DB values are not centrally typed or validated in Go.**
  - Gap: TS Drizzle declares enum value sets for projects/chapters/narrators/tool calls/etc.; SQLite does not enforce them, and Go uses string literals scattered across handlers/services.
  - Evidence: enum declarations in `server/db/schema.ts:8`, `server/db/schema.ts:37`, `server/db/schema.ts:58`, `server/db/schema.ts:65`, `server/db/schema.ts:146`, `server/db/schema.ts:213`, `server/db/schema.ts:229`, `server/db/schema.ts:253`, `server/db/schema.ts:446`, `server/db/schema.ts:736`, `server/db/schema.ts:1080`, `server/db/schema.ts:1145`; Go string writes across `go_backend/internal/chapters/service.go:273`, `go_backend/internal/chapters/service.go:805`, `go_backend/internal/narrators/service.go:2661`, `go_backend/internal/narrators/service.go:2681`, `go_backend/internal/terminalruntime/manager.go:116`.
  - Acceptance: Introduce Go constants/validators for TS enum values used in direct SQL and add tests that production writes stay within the TS enum sets.

- **P2 — Main DB index parity is implicit.**
  - Gap: TS schema declares indexes/unique indexes for hot paths; Go assumes those indexes already exist and does not verify them.
  - Evidence: representative TS indexes in `server/db/schema.ts:125`, `server/db/schema.ts:152`, `server/db/schema.ts:197`, `server/db/schema.ts:300`, `server/db/schema.ts:372`, `server/db/schema.ts:398`, `server/db/schema.ts:483`, `server/db/schema.ts:964`, `server/db/schema.ts:1170`; Go no-op schema in `go_backend/internal/db/schema.go:5`.
  - Acceptance: Include required indexes in the startup capability report, especially for Go endpoints with broad scans or joins.

### Acceptance Criteria

- Go backend startup/readiness states one of two modes explicitly:
  - **Compatibility mode:** refuses or warns clearly when the TS-created schema/FTS objects required by enabled routes are missing or too old; never attempts migrations.
  - **Replacement mode:** owns a safe migration/FTS bootstrap path equivalent to TS/Drizzle plus `server/db/fts.ts`.
- `/api/search` parity has focused Go fixtures covering short-query non-degraded LIKE mode, invalid-entity empty result behavior, FTS-ready metadata/rank/snippets, FTS-missing degraded fallback, special-character sanitization, TS-compatible chapter LIKE snippets, and CJK trigram substring matches across chapters/messages/narrators. Remaining `/api/search` parity requires DB-FTS-01 TS-vs-Go endpoint smoke on a TS-created DB covering:
  - larger realistic ranking/order comparisons;
  - exact frontend payload tolerance for additive `searchMetadata` / top-level degraded fields;
  - missing/corrupt FTS objects producing controlled readiness/degraded-mode responses without 5xx.
- Direct-SQL production writes are checked against TS table/column/enum expectations, with Go constants or validators for every enum-like value written by Go.
- Project backup/import scope is documented and tested as either a subset or expanded to full TS parity.
- Optional tables not in `server/db/schema.ts` (`custom_subagents`, `mcp_servers`) have explicit ownership and user-facing degradation behavior.

### Notes

- Main DB “no migrations in Go” is not itself a bug if replacement readiness is scoped to a compatibility backend over a TS-migrated database; it becomes P0 only for standalone replacement claims.
- `go_backend/internal/db/db.go:49` sets connection pragmas including `busy_timeout = 250`, which aligns with the project rule to avoid multi-second SQLite busy blocking.
- No real DB was opened for verification; evidence is static code only.

### Go DB Compatibility Contract

Go backend DB handling is intentionally read-only over a TS/Bun-owned main database. The contract below is the operational rule set that makes the compatibility backend safe:

1. **Startup order is fixed.** `go_backend/internal/db/db.go` must open the SQLite file, then run `VerifyCanonicalSchema`, then run `EnsureMigrationCompatibility`. If any step fails, Go must close the DB and refuse to serve requests.
2. **Go never repairs the main schema.** `go_backend/internal/db/schema.go` remains a no-op for main schema creation/migration. Go may only write its own namespaced compatibility journal table `__narrafork_go_migration_journal` after schema readiness has passed.
3. **Canonical schema is the readiness gate.** `CanonicalSchemaContract()` is the read-only contract report. It must continue to describe the TS-owned main schema, required indexes, FKs, FTS tables, and FTS sync triggers that enabled Go routes rely on.
4. **Migration compatibility is journal-based, not migration-based.** `EnsureMigrationCompatibility()` compares the embedded Drizzle snapshot against the existing TS journal `__drizzle_migrations`. It does not execute Drizzle SQL. Missing expected hashes block startup; extra unknown hashes are recorded in the Go journal but do not mutate TS-owned business tables.
5. **FTS is diagnostic-only in Go.** Go does not rebuild or repair FTS. If FTS tables/triggers are missing or incompatible, the readiness check must fail with an actionable message telling the user to start the TypeScript backend once so TS can run migrations, `ensureColumns`, backfills, and `ensureFts`.
6. **Legacy data repair remains TS-owned.** Old narrator status values, missing columns, malformed JSON trait fields, and clean-shutdown/FTS rebuild recovery stay in the TS startup path. Go may detect them and refuse startup, but it must not patch them.

#### DB Compatibility Decision Table

| Condition | Go behavior | User action |
|---|---|---|
| `narrafork.db` missing | fail fast before opening | start TS backend once to create and migrate the DB |
| `__drizzle_migrations` missing | fail fast with migration guidance | run `bun run db:migrate` or start TS backend once |
| Expected Drizzle hash missing | fail fast with the missing tag/hash list | start TS backend once so migrations are applied |
| Extra unknown Drizzle hashes present | allow startup, record them in Go journal | treat as forward-compatible only if canonical schema still passes |
| Canonical table/column/index/FK mismatch | fail fast with read-only schema mismatch details | run TS backend once to migrate/backfill the database |
| FTS table/trigger/tokenizer mismatch | fail fast with FTS readiness diagnostics | run TS backend once so `ensureFts` can rebuild or validate the FTS objects |
| Legacy narrator status/traits/substatus shape | fail fast if readiness is broken | run TS backend once so `ensureColumns` and data backfill can normalize the rows |

#### TS Repair Instruction

When Go reports a DB compatibility failure, the expected recovery path is:

1. Start the TypeScript backend once on the same database, or run `bun run db:migrate` followed by `bun run dev`/`bun run start`.
2. Let TS complete `runMigrations`, `ensureColumns`, the narrator status/trait backfill, and `ensureFts`.
3. Re-run Go startup. If Go still fails, the remaining issue is a real schema drift, not a missing TS repair step.

### Track: Domain Lifecycle Parity

Owner: spec-domain
Status: Done
Last update: 2026-05-14

### Scope

- Compared lifecycle behavior in the requested TS surface:
  `server/services/{project,chapter,chapter-fork,chapter-merge,chapter-batch-merge,review,git,chapter-edge,dependency}*.ts`
  and `server/routes/{projects,chapters,git,graph,ruler,reviews,chapter-edges}.ts`.
- Compared the requested Go surface:
  `go_backend/internal/{projects,chapters,chapteredges,graph,explorations,gitroutes,git}/`.
- Focus: projects, chapters, fork/split/dormant/wake, merge/unmerge/batch merge, review,
  ruler, graph, dependency edges, and git panel lifecycle semantics.

### Verified Findings

- Go has a substantial compatibility backend for basic project/chapter/git flows, but it is not
  lifecycle-parity safe yet. Basic project CRUD, root chapter creation, create/fork/review/merge,
  graph reads, edge CRUD, exploration groups, review conclusion actions, and git
  status/stage/commit/diff/stash/reset exist.
- Highest-risk gaps are semantic lifecycle gaps, not just missing endpoints: project/chapter delete,
  fork-at-message state, review correctness, batch merge orchestration, partial/degraded ruler lifecycle
  semantics, graph update semantics, and git safety differ from the TS backend.

#### Projects lifecycle

- **Implemented in Go:** list/create/get/update/delete plus import/backup endpoints are registered in
  `go_backend/internal/projects/handlers.go:18-27`; create supports `existing`/`init`/`clone`, SSE
  progress for clone, default branch detection, project insert, and root chapter creation in
  `go_backend/internal/projects/service.go:83-137`; backup/import copies a scoped table set in
  `go_backend/internal/projects/service.go:219-341`.
- **TS evidence:** project creation also initializes the per-project backup DB, updates `.gitignore`,
  and commits the backup ignore entry in `server/routes/projects.ts:237-241`; project deletion walks
  every chapter through full cleanup, deletes exploration groups, prunes worktrees, and performs
  fallback FK cleanup in `server/routes/projects.ts:313-443`.
- **Fixed 2026-05-25:** Go project delete is no longer a direct project-row delete. It collects
  project chapters/narrators/terminals/containers, kills terminal runtimes, removes non-root worktrees,
  prunes worktrees, removes container IDs, deletes broad project-scoped rows (chapters, edges,
  commits, exploration groups, narrators/messages/tool calls/snapshots/buffers, terminals/tabs/view
  state, containers/ports, merge sessions, review conclusions, volume snapshots, hooks, gateway
  mappings, benchmark rows), cleans workspace/recent-tab JSON references, and returns cleanup warning
  details for external failures. Evidence: `go_backend/internal/projects/service.go:312-581`, tests in
  `go_backend/internal/projects/service_delete_test.go:24-104`. Remaining deletion parity gap is
  external side-effect proof: TS emits async recent-tab/proxy-cache refresh flows after delete, while Go
  performs DB-side tab cleanup but has no equivalent frontend/event/proxy invalidation proof.
- **Gap:** Go create does not mirror TS `.narrafork/project.db` initialization and `.gitignore`
  commit behavior on normal project creation (`server/routes/projects.ts:237-241` vs
  `go_backend/internal/projects/service.go:126-136`). Go does have explicit backup sync/import, but
  create-time backup DB lifecycle is incomplete.
- **Fixed 2026-05-25:** Go returns `chapterSettings` as parsed JSON and PATCH now merges incoming
  `chapterSettings` with existing values instead of overwriting the object, matching TS route behavior
  for lifecycle settings such as `requireReviewBeforeMerge`. Go also rejects invalid TS-schema fields
  for project PATCH (`status`, branch names, string length limits), ignores Go-only `flowMode` updates,
  and strips unsupported `chapterSettings` keys rather than persisting Go-only settings. Evidence:
  `go_backend/internal/projects/service.go:177-310`, `go_backend/internal/projects/service.go:1358-1372`,
  tests in `go_backend/internal/projects/service_json_test.go:13-158`; TS baseline
  `server/routes/projects.ts:275-295` and `server/lib/validators/projects.ts:25-50`.

#### Chapters create/update/delete/dormant/wake lifecycle

- **Implemented in Go:** chapter routes include list/create/get/patch/delete, dormant/wake, cleanup,
  fork/split/cherry-pick/dependency sync, merge/unmerge, review, commits, git-status, and container
  helpers in `go_backend/internal/chapters/handlers.go:29-66`; create makes a branch/worktree,
  inserts chapter metadata, and creates a narrator in
  `go_backend/internal/chapters/service.go:152-208`.
- **TS evidence:** chapter create validates repo/base branch, rolls back branch/worktree on DB errors,
  creates a narrator according to project chapter settings, emits events, schedules auto-dormant, and
  syncs commits in `server/services/chapter-service.ts:148-232`.
- **Gap:** Go chapter create lacks event bus broadcasts, auto-dormant scheduling, commit sync, and
  project `chapterSettings` handling beyond default narrator config. It is adequate for basic branch
  creation but not full lifecycle parity.
- **Fixed 2026-05-25:** Go `Update` now enforces TS root/trunk invariants: root chapters reject
  `status`/`role` changes, and non-root chapters reject `role: "trunk"`. Evidence:
  `go_backend/internal/chapters/service.go:1243-1256`, tests in
  `go_backend/internal/chapters/service_json_test.go:71-180`; TS baseline remains
  `server/services/chapter-service.ts:278-319`.
- **Gap:** Go dormant/wake routes now call dedicated service methods instead of raw `Update(status)`,
  with worktree/container side effects partially implemented, but still need endpoint-level parity
  proof against TS worktree lifecycle, auto-dormant scheduling, commit sync, and event broadcasts.
- **Fixed 2026-05-25:** Go `Delete`/cleanup now kills/deletes terminal records, removes
  containers/ports, removes worktree/branch, archives narrators, clears child/merge chapter references,
  deletes the chapter, and cleans recent tab references from `user_preferences`. Evidence:
  `go_backend/internal/chapters/service.go:1286-1361`, `1465-1528`, tests in
  `go_backend/internal/chapters/service_merge_parity_test.go:165-238` and
  `go_backend/internal/chapters/terminal_lifecycle_test.go`. Remaining chapter-delete parity gap is
  frontend-visible side effects: TS interrupts live narrators and broadcasts recent-tabs snapshots and
  `chapter:abandoned`; Go DB cleanup is present but event/WS/UI smoke proof is still needed.

#### Fork, split, and cherry-pick lifecycle

- **Implemented in Go:** fork creates branch/worktree, records parent/fork fields, creates a fork edge,
  resolves `startCommitSha`/`forkAtMessageUuid`, restores file snapshots at the fork message when
  available, copies project-configured files, supports `full` and `fresh` narrator inheritance, records
  explicit compressed-context fallback metadata, inherits container config/autostarts containers, and
  runs startup scripts (`go_backend/internal/chapters/service.go:282-444`, `467-538`; tests in
  `go_backend/internal/chapters/fork_parity_integration_test.go:20-193`). Split and cherry-pick
  endpoints exist in `go_backend/internal/chapters/handlers.go:310-373`.
- **TS evidence:** TS fork resolves `startCommitSha` or `forkAtMessageUuid`, restores uncommitted file
  snapshots at a fork message, copies commit history, copies project-configured files, forks the
  primary narrator with inherit mode, optionally starts containers, runs startup script, records fork
  edge metadata, and emits events (`server/services/chapter-fork.ts:55-397`).
- **Remaining fork gap:** Go is no longer silently fresh-forking `inheritMode`/`forkAtMessageUuid`.
  Remaining fork parity is narrower: compressed forks use bounded copied refs plus explicit fallback
  metadata instead of a TS-equivalent AI context summary, and event-bus/commit-history parity still
  needs endpoint-level proof against TS fixtures.
- **Implemented in Go, still needs TS-vs-Go fixture proof:** split-at-commit now creates a frozen prefix chapter, preserves the original chapter as the continuation, creates a new fork from the split point, rewires fork edges/child chapters, truncates prefix/new-fork narrator refs at the commit/message cutoff, auto-starts inherited containers only for the new fork, and rolls back DB/git artifacts on transaction failure (`go_backend/internal/chapters/handlers.go:365-400`, `go_backend/internal/chapters/split_at_commit_test.go:21-277`). Remaining split parity risk is endpoint-level TS-vs-Go semantic proof across richer real project histories and compressed AI summary quality/fallback behavior, not a disabled or shallow-fork route.
- **Gap:** Go cherry-pick can apply commits and refresh metadata
  (`go_backend/internal/chapters/handlers.go:340-372`), but it does not create `cherry_pick` edges or
  commit-sync/history records comparable to TS edge model.

#### Merge, unmerge, AI resolve, and batch merge lifecycle

- **Implemented in Go:** merge check, merge, AI resolve, unmerge, and batch merge are present in
  `go_backend/internal/chapters/handlers.go:109-123`, `485-539`; core merge/unmerge logic is in
  `go_backend/internal/chapters/service.go:600-917`.
- **TS evidence:** TS merge auto-commits dirty source changes when review is not required, validates
  stale review approval, locks target worktree, collects merge context, marks source merged with
  `worktreePath: null`, removes source worktree after DB update, creates merge edge, records commit,
  emits events, and injects merge summary (`server/services/chapter-merge.ts:72-185`, `232-388`,
  `540-659`).
- **Gap:** Go merge now rejects dirty source worktrees, validates required-review approval/no-review/
  non-approve/stale-commit cases, and locks the target worktree. Remaining merge parity gaps are that
  non-review merges reject instead of TS auto-committing dirty source state, source worktrees are not
  removed after merge, merge events are not emitted, and merge summaries are not generated/injected.
  Evidence: Go `go_backend/internal/chapters/service.go:1566-1657`, `2220-2345`, tests in
  `go_backend/internal/chapters/service_merge_parity_test.go:35-137`, `91-137`; TS
  `server/services/chapter-merge.ts:127-185`, `232-388`, `540-659`.
- **Gap:** Go AI resolve uses a one-shot `agentbasic.Generate` and direct file check
  (`go_backend/internal/chapters/service.go:690-797`), whereas TS uses the full narrator session loop
  with merge context and evented behavior (`server/services/chapter-merge.ts:390-538`). This affects
  permissions, tools, streaming, and auditability; ownership overlaps Narrator / Agent Parity.
- **Gap:** Go unmerge has the core reset/revert path and merge-edge cleanup, but lacks TS commit sync,
  merge-summary cleanup parity, event broadcasts, and branch/worktree path derivation details
  (`go_backend/internal/chapters/service.go:829-917` vs `server/services/chapter-merge.ts:690-830`).
- **Updated 2026-05-26:** Go batch merge now exposes the TS-style async session contract instead
  of the older synchronous-only behavior: `POST /api/chapters/batch-merge` returns
  `mergeSessionId` / `targetChapterId` / `createdTarget` / `status`, persists `merge_sessions`,
  broadcasts `merge:started`, `merge:step_ok`, `merge:conflict`, `merge:ai_resolving`,
  `merge:completed`, `merge:cancelled`, and `merge:error`, waits for WS `merge_decision`, rolls
  back newly created targets on cancel/error, and marks interrupted `running` / `ai_resolving` /
  `waiting_decision` sessions as `error` on Go startup. Health advertises
  `chapters.batchMerge.mode="async-merge-session"`, route/response/event details, decision WS,
  stale-session cleanup, and created-target rollback. Frontend project and graph batch-merge
  entry points now gate on this capability, show "merge session started" for async responses,
  consume global `merge:*` narrator WS events for completion/cancel/error/conflict notifications,
  expose continue/cancel actions on conflict notifications that send WS `merge_decision`, and refresh
  graph/`narraFlow`/chapter queries.
  Evidence: `go_backend/internal/chapters/service.go:925-1188`,
  `go_backend/internal/chapters/handlers.go:156-235`,
  `go_backend/internal/ws/hub.go:1716-1747`, `go_backend/cmd/narrafork-go/main.go:246-253`,
  `go_backend/internal/chapters/batch_merge_test.go` including explicit HTTP session-response,
  wait-decision continue/cancel, user-cancel, and timeout coverage, `frontend/hooks/usePlatform.ts`,
  `frontend/routes/projects/$projectId.tsx`, `frontend/components/graph/NarraFlow.tsx`, and
  `frontend/components/chapter/ChapterBatchMergeModal.tsx`.
- **Remaining gap:** batch merge still needs TS-vs-Go end-to-end smoke proof across reconnect,
  conflict continue/cancel, AI resolution failure, DB/git rollback, project DB sync, and frontend
  progress UI behavior before replacement can be marked safe.

#### Review lifecycle

- **Implemented in Go:** `/api/chapters/:id/review` creates a review chapter with review role,
  review edge, copied dirty/untracked source state, capped committed+uncommitted diff context, review
  system prompt, and optional user start message (`go_backend/internal/chapters/service.go:820-923`,
  `2862-3008`); tests cover copied dirty files, diff context, prompt instructions, and prompt/start
  initialization errors in `go_backend/internal/chapters/review_parity_test.go:18-99`. Review
  conclusion tooling persists TS-shaped conclusions and injects feedback; dismiss/convert/promote
  compatibility routes are implemented and tested (`go_backend/internal/narrators/review_parity_test.go`,
  `go_backend/internal/misccompat/review_parity_test.go`, `go_backend/internal/misccompat/handlers.go:2125-2209`).
- **TS evidence:** review creation copies dirty/untracked source files, builds a capped diff context,
  creates a fresh narrator with review-specific system prompt, sends the review start message, rolls
  back on failure, checks/resets review worktree mutations, concludes reviews, converts to subagent,
  promotes, dismisses, and emits review events (`server/services/review-service.ts:31-197`,
  `208-670`; `server/routes/reviews.ts:11-53`).
- **Remaining gap:** Go now covers dirty-source copy, rich diff context, ConcludeReview persistence,
  and basic dismiss/convert/promote compatibility. It still does not auto-start the full narrator loop
  in the same way as TS `sendMessage`, has partial rollback around post-narrator initialization, and
  still lacks TS event bus parity plus endpoint-level proof for review worktree reset-on-mutation across
  the full UI lifecycle.

#### Ruler lifecycle

- **TS evidence:** ruler routes implement timeline/segment reads, positions, commit-based fork,
  trunk merge with dirty checks and Ruler-specific branch deletion, abandon, rebase, and rebase
  conflict resolution via narrator (`server/routes/ruler.ts:81-733`), plus temp-chapter AI conflict
  resolution in `server/services/chapter-merge.ts:832-1078`.
- **Go evidence:** no `go_backend/internal/ruler/` package exists in the requested domain surface, but
  Go does register misccompat ruler compatibility routes. Route coverage is present at
  `go_backend/internal/app/router.go:98-103`; list/segment/position compatibility handlers are at
  `go_backend/internal/misccompat/handlers.go:2046-2054`; related fork/merge/abandon/rebase-style
  implementation code is in `go_backend/internal/misccompat/handlers.go:1652-1891` and
  `go_backend/internal/misccompat/handlers.go:2058-2329`. Go also supports `flowMode == "ruler"`
  during project create (`go_backend/internal/projects/service.go:122-125`) and graph positions.
- **Gap:** Ruler mode is not absent in Go, but its lifecycle semantics are partial/degraded compared
  with TS: compatibility routes exist, yet trunk merge dirty checks, Ruler-specific branch deletion,
  temp-chapter narrator conflict resolution, and existing TS DB row semantics still need endpoint-level
  parity verification before replacement.

#### Graph and exploration lifecycle

- **Implemented in Go:** graph read and graph position patch are registered in
  `go_backend/internal/graph/handlers.go:18-21`; graph nodes include review node type, narrator meta,
  containers flag, dependency upstream update state, positions, exploration groups, and opened
  terminals in `go_backend/internal/graph/handlers.go:51-168`; graph position patch accepts both
  `panelWidth`/`panelHeight` and ruler `width`/`height` aliases in
  `go_backend/internal/graph/handlers.go:236-292`; exploration group CRUD/decide/abandon exists in
  `go_backend/internal/explorations/handlers.go:19-26`.
- **TS evidence:** TS graph builds nodes and edges from chapters plus narrator counts/statuses and
  supports graph position patch in `server/routes/graph.ts:52-135`, `285`; TS ruler positions accept
  width/height field aliases in `server/routes/ruler.ts:436-458`.
- **Fixed 2026-05-25:** Go graph now surfaces dependency upstream update state via
  `nodes[].data.hasUpstreamUpdates`, and graph position patch accepts TS ruler `width`/`height`
  aliases with explicit `panelWidth`/`panelHeight` taking precedence.
- **Fixed 2026-05-29:** The TS-Go contract harness now samples a seeded graph read with two
  chapters, a dependency edge, and an exploration group, plus chapter-edge project/type/chapter
  list and error cases. It also samples ruler segment summary/full reads for the same seeded
  segment, asserts TS-compatible `narratorStatus` folding from `status` + `substatus`, and covers
  graph/ruler position mutation validation plus persisted side effects. Remaining
  graph/exploration gap is broader endpoint-level smoke across graph/ruler frontend lifecycle
  flows rather than these individual read/error fields.

#### Chapter edges and dependency lifecycle

- **Implemented in Go:** edge list/create/delete supports project/chapter/type filters and direct
  dependency creation/deletion in `go_backend/internal/chapteredges/handlers.go:19-180`; manual
  create/delete is restricted to dependency edges, self/cross-project dependencies are rejected, and
  unsupported metadata fields are stripped. Dependency status and upstream sync exist in
  `go_backend/internal/chapters/handlers.go:375-458`.
- **TS evidence:** TS validates no self-edge, source/target existence, same project, and manual delete
  only for dependency edges in `server/services/chapter-edge-service.ts:12-54`, `123-181`.
- **Fixed 2026-05-25:** Go dependency create now matches TS safety checks for self-edge,
  source/target existence, same-project ownership, and dependency-only manual operations; tests cover
  validation response shape and delete restrictions in `go_backend/internal/chapteredges/handlers_test.go:15-163`.
- **Remaining dependency gap:** endpoint-level proof for upstream sync side effects and commit metadata
  parity is still needed, but invalid graph-edge creation and graph visibility gaps are resolved.

#### Git panel lifecycle

- **Implemented in Go:** status/stage/unstage/commit/discard/diff/stash/log/reset/AI commit message
  are registered in `go_backend/internal/gitroutes/handlers.go:24-36`; implementation shells through
  `go_backend/internal/git/service.go` with output limits and timeouts.
- **TS evidence:** TS git routes cover the same route names in `server/routes/git.ts:42-196` and rely
  on richer git service behavior.
- **Fixed 2026-05-25:** Git discard safety is now TS-compatible for tracked + untracked paths:
  `discardAll` restores tracked files and runs `git clean -fd`, while selected discard inspects
  `git status --porcelain`, restores tracked files, and deletes only selected untracked files. Evidence:
  `go_backend/internal/git/service.go:743-789`, tests in
  `go_backend/internal/git/service_merge_test.go:17-104`. Remaining git-panel polish is P2:
  `ai-commit-message` is still a static placeholder (`go_backend/internal/gitroutes/handlers.go:244-255`)
  and status line counts do not compute full line stats (`go_backend/internal/git/service.go:465-496`).

### P0

- **Project deletion external side effects:** DB/resource cleanup is now implemented and tested in Go;
  remaining replacement risk is proving frontend-visible side effects such as proxy-cache invalidation,
  event/broadcast behavior, and recent-tab/UI cleanup across real sessions. Evidence: Go
  `go_backend/internal/projects/service.go:312-581`,
  `go_backend/internal/projects/service_delete_test.go:24-104`; TS cleanup at
  `server/routes/projects.ts:313-443`.
- **Fork compressed-context quality / event proof:** fork-at-message and `full`/`fresh` inheritance are
  implemented in Go, so the old silent fresh-fork P0 is resolved. Remaining replacement risk is
  compressed-context quality (Go records explicit fallback metadata instead of TS AI summaries), plus
  event-bus and commit-history fixture proof. Evidence: Go
  `go_backend/internal/chapters/service.go:282-444`, `467-538`,
  `go_backend/internal/chapters/fork_parity_integration_test.go:20-193`; TS fork at
  `server/services/chapter-fork.ts:55-397`.
- **Ruler lifecycle semantic parity or explicit degraded mode:** Go has misccompat ruler compatibility
  routes, but they must either reach TS-equivalent lifecycle semantics or clearly advertise degraded
  behavior to the frontend. Evidence: TS `server/routes/ruler.ts:81-733`; Go route registration at
  `go_backend/internal/app/router.go:98-103`, route compatibility handlers at
  `go_backend/internal/misccompat/handlers.go:2046-2054`, and related implementation code at
  `go_backend/internal/misccompat/handlers.go:1652-1891`, `2058-2329`.
- **Batch merge interactive lifecycle:** the Go async merge-session/session-response/event/decision/
  rollback/stale-cleanup path is implemented and capability-gated, but still requires TS-vs-Go
  smoke proof for reconnect, conflict continue/cancel, AI resolution failure, DB/git rollback, and
  frontend progress UI before replacement. Evidence: Go implementation at
  `go_backend/internal/chapters/service.go:925-1188`, handler/session response at
  `go_backend/internal/chapters/handlers.go:156-235`, WS decision handling at
  `go_backend/internal/ws/hub.go:1716-1747`, startup cleanup in
  `go_backend/cmd/narrafork-go/main.go:246-253`, and TS orchestrator at
  `server/services/chapter-batch-merge.ts:81-427`.
- **Chapter dormant/wake lifecycle parity:** root/trunk update invariants were fixed on 2026-05-25,
  but dormant/wake still need TS-vs-Go parity proof for worktree, commit sync, auto-dormant, and event
  side effects. This remains P0 because DB/worktree state divergence can corrupt active/dormant chapter
  lifecycle. Evidence: Go invariant fix in `go_backend/internal/chapters/service.go:1243-1256` and
  `go_backend/internal/chapters/service_json_test.go:71-180`; remaining dormant/wake implementation in
  `go_backend/internal/chapters/service.go:1304-1414`; TS lifecycle in
  `server/services/chapter-service.ts:278-468`.
- **Merge lifecycle data-integrity protections:** add source dirty auto-commit behavior, required-review
  stale/clean commit validation, target/source worktree locking, source worktree cleanup after merge,
  commit sync, event broadcasts, and merge-summary injection. This is P0 because merging without these
  protections can lose uncommitted source state, accept stale review approvals, or leave DB/git/worktree
  state inconsistent. Evidence: Go `go_backend/internal/chapters/service.go:612-811`; TS
  `server/services/chapter-merge.ts:72-185`, `232-659`.

### P1

- **Review lifecycle parity:** dirty source state copy, rich diff context, ConcludeReview persistence,
  and basic dismiss/convert/promote compatibility are implemented and tested. Remaining P1 work is full
  TS-style auto-start through the narrator loop, event bus parity, stronger rollback after narrator
  initialization, and endpoint-level proof for review worktree reset-on-mutation. Evidence: Go
  `go_backend/internal/chapters/service.go:820-923`, `2862-3008`,
  `go_backend/internal/narrators/review_parity_test.go`,
  `go_backend/internal/misccompat/review_parity_test.go`; TS `server/services/review-service.ts:31-670`.
- **Fixed 2026-05-25 — Dependency edge validation:** Go now rejects self/cross-project dependency
  edges, rejects manual system-owned edge create/delete, strips unsupported metadata fields, and
  preserves TS-compatible `{error, code}` validation responses. Evidence:
  `go_backend/internal/chapteredges/handlers.go:110-180`, tests in
  `go_backend/internal/chapteredges/handlers_test.go:15-163`; TS baseline
  `server/services/chapter-edge-service.ts:12-54`.
- **Unmerge parity:** sync target/source commits, clean merge summary messages, and emit reload/wake
  events as TS does. Evidence: Go `go_backend/internal/chapters/service.go:829-917`; TS
  `server/services/chapter-merge.ts:690-830`.
- **Fixed 2026-05-25 — Git panel discard safety:** Go `discardAll` already matches TS tracked +
  untracked cleanup, and `discardFiles` now also mirrors TS by splitting selected paths from
  `git status --porcelain`, restoring tracked files and deleting selected untracked files without
  removing unselected untracked paths. Evidence: Go `go_backend/internal/git/service.go:743-789`,
  tests in `go_backend/internal/git/service_merge_test.go:62-104`; TS baseline
  `server/services/git-service.ts:1157-1188`. Note: added tests compile, but executing `TestDiscard*`
  was blocked by the tool safety layer because it intentionally runs `git checkout`/`git clean` inside a
  temporary repository.

### P2

- **Project create backup lifecycle:** initialize `.narrafork/project.db`, ensure `.gitignore`, and
  commit ignore entry during Go project creation. Evidence: TS `server/routes/projects.ts:237-241`;
  Go explicit backup sync/import at `go_backend/internal/projects/service.go:219-341` but no
  create-time initialization.
- **Split/cherry-pick enhancements:** split-at-commit prefix/continuation semantics now exist in Go
  and have focused coverage, but still need broader TS-vs-Go fixture proof across real histories and
  compressed AI summary fallback quality. `cherry_pick` still needs edge/history recording comparable to
  TS edge model. Evidence: Go split/cherry-pick in `go_backend/internal/chapters/handlers.go:365-409`,
  `go_backend/internal/chapters/split_at_commit_test.go:21-277`; TS fork/split design and edge model.
- **Graph polish:** upstream dependency updates are now surfaced in graph data and Graph position
  updates accept TS ruler `width`/`height` aliases, with explicit `panelWidth`/`panelHeight` taking
  precedence. Remaining graph polish is endpoint-level smoke against frontend/ruler flows. Evidence:
  Go graph `go_backend/internal/graph/handlers.go:101-129`, `236-292`, tests in
  `go_backend/internal/graph/handlers_test.go:21-168`, `306-349`; TS ruler positions
  `server/routes/ruler.ts:436-458`.
- **Git panel polish:** replace placeholder AI commit message and improve line/stat parity. Evidence: Go
  `go_backend/internal/gitroutes/handlers.go:244-255`, `go_backend/internal/git/service.go:465-496`.

### Acceptance Criteria

- **Projects:** create/update/delete/import/backup smoke tests prove DB rows and filesystem side
  effects match TS, including root chapter creation, backup DB setup, `.gitignore`, project deletion
  of all chapter/narrator/terminal/container/merge/exploration resources, and no orphan worktrees.
- **Chapters:** create, update, delete, cleanup, dormant, and wake enforce TS invariants; root/trunk
  cannot be invalidly mutated; status transitions create/remove worktrees and emit/broadcast the same
  observable lifecycle outcomes as TS or are explicitly unsupported.
- **Fork/split:** Go either implements `full`/`compressed`/`fresh`, `forkAtMessageUuid`, file snapshot
  replay, commit copy, copyFiles, containers/startup script, and edge metadata, or rejects unsupported
  modes before mutating git/DB.
- **Merge/batch/review:** merge, AI resolve, unmerge, batch merge, and review pass TS-compatible smoke
  flows including conflict handling, stale review protection, merge summaries/events, interactive
  batch decisions, rollback, review conclusion/promote/dismiss/convert, and no git/DB divergence after
  forced failures.
- **Ruler:** Go ruler compatibility routes are smoke tested against TS lifecycle expectations,
  especially temp-chapter AI conflict resolution and merge/rebase semantics, or their partial/degraded
  behavior is explicit enough that the frontend cannot enter a misleading broken lifecycle.
- **Graph/edges/git:** graph returns dependency update state and compatible positions; dependency
  edge validation remains covered by Go parity tests; git panel operations match route semantics and
  safety expectations, with exact response-shape/API details coordinated with API Contract Parity and
  Frontend Contract / Smoke Tests.

### Notes

- Evidence is code-read only; no DB, migrations, tests, or runtime commands were executed.
- Several gaps cross tracks: response shapes/status codes belong to API Contract Parity; review/AI
  resolver and fork inherit-mode behavior also overlap Narrator / Agent Parity; event bus/WebSocket,
  terminal, container, proxy-cache, and filesystem side effects overlap Runtime / System Integration
  Parity; UI smoke coverage belongs to Frontend Contract / Smoke Tests.


## Track: Narrator / Agent Parity

Owner: spec-narrator
Status: Done
Last update: 2026-05-14

### Scope

- Compared narrator REST/SSE/WS behavior, message storage, tool call persistence, permission system, plan mode, subagents, hooks, sidecars, compact/recovery, usage/cost, provider runtime, buffers, and goals.
- Investigation scope was limited to `server/routes/narrators.ts`, `server/services/narrator-*.ts`, `server/services/subagent-*.ts`, `server/lib/agent/**`, `go_backend/internal/narrators/*`, `go_backend/internal/agentbasic/*`, and `go_backend/internal/ws/*`.

### Verified Findings

- **Route surface is broad but still compatibility-oriented, not full TS parity.** Go registers many narrator routes including messages, permissions, goals, buffer, custom traits, compact, plan mode, fork, retry, background tasks, browser/session placeholders, and subagent detach (`go_backend/internal/narrators/handlers.go:32-141`). Several implementations are simplified direct SQL or stubs (`leave` returns `{ok:true}` at `handlers.go:1843-1845`; `notImplemented` helper at `handlers.go:1738-1740`). TS route imports show the original route delegates to many session services for buffered messages, active sessions, compact, permission reprocessing, temporary model restore, plan reflection, and edit/regenerate (`server/routes/narrators.ts:146-177`).
- **Message storage parity is partial.** Go uses `narrator_messages` + `narrator_message_refs`, supports seq cursor pagination, top-level filtering, child messages via `parent_tool_use_id`, and tree building (`go_backend/internal/narrators/service.go:451-508`, `624-668`, `671-752`). Go also copies refs during fork (`service.go:196-256`, `363-378`) and hydrates stored sidecars into message/tool/content-block responses. TS has the same core junction-table pattern and message-version bumping (`server/services/narrator-persistence.ts:51-129`, `157-199`) plus richer catch-up cursor/child-anchor support and sidecar-aware message loading (`server/services/narrator-messages.ts:118-222`). Remaining message-storage risk is broader catch-up cursor/child-anchor parity, creator enrichment for all paths, and live fixture proof rather than absence of content-block storage/replay.
- **Agent history replay is narrowed but still fixture-gated.** Go no longer builds provider history from `content_text` only: `agentHistory` replays stored `content_json`, reconstructs assistant `tool_use`, user `tool_result`, completed `narrator_tool_calls`, supported reasoning/redacted thinking, compact summary boundaries, sidecar text, trailing tool results, uploaded user images as provider-visible base64 image inputs, `text_file` attachment hints from provider-effective `contentText`, native `web_search` context/Responses items, OpenAI Responses `image_generation_call` replay, and TS-compatible `parentToolUseId` filtering/subagent top-level replay (`go_backend/internal/narrators/service.go:2991-3044`; `go_backend/internal/narrators/agent_history_content.go`; `go_backend/internal/agentbasic/history_content.go`; `go_backend/internal/agentbasic/provider_openai_chat.go`; focused tests in `go_backend/internal/narrators/agent_history_content_test.go` and `go_backend/internal/agentbasic/history_content_test.go`). Remaining replay gaps are TS-created long-session fixture smoke and provider-specific live smoke for rich/native blocks rather than the previous core tool transcript loss.
- **Tool call persistence is partial.** Go records running/pending/completed `narrator_tool_calls`, input/output JSON, file snapshots, and links results to the assistant draft (`go_backend/internal/narrators/agent_event_handler.go:227-274`; permission records in `service.go:928-966`). Go now blocks malformed/truncated tool input (including `_raw`) and empty `Write`/`Edit` input before execution, records failed tool results, and runs `PreToolUse` hooks before those malformed/empty-input guards so hook payloads can inspect `_raw` like TS. Remaining gaps are TS-level metadata around progress/long-running output streaming, redirected inputs outside covered plan-mode paths, usage/cost linkage, and full permission suggestion lifecycle. TS executor explicitly performs permission handling, `PreToolUse` hooks, malformed/truncated JSON guards, progress/long-running events, throttled output streaming, and redirected-input tracking (`server/lib/agent/tool-executor.ts:145-308`).
- **Permission behavior is functional but not equivalent.** Go supports pending tool calls, REST/WS decisions, answers merged into `input_json`, a 5-minute wait, danger reflection, status broadcasts, the TS ask-in-passing read-only permission lock, malformed/empty write-input guards, TS-ordered `PreToolUse` before malformed guards, subagent permission broadcasts targeted at the parent narrator, TS-compatible permission-gated readOnly `Agent` restrictions, TS-compatible permission-rank gating for `Send`, TS-style mode-only gating for `TeamStatus`/`AddGoal`/`Terminal`, TS-style path-aware fallback for read-only path tools and unknown tools, hard-deny protection for `.git` write targets including common Bash destructive/redirection writes, Bash destructive hard-deny for project root / root ancestors / `.worktrees` when `project.git_path` is available, mode-sensitive Bash command whitelist decisions, workdir-aware Bash command path blacklist/whitelist sensitivity, catastrophic Bash hard-deny subset, safe Bash readOnly/acceptEdits mode behavior, TS-compatible WebFetch bypass/dontAsk mode behavior, AskUserQuestion always-ask event bookkeeping, plan-mode Markdown write redirection to the designated plan file, first-attempt plan-mode soft-deny with relaxed-plan promotion after approval, TS-style `ExitPlanMode` user approval before leaving plan mode, and bypass-mode reprocessing for already-pending tool calls (`go_backend/internal/narrators/service.go:914-966`; `go_backend/internal/narrators/handlers.go:1504-1533`; `go_backend/internal/ws/hub.go:216-312`). It still does not fully match TS reflection loop control, full shell AST structural-path analysis, the full permission modes/whitelist/blacklist matrix, or all permission hooks imported by TS session routing (`server/routes/narrators.ts:137-177`).
- **Plan mode core safety is implemented but still smoke-gated.** Go now preserves `previous_permission_mode`/`plan_file_id`, toggles plan traits and read-only mode, redirects Markdown `Write`/`Edit` calls to the designated plan file, supports first-attempt soft-deny approval that promotes `relaxed_plan`, requires user approval before `ExitPlanMode` leaves plan mode, persists edited-plan/diff metadata, honors `compactAfter`, cancels pending exit approvals on manual exit, and restarts with an explicit continuation prompt after approval. Remaining plan-mode risk is broader TS-vs-Go live-session smoke and edge-case polish, not the core write-redirection or plan-exit approval contract (`go_backend/internal/narrators/service.go`, `go_backend/internal/narrators/handlers.go`, `go_backend/internal/narrators/agent_event_integration_test.go:927-1125`, `go_backend/internal/narrators/service_test.go:103-193`).
- **Subagent parity is now targeted but still not full TS-native runtime parity.** Go implements `Agent`, `Send`, `Await`, `TeamStatus`, foreground/background child message trees, detach-to-background parent unblocking, attach/await parent-blocking semantics, background result injection into parent tool calls, stale running recovery, TS-compatible readOnly `Agent` permission restrictions for general vs explore/plan subagents, TS-compatible `Send` auto-allow gating by caller/target permission rank, and TS-style `TeamStatus` mode gating before team messages are delivered. TS still has richer abort-controller swapping, team inbox/file-change state, custom subagent prompts, conclusion-file permission exceptions, recovery/compact retry, and more detailed broadcasts (`server/services/subagent-runner.ts:27-54`, `61-169`, `177-220`; `server/services/subagent-executor.ts:33-46`, `85-131`, `182-240`).
- **Hook runtime parity is narrowed but not complete.** Go now executes enabled `PreToolUse` command/http hooks during narrator tool execution with fail-open semantics and explicit block support, matching the TS executor's blocking hook gate (`server/lib/agent/tool-executor.ts:181-204`). The ordering now matches TS as well: permission resolves first, then `PreToolUse` sees truncated/malformed raw input (`tool_input._raw`) before Go's malformed/empty-input safety guard rejects execution. Go also triggers `PostToolUse` command/http hooks after tool completion with TS-shaped payload fields (`tool_output`, `tool_is_error`) and non-blocking/fail-open semantics, matching the TS post-tool hook behavior (`server/lib/agent/tool-executor.ts:351-367`). Remaining hook gaps are broader live-session smoke coverage and any future event-bus transport parity beyond current WS `hook:executed` telemetry.
- **Sidecar parity is narrowed but still incomplete.** Go now hydrates existing `narrator_sidecars` rows into message responses, tool-call records, `tool_use` content blocks, and tool-call detail responses so frontend sidecar notices can render. Go also formats hydrated sidecars into model-visible `<side_car>` blocks for user-message context and tool-result output, the Go agent event handler can persist/broadcast sidecars supplied by `tool_result` / `sidecars` events, Go runtime injects TODO reminder sidecars into the same active loop's fourth completed non-`TaskCreate` tool result, silent-progress reminders are generated after consecutive tool calls without visible text, relaxed-plan reminders are generated for non-read-only tools during relaxed plan mode, and the generic `after_tools` user-message sidecar channel is available at provider boundary. Buffered message sidecars now consume command text, image blocks, and text-file attachment hints instead of only pure text, and the busy REST buffer path persists multipart text files under `~/.narrafork/buffered-files`, stores recoverable metadata, rematerializes attachments into the worktree on consumption, and avoids duplicate attachment hints. Remaining gaps are full tree-sitter edge-case parity for relaxed-plan Bash command classification, broader subagent-side collector breadth, and live cross-platform smoke beyond focused background Bash process-tree tests (`server/lib/agent/sidecar.ts:1-60`; `server/services/narrator-session.ts:1463-1519`; `server/services/subagent-executor.ts:491-575`).
- **Compact exists but is not TS-equivalent.** Go can create compact/segment compact marker messages, set `is_compact`, update `prune_boundary_message_id`, and inject the most recent compact summary into future history (`go_backend/internal/narrators/handlers.go:867-899`, `1605-1633`; `go_backend/internal/narrators/service.go:1112-1143`, `1165-1183`). TS has locks, background/blocking compact modes, context-overflow recovery, prune boundary handling, custom compact, and segment operations wired through session services (`server/services/narrator-session-state.ts:286-301`; `server/services/narrator-recovery.ts:74-115`; `server/routes/narrators.ts:155-168`). Go compact is acceptable as a compatibility marker only, not a replacement for TS recovery.
- **Buffers still differ; goals REST/realtime/runtime parity is narrowed but not complete.** Go exposes buffer CRUD and goals CRUD (`go_backend/internal/narrators/handlers.go`). TS buffers are runtime-aware, cap queues, persist text files under `~/.narrafork/buffered-files`, support priority front insertion, and coordinate soft-stops (`server/services/narrator-buffer.ts:17-23`, `60-183`). Go goal REST mutations now align with the TS open-goal lifecycle for create/update/remove/clear/reorder and broadcast `goals_set` for frontend consumers, with focused coverage in `go_backend/internal/narrators/handlers_patches_test.go`. Go also persists/broadcasts bounded `goal_continuation` turns for active goals, injects them into provider history, and accounts active-goal token/time counters from provider usage snapshots with a `goals_set` broadcast after updates. Remaining goal gaps are creator enrichment, relaxed-plan auto-enable/deeper permission interaction, and TS-vs-Go smoke under longer live narrator runs (`server/services/narrator-goal-service.ts:102-153`, `156-188`, `190-240`).
- **Usage/cost parity is incomplete but runtime request/message accounting is narrower.** Go provider events define usage snapshots, metering, API request events, and broadcasts (`go_backend/internal/agentbasic/provider_types.go:56-85`, `107-173`; `go_backend/internal/narrators/agent_event_handler.go`), and route `/usage-stats` loads DB stats (`go_backend/internal/narrators/handlers.go`). Go now updates active `narrator_goals.tokens_used` / `time_used_seconds`, emits provider-call API request lifecycle events around real adapter calls, persists `api_requests`, links them to the assistant message, writes assistant message usage metadata for available columns, returns a TS-shaped narrator usage summary including direct subagents by default, serves the global usage-history list/detail/stats/timeseries with frontend fields plus fail-open behavior for missing `api_requests`/usage columns, and honors request-dump settings when provider events include raw dumps. Remaining usage/cost gaps are tool-call usage metadata, provider-adapter raw-dump capture coverage, credential/cost accounting breadth, non-narrator request kinds, and live-provider usage-history smoke breadth comparable to TS usage/cost services imported by TS routes (`server/routes/narrators.ts:185`). Note: TS currently increments `narrators.total_cost_usd` with `0` in normal session updates, so Go should not invent a different legacy-total-cost aggregation unless the shared contract changes.
- **Provider runtime gap remains P0/P1 depending on configured models.** Go has provider adapters and routing for Anthropic, OpenAI, Kiro, Codex, NKP/NUG/Cline-style providers and fallback logic (`go_backend/internal/agentbasic/provider_resolver.go:20-61`, `63-220`), but TS provider runtime has richer provider-specific features in `server/lib/agent/**` including sidecars, request dumps, recovery, pipeline state, image generation, and provider-specific event semantics. Go's own resolver still treats unsupported provider prefixes via fallback/error (`provider_resolver.go:50-60`), so replacement safety depends on explicit supported-provider smoke tests.
- **Rollback/edit-regenerate are now active Go routes, not disabled fallbacks.** Go now truncates `narrator_message_refs`/message blocks, marks affected tool calls stale, optionally rebuilds file state from snapshots, preserves shared-message safety with copy-on-write user edits, broadcasts message updates/deletions, and can optionally rerun the agent after an edit (`go_backend/internal/narrators/rollback_edit.go`; focused coverage in `go_backend/internal/narrators/rollback_edit_test.go`). Remaining work is broader TS-vs-Go fixture comparison across provider/runtime edge cases, not a `FEATURE_DISABLED` fallback.

### P0

- **P0-A: Preserve full content-block/tool transcript history.** Core `content_text`-only replay risk is resolved: Go reconstructs provider history from stored `content_json`, completed tool-call rows, compact summaries, sidecars, uploaded image blocks, `text_file` attachment hints, native `web_search` context/Responses items, OpenAI Responses `image_generation_call` items, TS-compatible `parentToolUseId` filtering/subagent replay, and supported reasoning/redacted blocks (`go_backend/internal/narrators/service.go:2991-3044`; `go_backend/internal/narrators/agent_history_content.go`; `go_backend/internal/agentbasic/history_content.go`; `go_backend/internal/narrators/agent_history_content_test.go`; `go_backend/internal/agentbasic/history_content_test.go`). Remaining P0-A replacement risk is TS-created long-session smoke and provider-specific live smoke before claiming transparent replacement for existing tool-heavy narrators.
- **P0-B: Implement TS-equivalent permission safety for writes/exec.** Go must match TS permission modes, whitelist/blacklist behavior, plan-mode semantics, and denial/error persistence before it can safely execute destructive tools. Malformed/truncated input guards, empty `Write`/`Edit` guards, TS-ordered `PreToolUse` before malformed/empty-input guards, permission-gated readOnly `Agent` restrictions, permission-rank-gated `Send`, mode-gated `TeamStatus`/`AddGoal`/`Terminal`, path-aware fallback for external read paths and unknown tools, `.git` write hard-deny protection including common Bash destructive/redirection writes, Bash structural hard-deny for project root / root ancestors / `.worktrees`, mode-sensitive Bash command whitelist behavior, workdir-aware Bash command path blacklist/whitelist sensitivity, catastrophic Bash hard-deny subset, safe Bash readOnly/acceptEdits mode behavior, TS-compatible WebFetch bypass/dontAsk mode behavior, AskUserQuestion/ExitPlanMode always-ask event bookkeeping, Markdown plan-file redirection, and same-narrator bypass-mode pending-permission reprocessing are now covered, but broader TS hook/permission lifecycle and full shell AST parity remain open (`server/lib/agent/tool-executor.ts:145-308`).
- **P0-C: Plan mode safety contract.** Go now preserves core `previousPermissionMode`/`planFileId` state, redirects Markdown `Write`/`Edit` calls to the plan file, supports first-attempt soft-deny approval that promotes to relaxed plan, requires user approval before `ExitPlanMode` leaves plan mode, refreshes approved tool input so edited plans are applied, stores approved-plan diff metadata, honors `compactAfter` for plan compact persistence, emits TS-style `continueMode`/`continuePrompt` metadata, cancels pending `ExitPlanMode` approvals on manual plan-mode exit, and aborts/restarts after approved `ExitPlanMode` with an explicit continuation user prompt. Remaining gaps are broader live-session TS-vs-Go smoke coverage and edge-case polish rather than the core approval orchestration.
- **P0-D: Provider/runtime allowlist or parity.** If Go is used as replacement, it must either fully support the configured provider/runtime semantics or reject unsupported prefixes before run start with clear UI fallback. Go now defaults to fail-fast for unsupported/unconfigured Kiro/Codex prefixes instead of silently falling back to a different provider; legacy automatic fallback is preserved only behind explicit `agent.allowGoProviderFallback=true` (`go_backend/internal/agentbasic/provider_resolver.go:52-63`, `go_backend/internal/agentbasic/openai_test.go`). The narrator POST path is covered by a regression test that persists `status=idle`, `substatus=["error"]`, and zero fallback provider HTTP requests for unsupported Kiro (`go_backend/internal/narrators/agent_event_integration_test.go`). Remaining replacement risk is broader live provider/runtime smoke and frontend messaging polish for fail-fast errors.

### P1

- **P1-A: Subagent execution parity.** Go now covers detach/attach parent-blocking, background task persistence/notifications, result injection, Send/Await continuation, and stale recovery. Remaining TS parity gaps are abort-controller source swapping, custom subagent prompts, conclusion-file permission exception, richer subagent buffers, team inbox/file-change semantics, and compact/recovery polish (`server/services/subagent-runner.ts:27-54`, `61-169`; `server/services/subagent-executor.ts:85-131`, `182-240`).
- **P1-B: Hooks and sidecars.** `PreToolUse` command/http hook execution/blocking semantics, TS-ordered hook execution before malformed/empty-input guards, and `PostToolUse` command/http non-blocking/fail-open payload delivery are now implemented for narrator tools, and Go emits TS-shaped `hook:executed` telemetry after hook execution. Stored/event-supplied sidecars are persisted, broadcast, hydrated into message/tool/detail responses, and injected into model-visible user/tool history. TODO reminder sidecars now also enter the same active loop on the fourth completed non-`TaskCreate` tool result, silent-progress reminders are generated after consecutive tool calls without visible text, relaxed-plan reminders are generated for non-read-only tools during relaxed plan mode, buffered message sidecars now carry command text plus image/text-file attachment metadata, and the generic `after_tools` user-message sidecar channel is wired through provider history. Remaining gaps are event-bus vs WS transport exactness if a future Go event bus is added, full tree-sitter edge-case parity for relaxed-plan Bash command classification, broader subagent collector breadth, and live OS smoke beyond focused background Bash process-tree tests (`server/lib/agent/tool-executor.ts:181-204`, `server/lib/agent/tool-executor.ts:351-367`; `server/lib/agent/sidecar.ts:1-60`; `server/services/narrator-session.ts:1463-1519`).
- **P1-C: Compact/recovery parity.** Add compact locks, background vs blocking compact behavior, context-overflow recovery, retry-after-compact, and exact prune-boundary semantics; current Go compact is a marker/summary implementation (`go_backend/internal/narrators/handlers.go:1605-1633`).
- **P1-D: Buffer runtime semantics.** Go now covers TS-style max queue limits, priority front insertion, busy-run slash-command command text, persisted multipart text-file attachments with worktree rematerialization, exact-ID reorder validation, zero-based reorder sequence persistence, `buffer_set` broadcasts after update/remove/clear/reorder, and priority-only after-tools cut-in (non-priority buffers now wait for run completion); remaining buffer runtime risk is full active-run soft-stop equivalence and multi-device/WS smoke behavior (`server/services/narrator-buffer.ts:17-183`).
- **P1-E: Usage/cost accounting.** Active-goal token/time counters, provider-call `api_requests`, assistant message usage metadata, narrator `/usage-stats`, global usage-history list/detail/stats/timeseries response shapes, and request-dump persistence policy now expose TS-compatible summary/record fields for frontend usage surfaces and degrade safely on missing usage columns. Remaining usage parity is credential metadata breadth, tool-call usage metadata, provider-adapter raw-dump capture coverage, non-narrator request kinds, and live-provider/admin usage-history smoke breadth beyond the narrator request/message counters.
- **P1-F: Message catch-up and WS event parity.** Go now has focused WS coverage for subscribe catch-up, `streaming_snapshot`, `sync_check`/`sync_ok`/`full_reload`, presence, permission resolution, legacy tool event conversion, preservation of TS optional tool fields (`streamStartedAt`, `parentToolUseId`, `updatedInput`, `metadata`, `sideCars`) in frontend-compatible events, REST goal mutations broadcasting `goals_set`, and bounded `goal_continuation` message/event/runtime turns. Remaining parity gaps are broader event families and TS-vs-Go smoke for `subagent_started`, `subagent_status_changed`, compact progress, tool progress/long-running output, quota/queue, browser/session counts, real reconnect behavior under active runs, and longer active-goal continuation/accounting flows.
- **P1-G: Rollback/edit-regenerate fixture breadth.** Focused Go tests now cover refs/block truncation, file-state rebuild and skip-revert paths, copy-on-write edits, image-block preservation, child subagent detachment, and optional agent rerun; add TS-vs-Go fixture diffs for longer tool chains, compact boundaries, provider recovery, and failure compensation before claiming full replacement parity.

### P2

- **P2-A: Browser/ask-in-passing/custom traits polish.** Go exposes related route names, but parity should be smoke-tested or explicitly scoped as compatibility-only.
- **P2-B: Commands/skills/tool menu fidelity.** Go returns a simplified command/tool menu and reads global skills (`go_backend/internal/narrators/handlers.go:1983-2115`); align with TS slash command resolution and project skill behavior when replacement moves beyond compatibility beta.
- **P2-C: Title generation, retry recovery polish, and fork compressed context quality.** Go implements some route shapes, but should be tested against TS response shapes and user-facing flows.
- **P2-D: Provider-specific nice-to-haves.** Request dumps, image-generation artifacts, queue/quota UX, and model-switch metadata should be matched after P0/P1 safety issues are resolved.

### Evidence

- TS narrator route imports many session-level capabilities: `server/routes/narrators.ts:146-177`.
- TS message persistence/junction refs: `server/services/narrator-persistence.ts:51-129`, `157-199`.
- TS message tree/catch-up support: `server/services/narrator-messages.ts:118-222`.
- TS permission/hook/streaming executor: `server/lib/agent/tool-executor.ts:145-308`.
- TS sidecars: `server/lib/agent/sidecar.ts:1-60`.
- TS plan mode: `server/services/narrator-plan-mode.ts:28-147`.
- TS buffers/goals/subagents: `server/services/narrator-buffer.ts:17-183`, `server/services/narrator-goal-service.ts:102-240`, `server/services/subagent-runner.ts:27-220`, `server/services/subagent-executor.ts:85-240`.
- Go route surface: `go_backend/internal/narrators/handlers.go:32-141`.
- Go message/fork/history: `go_backend/internal/narrators/service.go:196-256`, `451-508`, `624-752`, `1105-1162`.
- Go agent run/tool/permission/subagent paths: `go_backend/internal/narrators/service.go:888-1022`, `1360-1738`.
- Go tool-call/event persistence: `go_backend/internal/narrators/agent_event_handler.go:27-99`, `227-274`.
- Go WS permission and broadcast hub: `go_backend/internal/ws/hub.go:488-600`, `1229-1818`; focused WS contract tests in `go_backend/internal/ws/hub_narrator_events_test.go` and `go_backend/internal/ws/frontend_contract_smoke_test.go`.
- Go provider/runtime types and resolver: `go_backend/internal/agentbasic/provider_types.go:56-85`, `107-173`; `go_backend/internal/agentbasic/provider_resolver.go:20-61`, `63-220`.

### Acceptance Criteria

- **Replacement candidate:** all P0 items pass automated or scripted smoke tests using an existing TS-created narrator with tool calls, compact marker, plan mode, pending permission, buffered message, goal, and a subagent tree; no data loss and no unsafe tool execution.
- **Compatibility beta:** if P0-D provider parity is not complete, Go must expose an explicit supported-provider allowlist and fail unsupported narrator models before starting a run, with frontend-visible fallback messaging.
- **Message acceptance:** `GET /api/narrators/:id/messages` and agent replay preserve `content_json` block semantics, nested subagent messages, tool-call IDs, message refs/seq, compact boundaries, and `messageVersion` increments across delete/retry/fork.
- **Permission acceptance:** write/exec tools in default, read-only, plan, and deny modes produce the same pending/approved/denied/cancelled DB rows and WS events as TS; denied or malformed inputs cannot mutate files.
- **Subagent acceptance:** foreground, background, detached, interrupted, messaged, awaited, team-status, file-change, and conclusion-file flows match TS-visible statuses/events and persist background task results.
- **Usage acceptance:** streamed provider usage updates persist to narrator total cost/usage history and per-goal token/time counters, and `/usage-stats` reflects the latest run without requiring TS backend.

### Notes

- Current Go implementation is a useful compatibility backend for simple chat/tool sessions, and core content-block/tool replay plus core plan-mode/provider fail-fast safety are now implemented, but it remains **unsafe as a transparent replacement** for TS in projects relying on provider-specific rich blocks, broader permission/hook lifecycle parity, subagent edge cases, compact/recovery, or runtime side effects without fixture smoke evidence.
- Suggested execution order: P0-B permission/hook lifecycle + plan-mode live smoke → P0-A provider-specific/long-session transcript fixture smoke → P0-D live provider runtime smoke → P1 subagents/hooks/compact/usage → P2 UI polish.

## Track: Runtime / System Integration Parity

Owner: spec-runtime
Status: Done
Last update: 2026-05-14

### Scope

- Compared TS and Go runtime/system surfaces for WebSocket protocols, terminal runtime,
  containers/podman/proxy/ports, uploads/shares/storage/fs, settings/admin/update/runtime,
  providers, MCP, hooks, notifications, gateway, VNet, benchmarks, and volume snapshots.
- Evidence was limited to repository source paths; no server, database, Podman, migration, or
  network runtime was started.

### Findings

- **WebSocket route coverage is still partial, but VNet and terminal reconnect scaffolding are no longer missing.** Go exposes
  `/ws/narrator`, `/ws/terminal`, and `/ws/vnet`; the VNet route is a Go relay hub with JWT/relay-token
  auth, disabled-setting fallback, peer cleanup, and message-size limits, while UDP rendezvous remains
  explicitly unsupported. Go terminal WS now covers subscribe/input/resize/kill/create/rename plus
  raw-capped snapshot/scrollback/bufferState replay, snapshot fallback payloads, last-input-client resize
  gating, and backpressure tests. Remaining WebSocket replacement risk is exact TS event/replay fidelity,
  not absent frontend-consumed terminal events. Evidence: `server/websocket/ws-handler.ts:37`,
  `server/websocket/ws-handler.ts:57`, `server/websocket/ws-handler.ts:81`,
  `server/websocket/terminal-ws.ts:78`, `server/websocket/terminal-ws.ts:106`,
  `server/websocket/terminal-ws.ts:159`, `go_backend/internal/app/router.go:192`,
  `go_backend/internal/app/router.go:194`, `go_backend/internal/ws/hub.go:217`,
  `go_backend/internal/ws/hub_terminal_test.go:100`,
  `go_backend/internal/ws/hub_terminal_test.go:189`,
  `go_backend/internal/ws/vnet_relay_test.go:12`, `go_backend/internal/ws/vnet_relay_test.go:59`.
- **Terminal runtime is functional but not TS-equivalent.** TS uses dtach when available, persistent
  `BufferManager` scrollback, platform-specific Bun/portable PTY runtimes, event-bus lifecycle
  events, reattach after detach/restart, and graph-state/admin helpers. Go uses `creack/pty` with an
  in-memory capped raw replay buffer, emits output/exit/requestResize/scrollback/bufferState, updates DB
  status, and exposes capability metadata for raw replay and last-input-client resize. It still does not
  provide TS-equivalent persistent BufferManager/xterm serialized replay across process restarts, full
  dtach detach/reattach parity on every platform, or TS lifecycle events. Evidence:
  `server/services/terminal-service.ts:171`, `server/services/terminal-service.ts:272`,
  `server/services/terminal-service.ts:277`, `server/services/terminal-service.ts:301`,
  `server/services/terminal-service.ts:337`, `server/services/terminal-service.ts:504`,
  `go_backend/internal/terminalruntime/manager.go:38`,
  `go_backend/internal/terminalruntime/manager.go:76`,
  `go_backend/internal/terminalruntime/manager.go:147`,
  `go_backend/internal/terminalruntime/manager.go:163`.
- **Container routes exist, but orchestration/proxy/port parity still needs end-to-end proof.** TS configures
  rootless Podman environment/cgroup fallback, allocates ports, derives proxy labels/URLs, runs an
  in-process container proxy cache, and emits lifecycle events. Current Go has narrowed the older proxy
  gap: compose-backed live listing derives `proxyLabel`, `proxyUrl`, `containerIp`, and proxy-mode
  `containerPort`, persists them, maintains `port_allocations`, and starts a Go reverse-proxy HTTP/WS
  server when container proxy settings are enabled. Remaining risk is TS-vs-Go parity proof for rootless
  setup/cgroup behavior, proxy cache reconciliation, port allocation/release edge cases, and lifecycle
  events rather than a complete absence of proxy metadata/runtime. Evidence:
  `server/services/container-service.ts:45`, `server/services/container-service.ts:70`,
  `server/services/container-service.ts:15`, `server/services/container-proxy.ts:38`,
  `go_backend/internal/chapters/handlers.go:1072`,
  `go_backend/internal/chapters/handlers.go:1107`,
  `go_backend/internal/chapters/handlers.go:1165`,
  `go_backend/internal/containerproxy/manager.go:100`,
  `go_backend/internal/containerproxy/manager_test.go:47`,
  `go_backend/internal/chapters/container_runtime_test.go:151`.
- **Uploads/shares/storage/fs are compatibility implementations with different guarantees.** TS
  shares are ephemeral in memory and cleaned on restart. Current Go share handling is no longer a raw
  unknown-directory fallback: startup calls `CleanupStaleShares`, POST share creation is mounted behind
  auth, public GET only resolves an in-memory record or same-root metadata file, expired metadata removes
  disk content, and focused tests cover expired/stale share behavior. Seeded upload serving and public
  share missing-state baselines are now also covered by the TS-Go harness. This is still a Go-only persistent
  metadata design rather than TS-equivalent ephemeral shares, so replacement needs an explicit product
  decision plus smoke evidence for restart/expiry/privacy behavior. Go storage scan/cleanup is present
  and conservative for worktrees/containers; TS has admin-gated storage cleanup/scan services. Evidence:
  `server/lib/shares.ts:18`, `server/lib/shares.ts:106`, `server/routes/storage.ts:13`,
  `server/routes/storage.ts:91`, `go_backend/cmd/narrafork-go/main.go:164`,
  `go_backend/internal/app/router.go:85`, `go_backend/internal/misccompat/handlers.go:674`,
  `go_backend/internal/misccompat/handlers.go:869`,
  `go_backend/internal/misccompat/handlers.go:938`,
  `go_backend/internal/misccompat/share_security_test.go:431`.
- **Settings/admin/update/runtime/providers are broad but partly fallback/degraded.** Go registers
  settings/admin/update/runtime/provider routes and many provider compatibility endpoints, but update
  self-apply explicitly returns unsupported, version metadata is Go-specific, and some provider/model
  behavior depends on cached settings rather than TS live runtime behavior. Evidence:
  `go_backend/internal/app/router.go:129`, `go_backend/internal/app/router.go:130`,
  `go_backend/internal/app/router.go:131`, `go_backend/internal/app/router.go:137`,
  `go_backend/internal/systemroutes/handlers.go:648`,
  `go_backend/internal/systemroutes/handlers.go:654`,
  `go_backend/internal/systemroutes/handlers.go:788`,
  `go_backend/internal/settings/handlers.go:51`.
- **MCP, gateway, VNet relay mode, and benchmarks are not full TS runtimes; volume snapshots still need positive Podman smoke evidence.** TS MCP uses
  `mcpManager` to connect/disconnect/test external servers and list live external tools; Go keeps an
  in-memory MCP state and embedded tools. Go gateway fallback remains explicit; Go VNet is an active
  WebSocket relay runtime but is capability-labeled relay-only with UDP rendezvous disabled. Volume
  snapshots are no longer metadata-only in Go: current code accepts the frontend `chapterId` field,
  creates/applies/deletes real Podman tar archives, rejects unsafe paths, validates required inputs and
  snapshot existence before unsupported-runtime fallback, returns `501` no-Podman fallback payloads before
  DB mutation, and has focused Go tests plus seeded TS-Go read/missing-state harness coverage. Remaining
  risk is parity proof for TS event/locking semantics and end-to-end positive Podman/frontend smoke coverage.
  Evidence: `server/routes/mcp.ts:338`,
  `server/routes/mcp.ts:407`, `server/routes/mcp.ts:442`, `server/routes/mcp.ts:560`,
  `server/routes/vnet.ts:7`, `server/routes/gateway.ts:30`,
  `server/services/volume-snapshot-service.ts:106`,
  `server/services/volume-snapshot-service.ts:225`,
  `go_backend/internal/mcpcompat/handlers.go:26`, `go_backend/internal/mcpcompat/handlers.go:64`,
  `go_backend/internal/mcpcompat/handlers.go:384`,
  `go_backend/internal/systemroutes/handlers.go:514`,
  `go_backend/internal/systemroutes/handlers.go:538`,
  `go_backend/internal/misccompat/handlers.go:3333`,
  `go_backend/internal/misccompat/volume_snapshot.go:306`,
  `go_backend/internal/misccompat/volume_snapshot_test.go:17`.

### P0

- **P0 — Terminal/WebSocket replacement parity is incomplete.** A Go backend replacement would still
  degrade terminal reconnect/replay if users depend on TS persistent BufferManager/xterm serialized
  replay, dtach reattach semantics, or lifecycle event parity across restarts. The older frontend-consumed
  terminal event gaps are narrower now: Go emits raw-capped scrollback and bufferState snapshots and
  gates resize to the last input client. VNet is no longer a missing route: Go exposes a relay-only
  `/ws/vnet` with explicit UDP rendezvous fallback, so remaining VNet work is TS-vs-Go relay fixture/smoke
  coverage rather than entry-point absence. Acceptance: implement TS-equivalent terminal/VNet semantics or
  explicitly document/disable degraded paths with frontend-visible fallbacks; terminal WS smoke tests must
  cover create, subscribe, input, resize, reconnect scrollback, bufferState, kill, rename, unauthorized
  access, and VNet relay/disabled-setting behavior. Evidence: paths listed in the first two Findings plus
  `go_backend/internal/app/router.go:194`, `go_backend/internal/ws/hub_terminal_test.go`, and
  `go_backend/internal/ws/vnet_relay_test.go`.
- **P0 — Container/proxy/port runtime side effects are not yet proven TS-equivalent.** Go can invoke compose,
  derive/persist proxy metadata, run a reverse proxy, and persist port allocation rows, so the older
  "proxy URL routing absent" description is obsolete. Remaining replacement risk is that rootless setup,
  cgroup/backend detection, proxy cache reconciliation, port allocator edge cases, and lifecycle/event
  semantics still need end-to-end TS-vs-Go proof. Acceptance: either implement and prove the full TS
  container-service/container-proxy/port-allocator contract, or mark any degraded container/proxy paths
  unsupported in Go and keep UI/API from implying parity. Evidence: container Finding paths above.
- **P0 — Volume snapshot parity now hinges on TS event/locking and end-to-end smoke proof, not metadata-only behavior.** TS creates and applies real archives via `podman exec`/`podman cp`. Current Go code now also accepts the frontend `chapterId` create field, creates/applies/deletes bounded Podman tar archives, cleans DB rows on delete, validates unsafe container paths, and returns an explicit `PODMAN_NOT_FOUND` fallback before DB/container mutation when Podman is unavailable. Remaining acceptance: prove TS-compatible creating/created/applying/error event semantics, concurrency/delete-vs-apply behavior, and a frontend/Podman smoke transcript, or mark any event/UI deltas as explicit degraded behavior. Evidence: `server/services/volume-snapshot-service.ts:98`,
  `server/services/volume-snapshot-service.ts:155`, `server/services/volume-snapshot-service.ts:214`,
  `frontend/lib/api/projects.ts:279-308`, `server/routes/volume-snapshots.ts:23-37`,
  `server/lib/validators/containers.ts:9-20`, `go_backend/internal/misccompat/handlers.go:3333`,
  `go_backend/internal/misccompat/handlers.go:3394`,
  `go_backend/internal/misccompat/volume_snapshot.go:306`,
  `go_backend/internal/misccompat/volume_snapshot.go:362`,
  `go_backend/internal/misccompat/volume_snapshot_test.go:17`.
- **P0 — Share lifecycle is no longer an expiry-bypass blocker, but its Go-only persistence semantics need an explicit replacement decision.** TS share records are process-local and stale share directories are cleaned at startup. Current Go also cleans stale share dirs/metadata at startup and rejects unknown disk directories without matching metadata; metadata-backed shares enforce expiry and delete disk content when expired. Remaining acceptance: decide whether persistent metadata-backed shares are an accepted Go design or should be disabled/cleaned like TS; smoke must cover restart, expired metadata, stale directory without metadata, authenticated creation, and public download/preview privacy semantics. Evidence: `server/lib/shares.ts:18`,
  `server/lib/shares.ts:106`, `go_backend/cmd/narrafork-go/main.go:164`,
  `go_backend/internal/app/router.go:85`, `go_backend/internal/misccompat/handlers.go:674`,
  `go_backend/internal/misccompat/handlers.go:869`, `go_backend/internal/misccompat/handlers.go:938`,
  `go_backend/internal/misccompat/share_security_test.go:431`,
  `go_backend/internal/misccompat/share_security_test.go:455`,
  `go_backend/internal/misccompat/share_security_test.go:472`.

### P1

- **P1 — Gateway fallback is not parity.** Go returns `supported:false` or fallback payloads for
  gateway while TS has runtime hubs, QR/login platform handling, and session routing. VNet is tracked in
  the P0 WebSocket row as an active relay-only runtime requiring TS-vs-Go smoke coverage, not as a
  missing entry point. Acceptance: frontend smoke tests must assert fallback copy/disabled UI for gateway,
  or Go must bundle an equivalent gateway runtime. Evidence: `server/routes/gateway.ts:30`,
  `go_backend/internal/systemroutes/handlers.go:514`.
- **P1 — MCP external-server runtime remains partially degraded.** Go has protocol and CRUD-compatible endpoints plus stdio external tool discovery/injection/execution through per-call JSON-RPC sessions, but it still does not mirror TS `mcpManager` persistent live process lifecycle or non-stdio transports.
  Acceptance: connect/disconnect/test/import/list-tools and narrator-tool smoke tests must verify stdio external MCP tool availability; persistent runtime and sse/streamable-http remain documented follow-ups. Evidence: `server/routes/mcp.ts:338`,
  `server/routes/mcp.ts:407`, `server/routes/mcp.ts:442`, `server/routes/mcp.ts:560`,
  `go_backend/internal/mcpcompat/handlers.go:64`, `go_backend/internal/mcpcompat/handlers.go:384`,
  `go_backend/internal/mcpcompat/handlers.go:425`.
- **P1 — Update/runtime/storage/admin operations are partially conservative.** Go exposes endpoints
  but self-update apply is unsupported and storage cleanup avoids deleting worktrees/containers.
  Acceptance: UI and docs must treat these as operator/manual or fallback flows unless full TS
  service behavior is implemented. Evidence: `server/routes/update.ts:48`,
  `server/routes/update.ts:143`, `server/routes/storage.ts:91`,
  `go_backend/internal/systemroutes/handlers.go:735`,
  `go_backend/internal/systemroutes/handlers.go:788`,
  `go_backend/internal/misccompat/handlers.go:672`.
- **P1 — Benchmark execution parity needs runtime smoke tests.** TS has container-backed benchmark
  services using Podman and workdir copy/setup; Go has benchmark endpoints and summaries but should
  be treated as compatibility until the actual run/cancel/artifact lifecycle is tested against TS.
  Acceptance: benchmark smoke tests cover create/run/status/results/logs/cancel with output limits
  and cleanup. Evidence: `server/services/benchmark-container.ts:34`,
  `server/services/benchmark-service.ts:240`, `server/services/benchmark-service.ts:310`,
  `go_backend/internal/systemroutes/handlers.go:127`, `go_backend/internal/systemroutes/handlers.go:375`.

### P2

- **P2 — Hooks/provider-model cache coverage still needs endpoint-level checks; notification sounds are now sampled.** Go
  includes compatible routes, and notification-sound auth/upload/download/delete/MIME/missing-state
  contracts now have focused and TS-Go harness coverage. Remaining endpoint-detail work is hook
  runtime breadth plus provider model cache persistence/empty-state fallbacks. Evidence:
  `go_backend/internal/misccompat/handlers.go:75`,
  `go_backend/internal/misccompat/handlers.go:97`, `go_backend/internal/settings/handlers.go:51`,
  `go_backend/internal/systemroutes/handlers.go:821`.
- **P2 — Runtime scan/cleanup should keep explicit limits.** Go uses conservative fallbacks in some
  places; any future parity work must preserve request-path output limits, timeouts, and no
  destructive worktree/DB/container cleanup without explicit admin intent. Evidence:
  `go_backend/internal/misccompat/handlers.go:625`, `go_backend/internal/misccompat/handlers.go:658`,
  `go_backend/internal/systemroutes/handlers.go:648`.

### Acceptance Criteria

- Runtime/system parity inventory maps every TS-facing WebSocket/API/runtime feature in this scope
  to **full parity**, **intentional fallback**, **unsupported**, or **Go-only extension**; fallback and
  unsupported entries are surfaced to frontend/API smoke tests.
- Terminal acceptance: Go matches TS terminal create/list/update/kill, WS subscribe/input/resize,
  scrollback replay, bufferState, reconnect, dtach detach/reattach, process exit codes, DB status,
  lifecycle events, and auth behavior on Linux and Windows, or unsupported features are disabled.
- Container acceptance: Go matches TS Podman setup checks, rootless env/cgroup handling,
  compose start/stop/restart/logs, port allocation persistence, proxy labels/URLs, container IP
  reconciliation, cleanup, and lifecycle event behavior; no request path performs unbounded Podman
  output collection.
- File/share/storage acceptance: uploads, avatars, shares, public preview/download, storage scan,
  cleanup, database preview/cleanup/vacuum, and fs route behavior match TS status codes, size limits,
  auth boundaries, and cleanup semantics.
- MCP/gateway/VNet/providers/update/benchmark/snapshot acceptance: each route either executes the
  TS-equivalent runtime side effect and emits compatible statuses/events, or returns a stable
  explicit fallback/unsupported payload that the frontend handles without implying success.
- Smoke tests cover authenticated and unauthorized flows, missing external binaries (`git`, `podman`,
  `dtach`), large-output limits, reconnect behavior, and stale/missing TS-created DB tables.

### Notes

- Status is `Done` for this spec section because the runtime/system integration gap list is now
  prioritized with evidence and acceptance criteria. It does **not** mean Go is replacement-ready;
  the P0 items above remain replacement blockers unless explicitly scoped out.
- Cross-track impact: API/frontend tracks should include fallback/unsupported payload checks for
  VNet, gateway, update apply, storage cleanup, terminal WS, container proxy, and volume snapshots;
  narrator track should account for degraded terminal/share/admin/MCP optional tools.
- No source files other than this spec were modified.

## Track: Frontend Contract / Smoke Tests

Owner: spec-frontend
Status: Verified
Last update: 2026-05-14

### Scope

- Map the current frontend replacement contract from API clients, WS hooks/managers, and high-risk
  route consumers to the Go backend compatibility surface.
- Prioritize frontend-visible breakages for replacing the TS backend with Go, including response
  field names, auth boundaries, fallback payloads, and degraded runtime behavior.
- Define a smoke checklist that can be run against a Go-backed frontend before declaring replacement
  readiness.
- Evidence paths intentionally focus on frontend contract entry points and corresponding Go/TS
  backend surfaces: `frontend/lib/api/auth.ts`, `frontend/lib/api/projects.ts`,
  `frontend/lib/api/chapters.ts`, `frontend/lib/api/narrators.ts`,
  `frontend/lib/narrator-ws-manager.ts`, `frontend/hooks/useNarratorWS.ts`,
  `frontend/lib/api/terminals.ts`, `frontend/hooks/useTerminalWS.ts`,
  `frontend/lib/api/git.ts`, `frontend/lib/api/misc.ts`, `frontend/lib/api/settings.ts`,
  `frontend/lib/api/streams.ts`, `frontend/lib/usage-history-api.ts`,
  `go_backend/internal/app/router.go`, `go_backend/internal/app/router.go:142-143`,
  `go_backend/internal/narrators/handlers.go`, `go_backend/internal/ws/hub.go`,
  `go_backend/internal/systemroutes/handlers.go`,
  `go_backend/internal/misccompat/handlers.go`, `go_backend/internal/chapters/handlers.go`,
  and TS references under `server/app.ts`, `server/routes/*`, `server/websocket/*`.

### Findings

- The frontend replacement surface is broader than simple route existence. Core consumers include
  auth/session bootstrap, project graph/ruler APIs, chapter/review/container workflows,
  narrator message/SSE/WS flows, terminal API/WS, Git actions, settings/provider/MCP/skills/
  routines/hooks/storage/update/gateway endpoints, and usage history. Evidence:
  `frontend/lib/api/auth.ts`, `frontend/lib/api/projects.ts`, `frontend/lib/api/chapters.ts`,
  `frontend/lib/api/narrators.ts`, `frontend/lib/api/terminals.ts`,
  `frontend/lib/api/git.ts`, `frontend/lib/api/misc.ts`, `frontend/lib/api/settings.ts`,
  `frontend/lib/api/streams.ts`, `frontend/lib/usage-history-api.ts`.
- Go route coverage is substantial, but several frontend-critical contracts are currently
  fallback/degraded or schema-incompatible rather than TS-equivalent. Evidence:
  `go_backend/internal/app/router.go`, `go_backend/internal/systemroutes/handlers.go`,
  `go_backend/internal/misccompat/handlers.go`, `go_backend/internal/chapters/handlers.go`,
  `go_backend/internal/narrators/handlers.go`.
- Narrator real-time behavior is the most replacement-sensitive area: the frontend manager/hook
  expects TS-compatible narrator WS event names, control messages, and message identifiers. Current Go
  still has a reduced/different WS surface, but HTTP message payloads now include the TS-compatible
  `messageUuid` alias alongside legacy `sdkMessageUuid`; a route test locks this alias for catch-up child
  messages. Evidence:
  `frontend/lib/narrator-ws-manager.ts`, `frontend/hooks/useNarratorWS.ts`,
  `frontend/lib/api/types.ts:391-399`, `frontend/lib/api/narrators.ts`,
  `server/routes/narrators.ts`, `server/websocket/*`, `server/db/schema.ts:342`,
  `go_backend/internal/narrators/service.go:137-192`,
  `go_backend/internal/narrators/message_pagination_test.go:156`, `go_backend/internal/ws/hub.go`.
- Runtime-heavy UI can still render with Go, but key interactions degrade unless explicitly handled:
  terminal reconnect/scrollback/container proxy behavior, gateway/VNet/update apply/storage cleanup,
  and benchmark/runtime fallbacks. Evidence: `frontend/lib/api/terminals.ts`,
  `frontend/hooks/useTerminalWS.ts`, `frontend/lib/api/misc.ts`, `frontend/lib/api/streams.ts`,
  `go_backend/internal/systemroutes/handlers.go`, `go_backend/internal/misccompat/handlers.go`.
- Some Go compatibility choices are acceptable only if the frontend receives stable fallback payloads
  and disables unsupported affordances; otherwise pages can show misleading success or fail with 500s.
  Highest-risk examples are ruler compatibility routes with partial/degraded lifecycle semantics,
  routines project/global fields, MCP/custom subagent table fallback, update self-apply unsupported,
  and settings/provider secret handling.

### P0

- **P0 — Narrator WS event/control/message contract mismatch can break active sessions.** The
  frontend subscribes through `frontend/lib/narrator-ws-manager.ts` and `frontend/hooks/useNarratorWS.ts`
  and expects TS-compatible event names, subscription/control messages, tool/permission/subagent/
  background/compact/goals updates, and incremental message identity. Go must either mirror TS event
  names and payloads or provide a frontend adapter before replacement. Evidence:
  `frontend/lib/narrator-ws-manager.ts`, `frontend/hooks/useNarratorWS.ts`,
  `server/websocket/*`, `go_backend/internal/app/router.go:142-143`,
  `go_backend/internal/ws/hub.go`.
- **Resolved for HTTP payloads — Narrator messages now expose `messageUuid` alias.** Go still stores the
  TS/Drizzle column as `sdk_message_uuid` and keeps the legacy `sdkMessageUuid` JSON field for
  compatibility, but HTTP message JSON now also exposes `messageUuid` so frontend message identity,
  fork-from-message, and ask-in-passing consumers can use the TS field. Remaining narrator P0 risk stays
  with WS event/control parity rather than this HTTP field alias. Evidence:
  `frontend/lib/api/types.ts:391-399`, `frontend/lib/api/narrators.ts`,
  `frontend/components/narrator/*`, `server/routes/narrators.ts`, `server/db/schema.ts:342`,
  `go_backend/internal/narrators/service.go:137-192`,
  `go_backend/internal/narrators/message_pagination_test.go:156`.
- **Resolved — Notification-sound mutation auth and lifecycle baseline matches TS in current Go.** Frontend/API security
  expectations still require broader UI smoke, but current Go registers `/api/notification-sounds` inside
  the authenticated API group, has a route test asserting unauthenticated POST/GET/DELETE return 401,
  and now has sampled upload/download/delete/missing/MIME validation coverage plus focused per-user
  lookup tests so one user's custom sound is not served from another user's directory. Evidence:
  `server/app.ts`, `server/routes/notification-sounds.ts`, `go_backend/internal/app/router.go`,
  `go_backend/internal/app/router_test.go:240`, `go_backend/internal/misccompat/routes_test.go`,
  `go_backend/testtools/ts-go-api-contract-diff.ts`.
- **Resolved — Settings raw-secret masking/shape now has Go coverage.** The settings UI and provider
  forms use `frontend/lib/api/settings.ts`; current Go masks `auth.jwtSecret`, TLS passphrase, VNet relay
  token, provider API keys, NUG OAuth client secret, and Cline access token in GET/PATCH responses while
  preserving masked placeholder patches. Keep `SETTINGS-SECRET-01` as frontend/fixture smoke coverage,
  but this is no longer an open raw-secret exposure blocker. Evidence:
  `frontend/lib/api/settings.ts`, `frontend/components/settings/*`,
  `server/routes/settings.ts:643-698`, `server/routes/settings.ts:1003-1042`,
  `go_backend/internal/settings/handlers.go:151-174`,
  `go_backend/internal/settings/handlers.go:2061-2088`,
  `go_backend/internal/settings/handlers_test.go:1027`,
  `go_backend/internal/settings/handlers_test.go:1097`,
  `go_backend/internal/settings/handlers_test.go:1151`.
- **P0 — Batch merge async session is now implemented but not smoke-proven.** Go now exposes
  `mergeSessionId` / `targetChapterId` / `createdTarget` / `status`, health capability metadata,
  WS progress/decision support, rollback, and stale-session cleanup; frontend batch-merge entry
  points gate on capability and no longer label async session creation as completion. Remaining
  replacement risk is end-to-end proof for reconnect, continue-to-AI-resolution outcomes, AI
  resolution failure, DB/git rollback, project DB sync, and browser smoke behavior. Evidence:
  `frontend/lib/api/chapters.ts`, `frontend/hooks/usePlatform.ts`,
  `frontend/components/chapter/ChapterBatchMergeModal.tsx`,
  `frontend/components/graph/NarraFlow.tsx`, `server/services/chapter-batch-merge.ts:81-427`,
  `go_backend/internal/chapters/handlers.go:156-235`, and
  `go_backend/internal/chapters/service.go:925-1188`.
- **P0 — Ruler compatibility routes are lifecycle-partial/degraded.** The project/ruler frontend path
  uses `frontend/lib/api/projects.ts`; Go has misccompat ruler routes, but they must preserve TS
  persisted-field expectations and make partial lifecycle semantics explicit to avoid project-page or
  interaction failures. Evidence: `frontend/lib/api/projects.ts`, `server/routes/ruler.ts`,
  `go_backend/internal/app/router.go:98-103`, `go_backend/internal/misccompat/handlers.go:2046-2054`,
  `go_backend/internal/misccompat/handlers.go:1652-1891`,
  `go_backend/internal/misccompat/handlers.go:2058-2329`.
- **P0 — Terminal reconnect, scrollback, and container proxy degradation must be explicit.** Terminal UI
  can tolerate unsupported advanced behavior only if Go returns stable fallback states and WS close/error
  reasons; silent partial behavior is a replacement blocker for container-backed chapters. Evidence:
  `frontend/lib/api/terminals.ts`, `frontend/hooks/useTerminalWS.ts`, `server/routes/terminals.ts`,
  `server/websocket/*`, `go_backend/internal/app/router.go:142-143`,
  `go_backend/internal/systemroutes/handlers.go`, `go_backend/internal/ws/hub.go`.
- **P0 — Volume snapshot workflow still needs frontend/runtime smoke proof.** The previous `chapterId`/`sourceChapterId` field mismatch and metadata-only Go behavior are obsolete: Go now accepts frontend `chapterId`, falls back to legacy `sourceChapterId`, performs real Podman archive create/apply/delete side effects, and returns explicit no-Podman fallback payloads before mutation. Replacement still needs smoke evidence that the frontend handles Go success/fallback/error payloads without misleading success, and that any missing TS volume-snapshot event/locking semantics are either implemented or explicitly degraded. Evidence: `frontend/lib/api/projects.ts:279-308`,
  `frontend/hooks/useVolumeSnapshots.ts:20-38`, `server/routes/volume-snapshots.ts:23-37`,
  `server/lib/validators/containers.ts:9-20`, `go_backend/internal/misccompat/handlers.go:3333-3417`,
  `go_backend/internal/misccompat/volume_snapshot.go:306-404`,
  `go_backend/internal/misccompat/volume_snapshot_test.go:17`.

### P1

- **Resolved — FS browse/mkdir/reveal baseline contracts are sampled.** File browser UI expects `sep`,
  `parent`, `drives`, directory-only entries, hidden-directory toggles, mkdir validation, and a
  platform-safe reveal capability. Current Go returns the navigation metadata, rejects TS-invalid
  mkdir bodies such as `{path}`, rejects unsafe folder names, and on Linux matches TS by refusing
  reveal before any `xdg-open`/process side effect while exposing `fs.reveal.supported=false` in
  capabilities. The TS-Go harness now samples shortcuts, browse default/fixture/hidden/missing,
  mkdir invalid/positive cases, and reveal unsupported/missing behavior. Keep a frontend smoke because
  WSL/Windows host-path UX still depends on the deployment platform and capabilities. Evidence:
  `frontend/lib/api/misc.ts`, `frontend/components/common/DirectoryPicker.tsx`,
  `frontend/components/common/PathInput.tsx`, `server/routes/fs.ts`,
  `go_backend/internal/fs/handlers.go`, `go_backend/internal/fs/handlers_test.go`,
  `go_backend/testtools/ts-go-api-contract-diff.ts`.
- **Resolved — Project routines include frontend-required override fields.** The routines UI expects
  project-scoped `override` plus `globalEnabled`; current Go returns both fields and `enabled` for
  `/api/routines/project/:projectId`, and focused coverage verifies enabled/disabled overrides against
  global defaults. Keep frontend smoke coverage because project routines still depend on DB-backed
  `chapter_settings` availability. Evidence: `frontend/lib/api/misc.ts`,
  `frontend/components/project/ProjectRoutinesModal.tsx`, `server/routes/routines.ts`,
  `go_backend/internal/simplecontent/handlers.go:388-420`,
  `go_backend/internal/simplecontent/skills_routines_parity_test.go:163`.
- **Partially resolved — Provider endpoints have focused frontend-contract coverage.** Go returns
  frontend-compatible `{models, fromCache}` for per-provider OpenAI/Anthropic/NKP/NUG refresh routes and
  `{results, models, fromCache}` for aggregate OpenAI/NUG refresh; the TS-Go harness now covers
  regular-user OpenAI/Anthropic/NKP/NUG positive provider refresh/cache fixtures plus NUG
  quota/channels/usage/summary normalization, while focused coverage stubs upstream providers and
  verifies sorted model arrays, provider auth headers, NKP `model_id` normalization, Anthropic
  `resolvedBaseUrl`, and NUG quota/channel/usage normalization. Cline status/pool/recommended cached
  endpoints, offline fixture-backed provider refresh/cache persistence, recommended/free-model
  refresh/cache persistence, user-info/token-refresh/balance, and invalid-refresh cleanup are now locked to
  frontend shape: `status.totalModels` reports the cached model pool rather than enabled-model count,
  provider status exposes `{id,name,prefix,hasToken}`, pool search/count preserve model metadata, and
  missing provider caches return an empty `models` array instead of `null`. NUG models/quota/channel/
  usage endpoints are now locked to frontend shape: GET models is cache-only with `fromCache:true`,
  aggregate refresh returns `{results, models, fromCache}`, quota aliases normalize to
  `balance`/`totalGranted`, usage accepts `data`/`count` fallbacks and emits camelCase event fields, and
  summary totals are numeric camelCase. Kiro status/models/usage now have focused shape coverage for
  `snapshot.entries`, `usageCache`, credential-scoped cached models, and cached usage entries; Kiro model
  refresh is no longer advertised as supported in runtime capabilities and returns an explicit
  feature-disabled payload because real refresh requires the TypeScript Kiro plugin bridge. Codex status
  now exposes the frontend `globalProxy` alias alongside legacy `proxy`, sanitizes `tierOrder`, builds
  TS-style `effectiveTierOrder`, returns TS-compatible import validation errors for empty/no-token
  imports, and has focused coverage for pagination, settings fields, paged `usageCache`, quota overview
  access, and `models.refreshSupported:false` capability. Remaining
  provider-page work is mostly end-to-end smoke for live Codex browser/device auth and real usage fetch
  side effects, not static response shape for these provider settings pages. Evidence:
  `frontend/lib/api/settings.ts`, `frontend/lib/api/misc.ts`, `server/routes/cline.ts`,
  `server/routes/nug.ts`, `server/routes/kiro.ts`, `server/routes/openai.ts`,
  `server/routes/codex.ts`, `server/routes/anthropic.ts`, `server/routes/nkp.ts`,
  `go_backend/internal/systemroutes/handlers.go`, `go_backend/internal/app/router.go`,
  `go_backend/internal/systemroutes/handlers_test.go:295`,
  `go_backend/internal/systemroutes/handlers_test.go:812`,
  `go_backend/internal/app/route_contract_test.go:498`.
- **P1 — Update self-apply is unsupported.** The frontend update UI must display a disabled/unsupported
  state instead of offering a successful apply path when backed by Go. Evidence:
  `frontend/lib/api/misc.ts`, `server/routes/update.ts`,
  `go_backend/internal/systemroutes/handlers.go`.
- **P1 — MCP/custom subagent table fallback needs stable empty/error states.** Frontend pages should not
  crash if Go lacks full table-backed behavior; Go should return TS-shaped empty arrays/status payloads.
  Evidence: `frontend/lib/api/misc.ts`, `server/routes/mcp.ts`, `server/routes/custom-subagents.ts`,
  `go_backend/internal/misccompat/handlers.go`.

### P2

- **P2 — Admin 403 UX should be smoke-tested.** Non-admin users should receive TS-shaped 403 responses
  that frontend admin routes can render as access denied rather than generic failure. Evidence:
  `frontend/lib/api/misc.ts`, `server/routes/admin.ts`, `go_backend/internal/systemroutes/handlers.go`.
- **P2 — Tool-call detail fields may be reduced.** Frontend narrator detail panels should tolerate
  missing optional timing/output/permission fields, but replacement should document any omitted fields.
  Evidence: `frontend/lib/api/narrators.ts`, `frontend/components/narrator/*`,
  `server/routes/narrators.ts`, `go_backend/internal/narrators/handlers.go`.
- **P2 — Usage statistics and usage-history compatibility are display-level until billing/UI decisions
  require exact parity.** Keep status/empty payloads stable and avoid 500s. Evidence:
  `frontend/lib/usage-history-api.ts`, `server/routes/*usage*`,
  `go_backend/internal/systemroutes/handlers.go`.
- **P2 — Storage/runtime/gateway fallbacks are acceptable only with clear degraded UI.** Frontend should
  show unavailable/unsupported states for cleanup, runtime metadata, gateway, and benchmark runner
  fallbacks rather than implying TS-equivalent execution. VNet remains in P0 WebSocket smoke coverage as
  a relay-only runtime with UDP rendezvous disabled, not because the `/ws/vnet` route is missing.
  Evidence: `frontend/lib/api/misc.ts`,
  `frontend/lib/api/streams.ts`, `server/routes/storage.ts`, `server/routes/runtime.ts`,
  `server/routes/gateway.ts`, `server/routes/vnet.ts`, `server/routes/benchmarks.ts`,
  `go_backend/internal/systemroutes/handlers.go`.
- **P2 — Edge features can be documented as degraded.** Learning, uploads, hooks, and
  project-db status helpers should keep basic UI paths stable, but exact side effects can remain lower
  priority if replacement notes call them out. Share lifecycle still needs restart/expiry/privacy smoke
  because Go's metadata-backed persistence is intentionally different from TS's ephemeral in-memory
  store, but the older unknown-directory expiry bypass description is obsolete. Evidence:
  `frontend/lib/api/misc.ts`,
  `server/routes/favorites.ts`, `server/routes/learning.ts`, `server/routes/shares.ts`,
  `server/routes/uploads.ts`, `server/routes/hooks.ts`, `server/routes/project-db.ts`,
  `go_backend/internal/misccompat/handlers.go`.

### Replacement Smoke Checklist

| Smoke ID | Area | Preconditions | Steps | Expected Result | Artifact |
|---|---|---|---|---|---|
| AUTH-NS-01 | Auth + notification sounds | Fresh fixture with admin and normal user tokens | Login, call `/api/auth/status`, attempt notification-sound POST/DELETE unauthenticated and authenticated | TS-compatible session shape; unauthenticated mutations rejected with no row/file mutation; no 5xx | HTTP transcript; DB/file diff |
| API-GIT-01 | Git API guard | Fixture can run with `git` present and with `git` intentionally unavailable | Call status/log/diff/commit paths and Git-missing guarded paths | Normal Git paths match TS required fields; missing Git returns stable `GIT_NOT_INSTALLED` shape only for guarded routes | HTTP transcript; env/binary record |
| SETTINGS-SECRET-01 | Settings/providers | Fixture contains provider credentials | Load/update settings and provider pages | No raw secret exposure; masked metadata and save behavior match TS; unsupported providers are explicit | HTTP transcript; screenshot/state dump |
| DB-FTS-01 | DB/search/readiness | TS-created DB with chapters, narrators, messages, CJK/substring terms, and FTS objects | Query `/api/search`; remove/disable FTS in a copy only if testing fallback | Results include required TS fields, snippets/ranking when enabled, or explicit degraded readiness; no 5xx | Search response diff; readiness output |
| DOMAIN-CHAPTER-01 | Chapter lifecycle | Git fixture project with root/trunk and branch chapters | Create/update/fork/dormant/wake/delete; try invalid root/trunk mutation | Required fields and DB/git/worktree side effects match TS, or unsupported fork modes reject before mutation | HTTP transcript; DB row diff; worktree listing |
| DOMAIN-MERGE-01 | Merge/batch/review | Fixture with clean, dirty, reviewed, and conflict branches | Run merge-check, merge, unmerge, batch merge, review creation/conclusion | No data loss; dirty/review/conflict semantics match TS or action is disabled before mutation; async session fields/events present when enabled | HTTP/WS transcript; git log/status; DB diff |
| DOMAIN-RULER-01 | Ruler lifecycle | Project with existing TS-created ruler positions/segments | Load ruler view, save positions, fork/merge/rebase/abandon supported paths | Existing rows load without 5xx; partial lifecycle is disabled visibly or matches TS side effects | HTTP transcript; frontend screenshot/state |
| NARRATOR-WS-01 | Narrator HTTP/SSE/WS | Narrator fixture with messages, tool calls, compact marker, goal, and subagent tree | Load messages, send prompt, reconnect WS/SSE, observe text/tool/compact/subagent/goal events | Event names and required fields match TS; `messageUuid` identity works; reconnect does not duplicate/corrupt state | SSE/WS transcript; message DB diff |
| NARRATOR-PERM-01 | Narrator permission safety | Narrator in default/read-only/plan/deny modes with a write-tool prompt | Trigger pending permission, approve/deny, retry malformed input | No write before approval; denial/malformed input cannot mutate; DB statuses and WS `permission_resolved` match TS | Tool-call DB diff; file diff; WS transcript |
| RUNTIME-TERM-01 | Terminal runtime/WS | Fixture chapter with terminal support; auth and unauth clients | Create/list/connect WS, input, resize, reconnect, check scrollback/bufferState, kill/rename | TS-compatible terminal fields/events, auth rejection, replay behavior, or explicit unsupported state; no 5xx | WS transcript; terminal row diff |
| RUNTIME-CONTAINER-01 | Containers/proxy/ports | Podman/compose fixture and missing-Podman variant | Start/stop/restart/logs/status/container terminal/proxy URL paths | Real side effects match TS, or unsupported state is exposed before mutation; missing Podman is stable | HTTP transcript; podman/log artifact; port/proxy state |
| RUNTIME-SHARE-01 | Shares/privacy | Fixture file and expired-share scenario | Create/share/download/preview before and after restart/expiry | Auth and expiry semantics match accepted design; no stale private file served accidentally | HTTP transcript; share dir listing |
| RUNTIME-SNAPSHOT-01 | Volume snapshots | Container volume fixture and frontend `chapterId` request | Create/list/apply/delete snapshot | Real archive side effects match TS, or explicit unsupported response before DB/container mutation; `chapterId` accepted if enabled | HTTP transcript; DB diff; podman archive evidence |
| FRONTEND-NOCRASH-01 | Frontend degraded state | Go-backed frontend with normal/admin users and disabled capabilities | Visit dashboard, project graph/ruler, chapter, narrator, terminal, settings, runtime/MCP/update/storage pages | No uncaught exception; disabled states visible; no misleading success toast for disabled features | Browser console log; screenshots; route list |

### Smoke Suite Exit Conditions

- P0 smoke rows must be `PASS` or `DISABLED-BY-DESIGN` under the contract above.
- Any 5xx, auth bypass, raw secret exposure, misleading success, unexpected mutation, or frontend uncaught exception is `FAIL`.
- Results must record backend commit, frontend commit if different, fixture ID/seed, OS, external binary versions/availability (`git`, `podman`, `dtach`), and artifact paths for transcripts, DB diffs, git/worktree diffs, WS/SSE logs, and screenshots.

### Acceptance Criteria

- Progress board row remains `Owner=spec-frontend` and `Status=Done` or `Verified`.
- All P0 items above are fixed, or intentionally disabled with frontend-visible unsupported states and
  automated smoke coverage proving no crash, no misleading success, and no auth/secret regression.
- Narrator WS and message payloads are TS-compatible for event names, control messages, permission/tool
  events, reconnect behavior, and `messageUuid` identity, or the frontend contains a tested compatibility
  adapter that accepts both TS and Go shapes.
- Project graph/ruler and chapter merge/review/container flows can load existing TS-created data without
  500s and with TS-compatible response fields required by the frontend.
- Replacement smoke checklist passes against a Go-backed frontend with at least one normal user and one
  admin user, and results are recorded in this spec or release notes.
- Degraded P1/P2 features return stable JSON/SSE/WS fallback contracts so the frontend can render disabled
  or empty states instead of uncaught exceptions.

### Notes

- Status is `Verified` for contract documentation because the frontend dependency map and known Go gaps
  were consolidated from verified track findings and provided investigation notes. It does not mean the Go
  backend is replacement-ready; current P0s still make replacement unsafe.
- This section intentionally avoids re-running broad searches and uses the supplied verified findings plus
  path-level evidence to define the replacement contract.
- No files other than `docs/go-backend-parity-spec.md` were modified.

## Track: Final Synthesis

Owner: spec-synthesis
Status: Done
Last update: 2026-05-14

### Scope

- Consolidate all completed/verified tracks into one cross-track P0/P1/P2 remediation plan.
- State replacement readiness and the minimum checklist for moving from unsafe to compatibility beta or replacement candidate.

### Cross-track P0 Rollup

- **API/security/setup:** the older notification-sound auth-boundary and Git-missing guard blockers are resolved in current Go, but `AUTH-NS-01` and `API-GIT-01` should stay in smoke coverage to prevent regressions. Evidence: API Contract P0; frontend auth/git smoke checklist.
- **DB/search/bootstrap:** Go search must use TS-compatible FTS/trigram/snippet/rank semantics or be explicitly degraded; Go also needs a schema/FTS readiness or migration/bootstrap policy before standalone replacement. Evidence: DB / Schema / FTS P0.
- **Domain lifecycle/data integrity:** project deletion, fork-at-message/inherit modes, chapter root/trunk/status invariants, dormant/wake DB/worktree consistency, merge dirty-source/review/worktree-lock protections, partial/degraded ruler lifecycle semantics, and batch-merge reconnect/conflict/rollback smoke proof remain replacement risks. Evidence: Domain Lifecycle P0.
- **Narrator safety/runtime:** core content-block/tool transcript replay, uploaded image/text-file replay, native web-search/image-generation replay, and provider fail-fast are implemented, but provider-specific live rich-block smoke, TS-equivalent permission/write safety, plan-mode live smoke, and broader provider/runtime parity remain blockers. Evidence: Narrator / Agent P0.
- **Runtime side effects/security:** terminal/WS replay and VNet behavior, container/proxy/port side effects, volume snapshot event/locking/smoke evidence, and share restart/expiry/privacy smoke evidence remain incomplete or unsafe if treated as replacement-ready. Share's older unknown-directory expiry bypass finding is obsolete in current Go, but metadata-backed persistence still needs an explicit accepted-design decision. Evidence: Runtime / System Integration P0.
- **Frontend-visible contracts:** narrator WS event/control parity, batch merge reconnect/decision UI smoke, ruler partial/degraded lifecycle compatibility, terminal/container degraded states, and volume snapshot success/fallback/error UI smoke behavior must be fixed or adapted before replacement. HTTP `messageUuid` alias, settings secret masking, and batch-merge session response/capability/progress-event consumption are now implemented, but frontend smoke remains required. Evidence: Frontend Contract P0.

### Cross-track P1 Rollup

P1 classification:

- **P1-critical:** not a P0 data-loss/security blocker by itself, but required for `replacement candidate` because it affects common workflows, release safety, or TS-compatible observability.
- **P1-deferred:** acceptable for `compatibility beta` if documented with stable fallback/disabled UI and smoke evidence, but still required before claiming broad feature parity.

- **P1-critical — Route/provider contract detail:** Cline/NUG/provider/model endpoint shapes, chapter/narrator response/stream semantics, and provider settings pages need endpoint-level smoke tests. VNet is tracked as P0 WebSocket smoke because Go is relay-only with UDP rendezvous disabled, not because the `/ws/vnet` entry point is missing. Evidence: API and Frontend P1.
- **P1-critical — Schema capability and FTS lifecycle:** startup/readiness capability reporting for tables/columns/indexes/FTS and FTS verifier/rebuilder or clear readiness failure are required for replacement-candidate safety. Optional-table ownership and backup scope can be P1-deferred if documented. Evidence: DB P1.
- **P1-critical — Lifecycle correctness:** review lifecycle, unmerge side effects, dependency edge validation, and git discard-all safety are replacement-candidate required because they affect common code-review/graph/git correctness. Evidence: Domain P1.
- **P1-critical — Narrator advanced observability:** subagent execution, hooks/sidecars, compact/recovery, catch-up cursors, usage/cost persistence, and WS event parity are replacement-candidate required for existing TS sessions that use agent tooling. Evidence: Narrator P1.
- **P1-deferred — Runtime fallback quality:** gateway, MCP, update/runtime/storage/admin, benchmark workflows, and remaining volume-snapshot event/locking proof may stay degraded for compatibility beta if the fallback contract is met; full runtime parity is required for broad replacement. Evidence: Runtime P1.
- **P1-deferred — Frontend fallback quality:** update disabled UI, provider pages, and volume-snapshot success/fallback/error states need stable contract tests; they may be deferred only with visible unsupported states and no crashes. FS navigation/mkdir/reveal, MCP/custom-subagent empty states, snapshot field aliases, and project routine `override`/`globalEnabled` fields are already covered or accepted by current Go. Evidence: Frontend P1.

### Cross-track P2 Rollup

- **Documentation/extensions:** document Go-only routes, health/version metadata, optional degraded edge features, project-db/status helpers, and accepted persistent-share semantics as compatibility extensions where accepted. Share restart/expiry/privacy behavior still needs smoke evidence before replacement readiness. Evidence: API/Runtime/Frontend P2.
- **Polish and validation:** centralize enum constants/validators, verify index assumptions, improve graph/git-panel fidelity beyond the P1 discard-all safety issue, title/retry/fork compressed-context polish, usage display, admin 403 UX, and optional tool-call/detail fields. Evidence: DB/Domain/Narrator/Frontend P2.
- **Safety maintenance:** keep output limits, timeouts, explicit admin intent, and non-destructive cleanup constraints when adding parity for storage/runtime/benchmark/container flows. Evidence: Runtime P2.

### Recommended Execution Order

| Phase | Work items | Required tests | Exit condition | Artifacts |
|---|---|---|---|---|
| 1. Safety gates | Notification-sound auth, Git-missing guard, settings secret masking, share expiry/fallback serving, narrator permission/plan-mode safety, provider allowlist | AUTH-NS-01, API-GIT-01, SETTINGS-SECRET-01, RUNTIME-SHARE-01, NARRATOR-PERM-01 | No auth bypass, raw secret, unexpected mutation, or unsafe provider/tool start | HTTP transcripts, secret-scan evidence, file/DB diffs |
| 2. Capability + fallback contract | Schema/FTS readiness, supported-feature matrix, disabled/fallback responses for ruler, batch merge, container proxy, VNet, snapshots, shares, unsupported runtimes | DB-FTS-01 plus disabled-flow checks from relevant rows | Every unsupported P0 is either hidden/disabled before mutation or remains a blocker | Readiness payload, feature matrix, frontend screenshots |
| 3. Domain integrity | Root/trunk/status invariants, dormant/wake worktree consistency, merge protections, project deletion cleanup, fork-at-message/inherit modes, interactive batch merge | DOMAIN-CHAPTER-01, DOMAIN-MERGE-01 | DB/git/worktree side effects match TS or unsupported modes reject before mutation | DB row diffs, git status/log, worktree listing |
| 4. Frontend contracts | Narrator WS names and `messageUuid`, batch merge session fields/events, ruler fields/lifecycle, terminal reconnect/fallback, snapshot `chapterId`, provider/settings payloads | NARRATOR-WS-01, DOMAIN-RULER-01, RUNTIME-TERM-01, RUNTIME-SNAPSHOT-01, FRONTEND-NOCRASH-01 | No frontend uncaught exceptions; required fields/events present or visibly disabled | WS/SSE logs, browser console, screenshots |
| 5. Runtime side effects | Terminal scrollback/dtach, container proxy/ports, real volume snapshots, VNet relay-only/UDP fallback behavior, share lifecycle | RUNTIME-TERM-01, RUNTIME-CONTAINER-01, RUNTIME-SNAPSHOT-01, RUNTIME-SHARE-01 | Real side effects match TS, or fallback/degraded contract is proven with no misleading success | Podman/terminal/VNet logs, port/proxy state, archive evidence |
| 6. Data/search/narrator fidelity | FTS-backed search, FTS lifecycle validation, content-block/tool transcript replay, usage/cost persistence, catch-up semantics | DB-FTS-01, NARRATOR-WS-01, NARRATOR-PERM-01 | TS-compatible fixture diff passes for required fields, events, DB rows, and side effects | Baseline diff report, DB snapshots, SSE/WS transcripts |
| 7. Exit smoke | Run the full smoke suite in the table above | All smoke IDs | P0 rows are `PASS` or `DISABLED-BY-DESIGN`; P1-critical rows required for replacement candidate are `PASS` | Commit/fixture/OS/binary record and artifact index |
| 8. Deferred parity | P1-deferred/P2 follow-ups: optional-table docs, backup/import scope, runtime fallbacks, graph/git polish, Go-only extension docs | Targeted endpoint/UI checks | Accepted degradations are documented and stable | Release notes or spec update |

### Replacement Readiness Checklist

- [ ] All P0 items above are fixed, or the affected feature is explicitly disabled with API and frontend-visible unsupported/fallback behavior.
- [ ] Startup/readiness reports the exact mode: TS-migrated compatibility mode or standalone replacement mode, with schema/FTS/provider/runtime capability checks.
- [ ] Security-sensitive contracts match TS: auth boundaries, admin-only routes, masked secrets, Git-missing 503 shape, and narrator write/exec permission safety.
- [ ] Existing TS-created data loads without corruption or 500s for projects, chapters, graph/ruler, narrators/messages/tool calls, terminals, settings, and optional tables.
- [ ] Frontend smoke checklist passes without crashes or misleading success for degraded features.
- [ ] Runtime smoke tests prove terminal reconnect/replay, container/proxy/port behavior, snapshots, share expiry/fallback behavior, MCP/gateway/VNet/update/storage/benchmark states, and missing external binary handling.
- [ ] Narrator smoke tests prove content-block replay, permission decisions, plan mode, provider allowlist, compact, subagents, buffers/goals, usage/cost, and WS event compatibility.
- [ ] Release notes or docs identify accepted P1/P2 degradations and Go-only extensions.

### Notes

- **Current readiness: unsafe / not replacement-ready.** This conclusion is evidence-based: all tracks have Verified/Done sections, and multiple P0 blockers remain unresolved.
- The closest safe near-term target is **scoped compatibility beta**, not transparent replacement: keep Go behind explicit capability checks and UI fallbacks until all P0s are fixed or deliberately disabled.
- Do not claim replacement candidate until all P0s are closed, critical P1s are addressed, and the cross-track smoke suite has recorded passing results.

## 2026-05-15 Revalidation Delta and Subagent Dispatch Plan

Owner: main narrator  
Status: Done  
Source: main-thread code review plus `codex:gpt-5.5` subagents `api-audit`, `db-audit`, `core-audit`, `agent-audit`, and prior `realtime-audit` output. No database, migration, server, Podman, or full Go test run was executed during this revalidation.

### Delta Notes Against Earlier Sections

- **Notification-sound public mutation blocker is obsolete in current Go.** The Go router registers `notification-sounds` inside the authenticated API group (`go_backend/internal/app/router.go`), and `TestNotificationSoundRoutesRequireAuth` asserts unauthenticated POST/GET/DELETE return 401. Keep an auth-boundary smoke test, but remove this from active P0 implementation work.
- **Changelog is partial, not missing.** Current Go has `/api/changelog`, but it reads file-system candidate directories and does not match TS embedded-changelog behavior. Track as P2/P1 ops parity, not a missing route.
- **Chapter split route is active but still proof-gated.** Current Go `split` no longer delegates to a shallow fork: it returns a full prefix/continuation/new-fork `SplitResult` and has focused coverage for edge rewiring, narrator ref truncation, container autostart scope, and rollback. Track remaining work as broader TS-vs-Go semantic proof plus compressed AI summary fallback quality, not route absence or shallow-fork behavior.
- **Many previous wave items are marked DONE because fallback behavior was accepted.** Treat fallback completion as compatibility scaffolding, not as proof that Go is replacement-ready.

### Wave H — Revalidated P0/P1 Blockers

| ID | Owner | Priority | Scope | Acceptance criteria | Verification |
|---|---|---:|---|---|---|
| H01-admin-boundary-current | codex-h01-api | P0 | Implemented 2026-05-15: re-checked and fixed current admin-only mismatches for `/api/chapters/podman/install`, `/api/storage/*`, and benchmark result/summary/compare paths. | Normal users are denied before privileged mutations/scans with TS-compatible 403 `FORBIDDEN`; admin smoke reaches safe handlers. | Focused route tests added in `go_backend/internal/app`, `chapters`, and `systemroutes`; TS evidence: `server/routes/chapters.ts`, `storage.ts`, `benchmarks.ts`. Attempted `go test ./internal/app`, `go test ./internal/chapters`, `go test ./internal/systemroutes`, `go test ./internal/misccompat`; blocked before H01 test execution by unrelated `internal/ws` build errors and `internal/narrators` duplicate helper declarations. |
| H02-api-contract-guard | Wave H02 implementation subagent | P0 | **Implemented 2026-05-15:** Go now applies the TS `GIT_NOT_INSTALLED` guard only to `GIT_REQUIRED_RULES`-equivalent project/chapter/ruler/git-panel routes, aligns `GET /api/narrators?standalone=true` to the TS paginated object shape, rejects bare `GET /api/narrators` with `VALIDATION_ERROR`, and returns `TOKEN_EXPIRED` for expired JWTs. | Frontend-required narrator list response shape matches TS; Git-missing routes return stable 503 `GIT_NOT_INSTALLED`; expired tokens return a distinct expired code; setup/settings/provider/search/standalone narrator routes are not over-blocked by the Git guard. | PASS: `go test ./internal/app ./internal/auth ./internal/narrators ./internal/gitroutes ./internal/chapters`. Focused tests cover guard matching/response, non-Git over-block checks, standalone list shape, bare list validation, and expired JWT code. |
| H03-agent-history-content-json | codex-h03-agent | P0 | **Implemented 2026-05-15; narrowed 2026-05-30:** Go now rebuilds replay history from `content_json` and completed tool-call rows instead of `content_text` only. Preserves text, tool_use, tool_result, supported reasoning/redacted thinking, compact boundaries, trailing tool results, sidecars, uploaded images, text-file hints, web-search context/native Responses items, and OpenAI Responses image-generation items. | Existing TS-created sessions with prior tool calls can continue in Go without losing the core tool transcript context; remaining risk is live/long-session fixture breadth, not core transcript loss. | PASS: `go test ./internal/narrators`; PASS: `go test ./internal/agentbasic`. Fixture tests cover multi-turn tool calls, Responses request shape, Anthropic thinking/redacted blocks, compact summary replay, unsupported-reasoning degradation, image/text-file replay, and native web-search/image-generation replay. |
| H04-subagent-message-tree | Wave H04 implementation subagent | P0 | **Implemented 2026-05-15:** Go now binds subagent user/assistant/tool-result message records to the parent Agent tool use via `parent_tool_use_id`, including foreground/background Agent launches and later Send/TeamStatus message paths. Child message broadcasts are also mirrored to the parent narrator with child/parent tool metadata. | Parent narrator message tree shows complete subagent child output; reconnect/catch-up preserves child tree and statuses. Detach/attach parent-blocking semantics are now covered by H10. | PASS: `go test ./internal/narrators -run 'TestH04|TestSubagent|TestMessagesAfter|TestMessagesCursor|TestBackgroundAgent'`; PASS: `go test ./internal/narrators`; PASS: `go test ./internal/ws`. Focused tests assert `parent_tool_use_id`, parent tree/catch-up, parent WS broadcast, foreground/background Agent, Send/Await, and TeamStatus. |
| H05-tool-registry-mcp | Wave H05 implementation subagent | P0/P1 | **Phase 2 implemented 2026-05-18:** Go static tool specs still resolve through the inventory layer, and connected stdio MCP tools are now injected into narrator provider tool lists as `mcp__server__tool` entries with guarded execution. MCP behavior permissions (`readOnly`/`readWrite`/`ask`/`deny`) and subagent-visible MCP filtering now follow the TS contract for the stdio path. | Connected stdio MCP tools can be called from Go narrator sessions through per-call JSON-RPC stdio processes; denied/unavailable tools are not exposed and hallucinated external tools are guarded. Persistent MCP runtimes and non-stdio transports remain documented follow-ups. | PASS: `go test ./internal/agentbasic`; PASS: `go test ./internal/mcpcompat`; PASS: `go test ./internal/narrators`. Added coverage for dynamic MCP inventory/provider formatting, guarded external execution, stdio fake-server call flow, permission mapping, first-token/silent-disconnect handling, and subagent disabled-tool filtering. |
| H06-schema-fts-readiness | Wave H06 implementation subagent | P1-critical | Implemented 2026-05-15: Go read-only readiness now checks canonical tables/columns plus high-value TS indexes/unique constraints, representative core FKs, FTS5 trigram external-content SQL, and FTS sync triggers; Go settings defaults were aligned with TS for first-write defaults that Go may create. | Go still does not mutate the main DB canonical schema; readiness failures name exact missing/incompatible objects and instruct operators to run TS Drizzle/FTS setup. Go-owned optional tables such as `narrator_browser_sessions` remain outside canonical readiness. | PASS: `go test ./internal/db ./internal/config ./internal/settings`. Static schema mutation guard remains in `go_backend/internal/db` and passed as part of `./internal/db`. |
| H09-browser-session-runtime | main narrator | P0/P1 | **Implemented 2026-05-18; narrowed 2026-05-29:** Go Browser sessions are narrator-bound in the Rod runtime, persisted to a Go-owned optional `narrator_browser_sessions` table when explicitly enabled, and surfaced through lifecycle observer events. Browser API list returns frontend-compatible millisecond fields; empty list is sampled in the TS-Go harness; close/TTL/screenshot/stop-tracing now try live sessions before storage fallback, missing persisted rows return 404 instead of success-shaped bodies, screenshots include no-store caching, screenshots/traces use bounded metadata persistence, and health capability reports supported `go-rod` runtime with Chrome required. | Browser sessions no longer masquerade as disabled fallback when Go owns a live runtime; cross-narrator session access is denied, active count/lifecycle broadcasts are emitted even when optional persistence is default-off, and runtime scan/cleanup can combine live and persisted sessions. | PASS: `go test ./internal/agentbasic`; PASS: `go test ./internal/narrators -run 'TestBrowserSession' -count=1`; PASS: `go test ./internal/app -run 'TestPhase0HealthCapabilitiesExposeKnownGoFallbacks|TestFrontendContractSmokeHTTPPaths' -count=1`; PASS: full `bun go_backend/testtools/ts-go-api-contract-diff.ts` (`diffs: []`). Added unit/contract tests for narrator binding, resource limit, expiration observer, optional table creation, bounded artifact persistence/count, list ms shape, default-off broadcasts, missing-row 404s, and capability contract. |
| H10-subagent-detach-reattach | main narrator | P0/P1 | **Implemented 2026-05-18:** Go subagents now detach foreground Agent calls into background tasks that unblock parent-blocking Await/Agent tool calls, persist aliases/task rows, broadcast detach/start/status events, and inject completed/failed/cancelled background results back into the parent `narrator_tool_calls` output. Attach/Await handle running/completed/stale background agents, Send continuations persist the caller tool-use id, and stale running tasks are reconciled to failed instead of hanging. | Detach no longer waits for timeout to unblock the parent; background completion survives reconnect/history replay through DB result injection; stale runtime recovery prevents permanent running state after restart. Remaining parity gaps are TS abort-controller source swapping, richer team inbox/file-change state, and custom prompt/runtime polish. | PASS: `go test ./internal/narrators`; PASS: `go test ./internal/app`; PASS: `go test ./...`. Added tests for parent-blocking detach, attach active/stale behavior, result injection, cancellation injection, Send parent-tool-use persistence, and capability contract. |
| H07-domain-split-sync | codex-h07-domain + main narrator | P1-critical | Phase 1 originally disabled shallow split before mutation; current Go supersedes that fallback with active split-at-commit prefix/continuation/new-fork behavior, narrator ref truncation, child/edge rewiring, route response shape, container autostart scope, and rollback coverage. Manual `chapter_edges` create/delete remains restricted to dependency edges; graph/git responses expose commit-sync/worktree-watcher fallback metadata. | Split no longer masquerades as a shallow fork; replacement readiness still requires broader TS-vs-Go split fixtures across real histories and compressed AI summary fallback quality. Manual edges cannot corrupt fork/merge/review graph semantics; background watcher parity remains a documented gap. | PASS: `go test ./internal/chapters -run 'TestSplitAtCommit|TestSplitRouteReturnsFullSplitResult' -count=1`; edge/graph/git focused tests pass in prior H07 evidence. |
| H08-runtime-ws-terminal-static | Wave H08 implementation subagent | P1-critical | First-phase runtime contract fixes landed: WS JSON heartbeat ping/pong, explicit `/ws/vnet` disabled WebSocket/API contract, terminal backpressure guard plus last-input-client resize, terminal Windows capability metadata, static frontend/API-only fallback, health version/commit/capabilities, changelog override dir, and update manual-only metadata. Changelog binary embedding remains a follow-up unless accepted via filesystem/API-only capability. | Long-lived narrator/terminal WS has heartbeat coverage; unsupported VNet is disabled before side effects; Go declares static frontend vs API-only mode and update manual-only mode. | Added focused tests for WS heartbeat/VNet, terminal resize/backpressure/capabilities, health/changelog/update/static contracts; ran `go test ./internal/ws ./internal/terminalruntime ./internal/systemroutes ./internal/app`. |

#### H04 Result — 2026-05-15

- `go_backend/internal/narrators/service.go` now resolves a stable parent Agent tool-use id for
  subagent message inserts. The initial Agent prompt is inserted with the known tool id before the
  child run starts, and subsequent user/assistant/system/draft messages infer the same id from the
  subagent's existing child message tree. This fixes the prior race where only the initial user
  message was patched after the run had already been launched.
- Subagent child message broadcasts are mirrored onto the parent narrator subscription with
  `childNarratorId` and `parentToolUseId`, while preserving the child narrator's own message payload.
  Parent message pagination and catch-up therefore attach complete child output under the parent
  Agent tool call instead of losing later assistant/tool-result output.
- Focused coverage was added in
  `go_backend/internal/narrators/subagent_message_tree_test.go` for simulated agent events/tool
  results, foreground Agent, background Agent, Send/Await, TeamStatus list, DB `parent_tool_use_id`
  assertions, parent message tree, catch-up, and parent WS-style broadcasts.
- Test result: `go test ./internal/narrators -run 'TestH04|TestSubagent|TestMessagesAfter|TestMessagesCursor|TestBackgroundAgent'`
  passes; `go test ./internal/narrators` passes; `go test ./internal/ws` passes.
- Remaining H04 limitations: detach/attach still use the existing explicit Go fallback contract rather
  than re-blocking the original parent tool promise; MCP/dynamic tool registry and custom subagent
  prompt/tool filtering remain H05; sidecar/native web-search/image-generation subagent parity remains
  outside this H04 message-tree fix.

#### H02 Result — 2026-05-15

- `go_backend/internal/app/router.go` now installs an authenticated API middleware that mirrors the TS
  `GIT_REQUIRED_RULES`: project create, ruler routes, chapter create/delete/cleanup/batch-merge,
  chapter lifecycle/merge/review/git-status actions, git-panel endpoints, and commit file diff routes
  return `503 { code: "GIT_NOT_INSTALLED" }` when Git is unavailable.
- Non-Git routes remain outside the guard so first-time setup and normal database/API usage are not
  blocked by missing Git. Focused checks cover settings, provider models, search, and standalone
  narrator list paths as non-guarded examples.
- `GET /api/narrators?standalone=true` now uses the same paginated response envelope as TS:
  `{ items, hasMore, nextCursor, totalCount }`. Bare `GET /api/narrators` no longer falls through to
  a full-table list; it returns a stable `400` `VALIDATION_ERROR` requiring `chapterId` or
  `standalone=true`.
- Expired JWT verification now returns a distinct `401` `TOKEN_EXPIRED` error while keeping malformed
  or missing auth tokens on the existing `UNAUTHORIZED` path.
- Test result: `go test ./internal/app ./internal/auth ./internal/narrators ./internal/gitroutes
  ./internal/chapters` passes.
- Remaining H02 limitation: this is a route-level contract guard; downstream Git behavior inside
  routes that are intentionally unguarded because TS permits database fallback (for example commit
  list/detail routes) still depends on each handler's existing fallback/error handling.

#### H05 Phase 1 Result — 2026-05-15

- Added `go_backend/internal/agentbasic/tool_inventory.go` as the first-phase Go inventory layer for static agent tools. It classifies tools as `core`, `optional`, `review`, `reflection`, or `mcp`, excludes optional tools by default, includes review tools only in review scope, includes reflection tools only in reflection scope, and supports encoded per-run enabled/disabled filters.
- Narrator runs now pass `narrators.enabled_tools` and the `custom-disabled-tools:` trait into `agentbasic.RunAgentLoop`. The filtered inventory is used for provider tool definitions, and a guarded executor returns a denied tool result if a hidden/disabled/unavailable static tool is hallucinated.
- Narrator run start broadcasts a non-persistent `tool_inventory` event containing inventory metadata. With the 2026-05-18 update, connected stdio MCP tools are reported as supported partial parity and injected into provider tool definitions; `/api/mcp/tools` reports `externalMCPToolInjection.supported:true` with `toolCount` and a clear no-connected-tools reason when empty.
- Added focused contract coverage for normal/review/reflection scopes, optional `enabledTools`, disabled filtering, provider request filtering during a run, dynamic MCP provider formatting, guarded hallucinated `mcp__` calls, stdio fake MCP `tools/call`, permission mapping, and disabled-tools trait parsing.
- Test result: `go test ./internal/agentbasic`, `go test ./internal/mcpcompat`, and `go test ./internal/narrators` pass.
- Remaining H05 risks: stdio MCP uses per-call short-lived JSON-RPC processes instead of TS-style persistent clients; sse/streamable-http transports remain disabled; unsupported TS optional tools such as Terminal/Recall/ForkNarrator/NarraForkAdmin are not fully ported; custom subagent-specific dynamic tool filters beyond static enabled/disabled inventory still require TS-parity follow-up.

#### H06 Result — 2026-05-15

- `go_backend/internal/db/canonical_schema.go` now keeps the main database TS-owned/read-only while
  expanding readiness beyond tables/columns to cover high-value TS indexes/unique indexes, core
  relational FKs used by graph/narrator/terminal/usage/background routes, FTS5 trigram
  external-content table SQL, and the nine TS FTS sync triggers for chapters, narrator messages, and
  narrators.
- Missing/incompatible readiness objects now fail with precise object names and remediation guidance:
  start the TypeScript backend to run Drizzle migrations and FTS setup. Go does not attempt CREATE,
  ALTER, DROP, index creation, trigger creation, or FTS rebuild on the main DB.
- Added db tests for complete readiness, missing table/column, missing index, non-unique replacement of
  a required unique index, missing FK, non-trigram FTS SQL, missing FTS trigger, optional Go-only table
  exclusion, and read-only mismatch behavior. The existing schema mutation guard remains part of the
  db package.
- Go settings first-write defaults now match the TS defaults for core sections Go can create:
  default/summary models, agent safety/retry/compact fields, chapters, containers, editor, routines,
  codex, update, vnet, shares, server/path/auth, and TS provider arrays. Previous Go-only defaults such
  as `server.tls`, `agent.contextThresholds`, provider-extension arrays, `kiro`, and `mcpServers` are no
  longer written on first load; the settings routes still provide runtime fallback/augmented values.
- Remaining H06 limitation: FK validation is a high-value core subset rather than an exhaustive copy of
  every TS `references()` declaration; full FTS rebuild/corruption repair remains intentionally TS-owned
  unless a future track adds a safe replacement-mode bootstrapper.
- Test result: `go test ./internal/db ./internal/config ./internal/settings` passes.

#### H07 Phase 1 Result — 2026-05-15; split status superseded by later Go implementation

- Historical note: an earlier Go compatibility phase disabled `POST /api/chapters/:id/split` with a stable `501` response to avoid a misleading shallow fork. That status is no longer current: Go now serves the split route with `201` and a full `SplitResult`, backed by `SplitAtCommit` focused coverage for prefix/continuation/new-fork creation, child/edge rewiring, narrator ref truncation, container autostart scope, route response shape, and rollback of DB/git artifacts (`go_backend/internal/chapters/handlers.go:365-400`, `go_backend/internal/chapters/split_at_commit_test.go:21-277`).
- Manual `chapter_edges` operations now only allow `dependency`. Lifecycle-owned edges
  (`fork`, `merge`, `review`, `cherry_pick`) remain readable but cannot be manually created/deleted
  through the generic edge route, preventing redundant field/graph corruption.
- Graph responses expose domain fallback capabilities for dependency-only manual edges and commit-sync/worktree-watcher status. Git commit responses include `commitSync` metadata showing the current manual-route-record fallback and `worktreeWatcher:false`.
- Focused evidence added in this track: edge self/cross-project/type/manual restriction tests, graph capability/git metadata assertions, and git commit-sync metadata/assertions. Split evidence is now in `go_backend/internal/chapters/split_at_commit_test.go` and should be treated as active-but-still-needing broader TS-vs-Go fixture proof rather than disabled-by-design.
- Remaining H07 gaps: broader TS-vs-Go split semantic proof across real project histories, compressed AI split summary parity/fallback quality, background commit-sync, and TS-equivalent worktree watcher behavior.

### Wave I — Follow-up Parity Tracks After H P0 Closure

| ID | Owner | Priority | Scope | Acceptance criteria | Verification |
|---|---|---:|---|---|---|
| I01-project-db-incremental | codex-i01-db-domain | P1 | Decide whether Go project backup DB is TS-compatible or Go-extended; add metadata/versioning and event-driven incremental sync or explicit manual-only semantics. | Project DB behavior is documented, versioned, and safe for import/export; large projects avoid unbounded full-copy surprises. | Backup/import fixture; metadata assertions; large-table bounded-copy test. |
| I02-snapshot-rebuild | codex-i02-domain-agent | P1 | Port TS file-state rebuild semantics for delete block/message, rollback, revert, and fork-at-message file state. | Surviving Write/Edit chains are replayed correctly; no over-rollback to original snapshot when later edits should survive. | Tool-call chain fixture; file diff assertions before/after delete/rollback/revert. |
| I03-compact-title-recovery | codex-i03-agent | P1 | Replace compact/compressed fork/title fallback paths with summary-model-backed behavior or explicit degraded quality markers; add context-overflow/retry cleanup parity. | Long sessions, compressed forks, plan compact, and title generation behave like TS or declare quality fallback to UI/API. | Compact/fork/title/retry fixture tests; provider-gated tests disabled unless credentials are present. |
| I04-notifications-runtime-events | codex-i04-runtime | P1/P2 | Add narrator status notification event chain and runtime event parity for review/merge/batch/ruler/container paths. | Done/waiting notifications follow user preferences; frontend receives required lifecycle events without manual refresh. | Notification webhook mock tests; WS event golden transcripts. |
| I05-frontend-contract-suite | codex-i05-frontend | P1 | Turn the smoke checklist in this spec into executable Go contract tests or scripted fixtures. | Replacement readiness is backed by repeatable artifacts instead of manual inspection. | Test runner output plus stored HTTP/WS/SSE/browser artifacts. |

### Dispatch Rules For Implementation Subagents

1. Launch one `codex:gpt-5.5` subagent per row unless two rows explicitly share an owner.
2. Each subagent must first re-read the current code and update this section if a finding is stale.
3. No subagent may mutate `~/.narrafork/narrafork.db`, run migrations, or edit `drizzle/`.
4. Each implementation must include focused tests before broad tests.
5. If a feature is intentionally unsupported, the subagent must implement the explicit disable/fallback contract: no side effects, stable machine-readable response, capability metadata, and frontend-visible disabled state where applicable.
6. Main narrator owns final synthesis and must not mark replacement-ready until H01-H08 are fixed or accepted as disabled-by-design with smoke evidence.

## 2026-06-06 Codex Multi-Agent Gap Audit

Owner: main narrator
Status: Drafted / locally spot-checked
Source: five read-only `nug2:codex:gpt-5.5` subagents plus local `Read`/`Grep` spot-checks.
Agents: `gap-api`, `gap-db`, `gap-services`, `gap-agent`, `gap-contracts`.
Verification scope: source inspection only. No server was started, no database was opened or migrated,
no Podman/runtime flow was exercised, and no broad Go/TS contract suite was run during this audit.

### Audit Conclusion

The current Go backend is much closer than the original baseline, but this fresh audit still does not
support a transparent TS-backend replacement claim. The remaining work is a mix of:

- real feature gaps that need implementation if Go is to be a replacement;
- accepted compatibility fallbacks that must stay clearly capability-gated in the frontend;
- contract risks where Go has route/test coverage but not enough TS-vs-Go golden evidence for
  behavior, event order, DB rows, or runtime side effects.

Compatibility beta remains plausible when P0 fallbacks are explicit and UI-gated. Replacement readiness
requires closing or formally accepting every P0 below and adding repeatable HTTP/SSE/WS/DB/runtime
golden evidence.

### Fresh P0 / P1 Gap Board

| ID | Priority | Area | Current finding | Evidence sampled | Required acceptance gate |
|---|---:|---|---|---|---|
| J01-db-ownership-bootstrap | P0 mostly closed for transition; replacement still blocked | DB startup / migration ownership | Go now reports SQLite TS-owned mode as a `strategyStage:"transition"` compatibility strategy with target `pure-go-db-management`, and the default SQLite config is `read-only-ts-owned`. Default `Open()` refuses a missing TS DB without creating `.narrafork`/`narrafork.db`; explicit `bun-final-compatible` remains a legacy/test bootstrap helper and future Go-owned primitive, not the transition default. | Go: `go_backend/internal/db/config.go`, `go_backend/internal/db/db.go`, `go_backend/internal/db/db_open_safety_test.go`, `go_backend/internal/db/sqlite_bootstrap.go`, `go_backend/internal/db/canonical_schema.go`; TS: `server/db/index.ts`, `server/db/run-migrations.ts`. | Transition acceptance: keep default startup read-only for schema ownership, keep legacy bootstrap opt-in only, and keep tests proving missing DB is not created and compatible DB opens without main-schema object mutations. Replacement acceptance remains separate: port TS `runMigrations`, `ensureColumns`, startup backfills, and FTS setup into pure Go with old-DB fixtures before changing current ownership to Go. |
| J02-fts-repair-contract | P0 mostly closed for transition; replacement still blocked | DB / FTS | `CanonicalSchemaContractReport` and health capabilities identify SQLite as transitional TS-owned/read-only with `currentRepairOwner:"typescript-drizzle-and-ts-fts-setup"` and `GoFTSRepair:false`. Default verification/Open now treats missing FTS, non-trigram FTS, and missing triggers as `ErrCanonicalSchemaMismatch` without creating, dropping, recreating, rebuilding, or clearing `application_id`; mutating `EnsureSQLiteFTS` is documented as explicit legacy/future-Go-owned helper only. | Go: `go_backend/internal/db/canonical_schema.go`, `go_backend/internal/db/canonical_schema_test.go`, `go_backend/internal/db/sqlite_fts.go`, `go_backend/internal/db/sqlite_bootstrap.go`; TS: `server/db/fts.ts`. | Transition acceptance: keep FTS repair out of default startup and preserve tests for missing/non-trigram/trigger drift. Replacement acceptance remains separate: implement explicit Go-owned FTS setup/repair/rebuild with tests for non-trigram FTS, missing triggers, unclean marker, and rebuild failure before making Go the current repair owner. |
| J03-settings-load-and-zod | P0 partially narrowed | Settings persistence / validation | Go health still declares `settings.validation.tsZodParity=false`, but load now runs a bounded TypeScript-legacy migration pass before saving. Covered migrations include `editor.legacyEncoding`, `agent.yoloSkipReadOnlyConfirmations`, `smartInterruptionCheck`, `defaultPermissionMode:"plan"`, legacy `openai`, provider prefixes, flat `subagentAllowedModels`, MCP `allow`, null Codex reasoning effort, and proxy URL normalization; tests prove persisted migration and unknown-field preservation. | Go: `go_backend/internal/config/config.go`, `go_backend/internal/config/config_test.go`, `go_backend/internal/app/router.go`; TS: `server/lib/settings/index.ts`, `server/routes/settings.ts`, `server/lib/settings/defaults.ts`. | Remaining replacement blockers: full TS Zod update schema parity, legacy Codex OAuth credential-pool migration, full custom API provider protocol normalization, and stale model-reference purge. Keep capability `loadMigrationParity:"partial"`, `patchValidationParity:"partial"`, and `knownMissingMigrations` visible until those are covered. |
| J04-kiro-chat-sse | Accepted disabled-by-design for compatibility beta; replacement still blocked | Provider route / SSE | TS exposes `/api/kiro/chat` as SSE. Go still returns `FEATURE_DISABLED` and health reports `providers.kiro.routes.chat=false`; frontend now has `assertKiroChatRouteSupported` / `guardedKiroStreamChat`, and the low-level stream helper is documented as requiring route-capability gating. | Go: `go_backend/internal/systemroutes/handlers.go`, `go_backend/internal/app/router.go`; TS: `server/routes/kiro.ts`; frontend: `frontend/lib/api/streams.ts`, `frontend/lib/api/streams.test.ts`. | Replacement requires TS-equivalent Kiro chat SSE. Compatibility acceptance requires keeping disabled payload stable and using the guarded frontend helper for any future Kiro chat UI. |
| J05-storage-maintenance-fallbacks | P0 for replacement; P1 for compatibility beta | Storage / maintenance | TS storage cleanup accepts `uploads`, `shares`, `worktrees`, and `containers`; Go supports uploads/shares but returns unsupported for worktrees/containers, disables `staleSessions` cleanup, and returns conflict for request-path VACUUM. | Go: `go_backend/internal/misccompat/handlers.go`, `go_backend/internal/app/router.go`; TS: `server/routes/storage.ts`, `server/services/storage-service.ts`. | Either implement bounded TS-equivalent cleanup, or keep capability-gated UI disable/reroute to runtime cleanup. Contract tests must prove no misleading success and stable unsupported codes for worktrees, containers, staleSessions, and VACUUM. |
| J06-mcp-transport-scope | Accepted stdio-only fallback for compatibility beta; replacement still blocked | MCP | Go supports MCP settings, built-in tools, and stdio external tool injection, while SSE and streamable HTTP transports remain disabled. Frontend creation/testing/connection are capability-gated, Kiro MCP tools now use the explicit provider route capability, and MCP JSON import skips unsupported non-stdio server configs instead of saving unusable records silently. | Go: `go_backend/internal/app/router.go`, `go_backend/internal/mcpcompat/handlers.go`, `go_backend/internal/agentbasic/tool_inventory.go`; frontend: `frontend/routes/routines/index.tsx`; TS: `server/routes/mcp.ts`, `server/lib/mcp/*`. | Replacement requires non-stdio transports or an explicit product decision to stay stdio-only. Continue testing non-stdio create/test/connect/import as stable disabled/skipped behavior. |
| J07-agent-tool-runtime-partials | P1 capability contract implemented; runtime gaps remain | Agent tools | Go tool inventory now exposes a machine-readable capability contract for degraded tools: `schemaCompatible`, `runtimeCompatible`, `knownDegraded`, `fallbackBehavior`, and `frontendRendering`. `Read`, `WebFetch`, `Browser`, and unsupported optional tools (`ForkNarrator`, `NarraForkAdmin`) have tests locking their degraded/unsupported metadata. | Go: `go_backend/internal/agentbasic/tool_inventory.go`, `go_backend/internal/agentbasic/tool_inventory_test.go`, `go_backend/internal/app/router.go`; TS: `server/lib/agent/tools/*`. | Runtime gaps remain intentionally explicit: no PDF/image Read blocks, WebFetch stays raw-only, Browser uses file/base64 artifacts instead of TS image/share preview blocks. Frontend tool cards should continue to consume the capability contract and render degraded artifacts safely. |
| J08-agent-loop-event-parity | P0 evidence baseline started; replacement still blocked | Narrator runtime / WS / SSE | Go now has first-wave transcript golden tests for current frontend contract: text-only, reasoning+text, tool success/fail lifecycle, and the closest permission lifecycle path. These lock DB rows and broadcast frames as Go behavior baselines, not full TS equivalence. TS-only loop hooks (`onBeforeTurn`, runtime/model override hooks, `shouldStop`, SSE emitter, richer dual-broadcast semantics) remain open. | Go: `go_backend/internal/narrators/agent_event_transcript_contract_test.go`, `go_backend/internal/narrators/agent_event_handler.go`, `go_backend/internal/ws/hub.go`; TS: `server/lib/agent/types.ts`, `server/lib/agent/loop.ts`, `server/services/narrator-event-handler.ts`, `server/websocket/narrator-ws.ts`. | Next fixtures: subagent foreground/background dual broadcast, reconnect streaming snapshot/catch-up, invalidState/context overflow, provider queue/quota/metering, DB failure compensation, then hook parity implementation. |
| J09-domain-lifecycle-depth | P0 partially narrowed | Chapter / merge / review | Go review creation now has a rollback helper and tests proving narrator-initialization failure removes partial review chapter, review edge, narrator/message refs, worktree, and branch. Health capability now declares `review.createRollback:true`; it also explicitly declares `autoStartSession:false` / `startMode:"seed-message-only"` and `merge.aiResolveMode:"one-shot-basic"` because full TS review auto-start and full-narrator AI conflict resolve are still not implemented. | Go: `go_backend/internal/chapters/service.go`, `go_backend/internal/chapters/review_parity_test.go`, `go_backend/internal/app/router.go`; TS: `server/services/chapter-merge.ts`, `server/services/review-service.ts`, `server/routes/reviews.ts`. | Remaining replacement blockers: review auto-start session parity, AI resolve through full narrator tool/permission loop, merge summary/commit-sync golden fixtures, and convert/promote/dismiss graph/session side-effect fixtures. |
| J10-runtime-terminal-container-watcher | P1-critical | Runtime / terminal / containers / git watcher | Go has runtime implementations but exposes accepted differences: terminal replay is raw capped output (`xtermSerializedReplay:false`), container start/fallback/proxy behavior needs broad smoke coverage, worktree watcher is global polling, and commit sync may still be operation/route triggered rather than TS-equivalent event-driven sync. | Go: `go_backend/internal/app/router.go`, `go_backend/internal/terminalruntime/*`, `go_backend/internal/chapters/container_runtime.go`, `go_backend/internal/worktreewatcher/watcher.go`; TS: `server/terminal/*`, `server/services/container-service.ts`, `server/services/worktree-watcher.ts`, `server/services/commit-sync-service.ts`. | Runtime smoke must cover terminal reconnect/TUI scrollback/resize/Windows fallback, no-Podman and compose/proxy paths, dormant/wake container state, external git commits, watcher latency, and commit table/graph refresh. |
| J11-schema-contract-depth | P1-critical | DB schema verifier | Go canonical readiness is stronger than the old table-only check, but still a high-value subset: FK list is explicitly representative, and schema contract does not yet fully hash column type/not-null/default/PK/check/enum semantics from TS Drizzle. | Go: `go_backend/internal/db/canonical_schema.go`, `go_backend/internal/db/schema_snapshot.go`; TS: `server/db/schema.ts`, `server/db/relations.ts`. | Generate a full TS schema snapshot contract and make Go compare column type, nullability, defaults, PK, unique/index columns, FK actions, and relation coverage. CI must fail when TS schema/migrations change without Go snapshot refresh. |
| J12-migration-snapshot-freshness | P1-critical | DB / release | Go embeds Drizzle migration and journal snapshots. Extra TS migrations may be treated as compatible in some paths, so Go can silently lag the current repo unless freshness is enforced. | Go: `go_backend/internal/db/drizzle_migrations_embedded.go`, `go_backend/internal/db/drizzle_journal_snapshot.go`, `go_backend/internal/db/migration_compat.go`; TS: `drizzle/meta/_journal.json`, `drizzle/*.sql`. | Add strict current-repo mode in CI/release: embedded migrations, expected journal hash/count/last migration, and canonical schema hash must match the current TS repository unless Go intentionally advertises an older compatibility baseline. |
| J13-postgres-is-go-owned | P1 | DB engine scope | Go PostgreSQL mode is Go-owned and generated from simplified canonical mappings; health capability now carries `tsParityBaseline:false` so it is not mistaken for a TS/Drizzle parity baseline. TS backend has no PostgreSQL main DB schema/migration parity. | Go: `go_backend/internal/db/config.go`, `go_backend/internal/db/postgres_schema_contract.go`, `go_backend/internal/app/router.go`; TS: SQLite-only `server/db/connection.ts` and Drizzle SQLite migrations. | Keep Postgres documented as experimental/Go-owned, not TS parity. Cross-engine fixtures must prove API response compatibility before it is advertised for replacement. |
| J14-provider-manager-partials | P1 partially narrowed | Providers | Codex manager parity is still partial, Kiro model refresh/chat are capability-limited, and provider route parity still relies on expanding contracts. The unsafe Codex `usage-queue/clear` false success is fixed: Go now returns `FEATURE_DISABLED` and health reports `usageQueueClearSupported:false` / `usageQueueParity:"disabled"`. | Go: `go_backend/internal/app/router.go`, `go_backend/internal/systemroutes/handlers.go`, `go_backend/internal/systemroutes/handlers_test.go`, `go_backend/internal/agentbasic/provider_*`; TS: `server/routes/codex.ts`, `server/lib/codex-*`, `server/routes/kiro.ts`. | Remaining work: golden tests for Codex scheduler/usage queue semantics if implemented later, credential affinity, Kiro plugin/model refresh/chat states, and provider usage/cost/quota event fields. |
| J15-project-db-and-routine-artifacts | P2 / P1 when used for shared data | Project DB / routines / skills | Project backup/import and routine/skill compatibility exist, but audit found risks around manual-only project DB sync semantics and routine artifact materialization if command/skill routine types are added beyond current tool routines. Skill companion-file safety should remain covered by golden tests. | Go: `go_backend/internal/projects/*`, `go_backend/internal/simplecontent/handlers.go`; TS: `server/services/project-db-sync.ts`, `server/services/routine-service.ts`, `server/services/skill-service.ts`. | Decide whether Go project DB sync is event-driven or manual-only and expose metadata. Add routine contract tests for tool/command/skill artifact enable/disable/reset and skill tests for disabled files, project-over-global precedence, symlink/path guards, companion depth/count/size, and output shape. |

### Required Next Verification Artifacts

1. **Contract diff harness expansion:** add rows for J01-J08, including negative/disabled-by-design
   assertions so fallback success cannot be mistaken for TS parity.
2. **DB fixture pack:** empty DB, old migrated DB, partially migrated DB, latest TS DB, non-trigram
   FTS, missing trigger, and outdated settings JSON fixtures.
3. **Narrator golden transcript suite:** record TS and Go WS/SSE/DB rows for text, tool chunks,
   permission, subagents, reconnect, compact, provider usage, and runtime override/priority-buffer
   scenarios.
4. **Runtime smoke scripts:** terminal reconnect/TUI, no-Podman and compose/proxy containers,
   worktree watcher/commit sync, storage cleanup fallback, MCP stdio/non-stdio, and Kiro disabled
   chat/model refresh UI gates.
5. **Release readiness gate:** Go may be a compatibility beta only if every P0 is either implemented
   or disabled with machine-readable capability metadata plus frontend-visible fallback. Replacement
   readiness requires passing the expanded TS-vs-Go suite with no unaccepted P0 diffs.

### Locally Spot-Checked Evidence

- `go_backend/internal/db/schema.go` keeps `EnsureSchema` no-op for the main TS-owned DB.
- `go_backend/internal/db/config.go` defaults SQLite to `read-only-ts-owned`; `db.go` refuses a
  missing default TS DB without creating `.narrafork` or `narrafork.db`.
- `go_backend/internal/db/db.go` now reaches `BootstrapSQLiteBunFinalCompatible` only through
  explicit legacy `bun-final-compatible` mode, while default TS-owned verification runs
  `VerifyCanonicalSchema` and `EnsureMigrationCompatibility` without schema bootstrap.
- `go_backend/internal/db/sqlite_bootstrap.go` and `sqlite_fts.go` keep mutating bootstrap/FTS
  helpers as explicit legacy/future-Go-owned helpers, not default transition startup behavior.
- `go_backend/internal/app/router.go` reports `settings.validation.tsZodParity=false`, Kiro chat
  disabled, WebFetch raw-only, Read PDF/image unsupported, Browser share/image block unsupported,
  MCP non-stdio transports disabled, terminal `xtermSerializedReplay=false`, and storage cleanup
  worktrees/containers unsupported.
- `go_backend/internal/systemroutes/handlers.go` returns `FEATURE_DISABLED` for `kiro.chat`.
- `go_backend/internal/misccompat/handlers.go` returns unsupported storage cleanup codes for
  `worktrees` and `containers`, disables `staleSessions`, and rejects request-path VACUUM.
- `go_backend/internal/agentbasic/tool_inventory.go` marks `ForkNarrator` and `NarraForkAdmin`
  unsupported and exposes WebFetch/Read partial metadata.
- `server/services/narrator-event-handler.ts` still has TS-specific `broadcastTargetId`,
  `sseEmitter`, `dualBroadcast`, and streaming snapshot logic that should be covered by Go parity
  transcripts before replacement is claimed.
