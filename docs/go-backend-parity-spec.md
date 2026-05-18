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

## Cross-track Coordination Log

- 2026-05-14 — Spec initialized. Agents should append short dated notes here when a gap spans multiple tracks.
- 2026-05-14 — Domain Lifecycle Parity notes cross-track coverage needed for Go lifecycle smoke tests: Git-dependent project/chapter/fork/merge/review/ruler/git routes must cover Git-missing behavior against TS `GIT_NOT_INSTALLED` guard, and frontend/runtime smoke should include narrator WS/event side effects for lifecycle flows (merge/review/batch merge/ruler AI resolve).
- 2026-05-14 — Narrator/Agent track notes that frontend/runtime parity must smoke-test narrator WS event shapes (`permission_resolved`, tool progress/output, compact progress, subagent/background events, `goals_set`) because Go currently broadcasts a reduced event surface compared with TS. API/auth-boundary and Git-missing findings from sibling tracks do not change narrator P0s directly, but should be included in replacement smoke coverage for narrator tools that call Git or privileged APIs.
- 2026-05-14 — spec-api: API contract review found cross-track blockers: Go exposes `/api/notification-sounds` POST/DELETE publicly while TS mounts mutations after auth, and Go lacks TS's `GIT_NOT_INSTALLED` guard for Git-dependent routes. Runtime/frontend/domain tracks should account for setup/offline behavior and auth-boundary smoke tests.
- 2026-05-14 — spec-runtime: runtime/system review found replacement blockers in terminal WS replay/dtach/VNet, container proxy/port side effects, and volume snapshot real Podman archive flows. Frontend/API smoke tests should assert explicit fallback or disabled UI for gateway, VNet, update apply, storage cleanup, MCP external runtimes, benchmarks, and metadata-only snapshot behavior.
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
| Narrator / Agent runtime | Blocked for replacement; partial for beta | MCP persistent runtimes/non-stdio transports and Codex stateful recovery can still block agent-heavy workflows unless disabled in UI. Browser sessions, Go subagent detach/reattach parent-blocking, and rollback/edit-regenerate now have targeted parity, subject to documented runtime boundaries. | provider-specific history replay, optional tools parity, permission edge cases, output bounds, rollback/edit TS-vs-Go fixture breadth, and stale state recovery. | richer usage/cost telemetry and native provider edge parity. | Agent parity tests cover MCP, Browser, subagent detach/reattach, rollback/edit-regenerate, Codex quota/failover, permission pause/resume, and WS hydration. | Goals 5, 6, 7, 8, 9 |
| Chapter / domain lifecycle | Blocked for full parity | Go chapter split-at-commit remains disabled; graph/ruler/narrator refs semantics are not replacement-equivalent until implemented or hidden. | merge/rebase/review/ruler edge cases need TS-vs-Go fixture comparison. | additive Go chapter routes documented. | split tests cover prefix/continuation/new fork, trunk special case, chapter edges, narrator refs, invalid commit, and failure compensation. | Goal 10 |
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
- Go does not implement the TS Git-unavailable request gate that selectively returns
  `GIT_NOT_INSTALLED` for Git-dependent authenticated routes. Evidence:
  `server/app.ts:155-203`; no equivalent middleware appears in `go_backend/internal/app/router.go:77-140`.
- Public/auth boundary mostly matches TS for health/auth/status/dependencies/changelog/shares and
  authenticated API groups, but Go makes `/api/notification-sounds` public for POST/DELETE as well
  as GET. TS mounts `shareRoutes` publicly, then applies `requireAuth`, then mounts
  `/api/notification-sounds`; therefore all current TS notification-sound routes are behind auth.
  Evidence: `server/app.ts:138-153`, `server/app.ts:220`,
  `server/routes/notification-sounds.ts:55-111`, `go_backend/internal/app/router.go:69-72`,
  `go_backend/internal/misccompat/handlers.go:97-106`.
- The TS route group `/api/project-db` is mounted only under `/api/projects` for backup/import
  plus separately documented route file; Go mounts a small `/api/project-db` compatibility status
  group and also puts project backup/import under `/api/projects`. This is likely additive but
  should be kept documented. Evidence: `server/app.ts:249-250`,
  `server/routes/project-db.ts:17-46`, `go_backend/internal/app/router.go:136`,
  `go_backend/internal/systemroutes/handlers.go:118-125`,
  `go_backend/internal/projects/handlers.go:21-23`.

### P0

- **P0 — Auth boundary mismatch on notification sound mutation.** Go currently registers
  `/api/notification-sounds` before auth with POST and DELETE exposed by
  `RegisterNotificationSoundPublic`; TS mutation routes are under the authenticated router. Fix by
  making only `GET /api/notification-sounds/:id` public and moving POST/DELETE behind auth, or
  document and explicitly accept the looser boundary. Evidence: `server/app.ts:152-153`,
  `server/app.ts:220`, `server/routes/notification-sounds.ts:55-111`,
  `go_backend/internal/app/router.go:69-72`, `go_backend/internal/misccompat/handlers.go:97-106`.
- **P0 — Missing Git-unavailable API contract guard.** TS returns a stable 503 payload with
  `code: "GIT_NOT_INSTALLED"` only for known Git-dependent routes when Git is absent; Go has no
  router-level equivalent. This can make setup/offline behavior diverge and may surface lower-level
  errors on project/chapter/git routes. Evidence: `server/app.ts:155-203`,
  `go_backend/internal/app/router.go:77-140`.

### P1

- **P1 — Missing TS route group: `/api/cline` auth/provider endpoints are only partially covered
  by Go's provider registration.** Go registers many Cline paths under `RegisterProviders`, but the
  exact TS route group includes status, auth browser/cancel/callback/logout, model refresh,
  pool/search/count, enabled models, balance, recommended models, and user info refresh. Verify
  request/response parity for every Cline endpoint before claiming compatibility. Evidence:
  `server/app.ts:225`, `server/routes/cline.ts:408-684`,
  `go_backend/internal/systemroutes/handlers.go:892-907`.
- **P1 — Missing TS route group: `/api/nug` exact contract is partial.** Go registers NUG provider
  paths and the public OAuth callback, but TS includes additional quota/usage/OAuth/channel health
  semantics and provider login/API-key flows. Route names largely exist; response and error shapes
  need endpoint-level comparison. Evidence: `server/app.ts:149-150`, `server/app.ts:228`,
  `server/routes/nug.ts:245-559`, `go_backend/internal/app/router.go:75`,
  `go_backend/internal/systemroutes/handlers.go:855-865`.
- **P1 — Degraded/stub-compatible responses may satisfy path existence but not TS behavior.**
  Gateway, update apply, benchmark execution, Kiro chat, and storage cleanup return fallback
  or unavailable payloads in Go where TS has real runtimes. VNet is not repeated here as lower priority:
  until `/ws/vnet` is implemented or entry points are explicitly disabled, it stays in the Runtime P0
  WebSocket blocker. API consumers must tolerate fallback payloads or the routes need full
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

- JSON pass with additive/shape differences: `/api/health`, `/api/auth/status`, `/api/projects`, `/api/settings`, `/api/search`, `/api/update/check` all returned comparable responses, but Go added capabilities/search metadata/update instructions and omitted `settings.nkpProviders`, `settings.nugProviders`, and `updateCheck.latestVersion` versus the fresh TS fixture.
- SSE mismatch found: `POST /api/update/download` returns TS `404 application/json {error:"No update available"}` when no update metadata exists, while Go returns `200 text/event-stream` with `progress,error` and terminal `success:false`.
- WS baseline pass: `/ws/narrator` and `/ws/terminal` both opened successfully with the TS-issued token after switching to the Go backend on the same DB/settings.
- Current raw diff labels: `json.health.shape differs`, `json.settings.missingGoKey nkpProviders`, `json.settings.missingGoKey nugProviders`, `json.settings.shape differs`, `json.search.shape differs`, `json.updateCheck.missingGoKey latestVersion`, `json.updateCheck.shape differs`, `sse.updateDownload.status TS=404 Go=200`, `sse.updateDownload.events TS= Go=progress,error`, `sse.updateDownload.terminal TS=none Go=false`.

| Frontend surface | Primary routes / protocol | Transport | Current Go status | Required diff assertions | Priority |
|---|---|---|---|---|---|
| App shell, auth, version, capability gates | `/api/health`, `/api/auth/status`, `/api/changelog`; capability hooks in `usePlatform` | JSON | Mostly ready, but health/version metadata differs and disabled features must be explicit | Compare status/content-type, `version`, `commit`, `gitAvailable`, and `capabilities.*`; missing capability fields must not default unsupported Go features to enabled UI | P0 |
| Dashboard / project list / project create | `/api/projects`, `/api/projects/:id`; clone/create stream from `POST /api/projects` | JSON + SSE for clone | Route exists; clone SSE and lifecycle side effects need page-level proof | JSON CRUD shape; clone SSE `progress`, `credential_required`, `complete`, `error`; no misleading success on git/credential failure | P0 |
| Project graph / ruler | `/api/projects/:id/graph`, `/api/projects/:id/graph/positions`, `/api/projects/:id/ruler*`, chapter edges | JSON | Partial / degraded ruler semantics | Graph payload fields (`nodes`, `edges`, exploration groups, opened terminals, degraded/fallback metadata); position patch side effects; ruler mutation fallback responses | P0/P1 |
| Chapter detail / graph actions / git panel | `/api/chapters*`, fork/merge/dormant/wake/cleanup/batch-merge, `/git/*`, commits/files | JSON | Broad route coverage, but lifecycle parity partial; split remains disabled | List/detail/update field parity; TS `GIT_NOT_INSTALLED` guard; merge-check/merge/fork response shape; commit/file diff truncation; `split` must return stable `FEATURE_DISABLED` or be hidden | P0 |
| Narrator panel core | `/api/narrators/:id`, `/messages`, `/permissions`, `/goals`, `/buffer`, `/plan-mode`, `/compact`, `/retry`, `/continue`, `/rollback/:messageId`, `/edit-and-regenerate/:messageId` | JSON + `/ws/narrator` | Partial/full mixed; rollback/edit routes are active with focused Go tests | Cursor pagination, `around`/catch-up shape, multipart message submit, permission approve/deny, goals/buffer/draft updates; rollback/edit must keep TS-compatible message refs, file-state, WS, and rerun side effects | P0 |
| Narrator realtime protocol | `/ws/narrator` subscribe/catch-up/presence/stats, message/tool/permission events | WebSocket | Core smoke exists; event surface still wider than current matrix | `subscribe`, `streaming_snapshot`, `catch_up`, `sync_check`, `sync_ok`, `full_reload`, `message_updated`, `messages_deleted`, `tool_*`, `permission_*`, reflection, compact, goals, buffer, subagent/background, quota/queue, browser/session/container/user events | P0/P1 |
| Terminal panel | `/api/terminals*`, tabs/view-state/processes, `/ws/terminal` | JSON + WebSocket | Unix baseline supported; Windows PTY and serialized xterm replay degraded | HTTP CRUD/tab/view-state shape; WS `subscribe`, `scrollback`, `bufferState`, `input`, `resize`, `output`, `requestResize`, `exit`, `error`; classify non-frontend events (`ready`, `snapshot`, `created`, `killed`, `renamed`) | P0/P1 |
| Search page | `/api/search?q=&entities=` | JSON | Partial: Go fallback/LIKE behavior can differ from TS FTS | Result entity shape, `entities` filtering, short-query behavior, snippet/relevance/degraded metadata, CJK/trigram fixtures | P1 |
| Settings, preferences, recent tabs | `/api/settings`, `/api/user-preferences`, recent-tabs routes, model/test/TLS/retry-rules | JSON + user WS events | Settings storage supported; validation not Zod parity; preferences/recent tabs need page matrix | Admin boundary, settings roundtrip, secret masking, provider prefix conflicts, user prefs shape, `user:recent_tabs_snapshot` user-scoped WS payload | P0/P1 |
| Skills / routines / MCP permissions UI | `/api/skills*`, `/api/routines*`, `/api/mcp/servers*`, `/api/mcp/tools`, `/api/kiro/mcp-tools` | JSON | Project content mostly supported; MCP stdio external tool injection is partial | Global skills/routines/global-prompt CRUD; MCP server list `{servers}` and tools arrays; stdio-only transport fallback; `capabilities.mcp.externalToolsInjection` should show stdio/per-call partial support while non-stdio transports remain disabled | P1 |
| Browser sessions UI | `/api/narrators/:id/browser-sessions*`, `browser_session_count` | JSON + `/ws/narrator` | Supported by Go Rod runtime with Go-owned optional storage | List is direct array with numeric ms fields; close/ttl/stop-tracing/screenshot manage live runtime with DB fallback; WS count initial/dynamic behavior; capability reports `go-rod`/narrator-bound/artifact persistence/resource limits | P0/P1 |
| Update UI | `/api/update/check`, `/download`, `/status`, `/apply`, `/cleanup`, `/version` | JSON + SSE download | Download SSE partial/full; self-apply disabled/manual-only | Check response extras tolerated; download SSE terminal result required on success/error; non-admin 403; apply returns stable disabled/manual-only payload | P0 |
| Provider settings | `/api/openai*`, `/api/anthropic*`, `/api/nkp*`, `/api/nug*`, `/api/kiro*`, `/api/codex*`, `/api/cline*` | JSON, plus Kiro chat SSE and Codex runtime transport toggles | Partial; Codex manager parity and several provider admin shapes remain risky | Empty-state and populated-state JSON shape, quota/usage numbers, OAuth redirects, credential masking, grouped models, `codex/status` pagination and `useWebSocket`; Kiro chat SSE `chunk/done/error` | P0/P1 |
| Storage / runtime / gateway admin | `/api/storage/*`, `/api/runtime/*`, `/api/gateway/*`, benchmarks | JSON + storage scan SSE | Partial/fallback; several disabled or manual-only paths | Admin boundary, scan SSE `progress/category/complete/error`, fallback codes for unsupported cleanup targets, gateway persistent runtime disabled state, benchmark execution disabled | P1/P2 |

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
  VNet, and unsupported runtime paths, verify `FEATURE_DISABLED`/fallback payloads and frontend-visible
  disabled state before treating the row as accepted. Rollback/edit-regenerate are now capability-gated as supported and require side-effect fixture parity instead of fallback acceptance. MCP stdio external tools are capability-gated as partial/per-call support; Browser sessions are capability-gated as supported Go Rod runtime with Chrome required.

#### P0 Diff Gates

- Health capability fields are explicit for every unsupported Go feature; absent fields must not make
  the frontend enable non-stdio MCP runtimes or update apply. Rollback/edit-regenerate should be enabled only when `capabilities.narrator.rollbackEditRegenerate.supported=true` and still needs TS-vs-Go side-effect fixture evidence. Browser sessions should be enabled only when `capabilities.narrator.browserSessions.supported=true` and shown with Chrome-required copy.
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
  - Gap: Go `/api/search` uses `LIKE` over base tables instead of TS FTS5 trigram tables and does not reproduce `MATCH`, `snippet(...)`, rank scoring, or TS query sanitization/fallback semantics.
  - Evidence: TS FTS setup in `server/db/fts.ts:47`, `server/db/fts.ts:52`, `server/db/fts.ts:57`; TS search in `server/services/search-service.ts:54`, `server/services/search-service.ts:84`, `server/services/search-service.ts:122`; Go search in `go_backend/internal/search/handlers.go:41`, `go_backend/internal/search/handlers.go:73`, `go_backend/internal/search/handlers.go:100`, `go_backend/internal/search/handlers.go:118`.
  - Impact: Search relevance, snippets, CJK/trigram matching, and performance can diverge significantly on real user data. This is a transparent-replacement gate, not a security-risk classification.
  - Acceptance: Go search either uses the existing TS-created FTS tables with compatible sanitized `MATCH`/snippet/rank semantics and LIKE fallback for <3 chars, or the replacement scope explicitly disables/degrades search with frontend-visible acceptance.

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
- `/api/search` parity is validated against fixtures covering:
  - query sanitization and short-query LIKE fallback;
  - trigram/CJK substring matches;
  - chapter title/description, narrator title, and message content results;
  - snippets and relative ranking/order close enough to TS behavior;
  - missing/corrupt FTS objects produce a controlled readiness/degraded-mode response.
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
- **Gap:** Go `Delete` is a direct `DELETE FROM projects` only
  (`go_backend/internal/projects/service.go:181-184`), so it does not mirror TS resource cleanup for
  worktrees, branches, terminals, containers, narrators/messages, merge sessions, exploration groups,
  recent tabs, or proxy cache. This is replacement-blocking if Go is used against real TS-created
  data.
- **Gap:** Go create does not mirror TS `.narrafork/project.db` initialization and `.gitignore`
  commit behavior on normal project creation (`server/routes/projects.ts:237-241` vs
  `go_backend/internal/projects/service.go:126-136`). Go does have explicit backup sync/import, but
  create-time backup DB lifecycle is incomplete.
- **Gap:** Go returns `chapterSettings` as raw string when present
  (`go_backend/internal/projects/service.go:492-495`), while TS stores/returns parsed JSON through
  Drizzle route responses and merges incoming settings in `server/routes/projects.ts:275-295`. This
  is partly API/schema-shaped, but it affects lifecycle settings such as `requireReviewBeforeMerge`.

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
- **Gap:** Go `Update` allows direct `status` and `role` mutation with no TS guard for root/trunk
  invariants. TS blocks root status/role changes and reserves trunk for root in
  `server/services/chapter-service.ts:278-319`; Go allows those fields through its generic allowed
  map in `go_backend/internal/chapters/service.go:483-505`.
- **Gap:** Go dormant/wake is just `Update(status)` (`go_backend/internal/chapters/handlers.go:52-53`,
  `541-550`). TS dormant/wake routes call dedicated service methods
  (`server/routes/chapters.ts:191-203`) that are expected to manage worktree lifecycle and events;
  therefore Go does not guarantee active/dormant worktree consistency.
- **Gap:** Go `Delete`/cleanup is closer than project delete but still partial: it kills terminal
  runtime records and removes worktree/branch, then deletes the chapter
  (`go_backend/internal/chapters/service.go:507-598`). TS delete also interrupts narrators, archives
  narrators, deletes terminal view state, containers/ports, merge sessions, removes recent tabs, and
  emits `chapter:abandoned` (`server/services/chapter-service.ts:390-468`). Go lacks several
  side-effect cleanups and broadcasts.

#### Fork, split, and cherry-pick lifecycle

- **Implemented in Go:** fork creates branch/worktree, records parent/fork fields, creates a fork edge,
  and creates a fresh narrator (`go_backend/internal/chapters/service.go:210-279`). Split and
  cherry-pick endpoints exist in `go_backend/internal/chapters/handlers.go:310-373`.
- **TS evidence:** TS fork resolves `startCommitSha` or `forkAtMessageUuid`, restores uncommitted file
  snapshots at a fork message, copies commit history, copies project-configured files, forks the
  primary narrator with inherit mode, optionally starts containers, runs startup script, records fork
  edge metadata, and emits events (`server/services/chapter-fork.ts:55-397`).
- **Gap:** Go ignores `inheritMode` and `forkAtMessageUuid` in actual fork behavior even though the
  handler accepts them (`go_backend/internal/chapters/handlers.go:75-84`, service input at
  `go_backend/internal/chapters/service.go:76-84`). Forks are effectively fresh narrator/worktree
  forks at branch/commit only. This breaks full/compressed/fresh semantics and fork-at-message
  uncommitted file state.
- **Gap:** Go split is a simplified fork from `commitSha`
  (`go_backend/internal/chapters/handlers.go:310-337`). TS split-at-commit semantics are broader:
  the original chapter is split into prefix/continuation with narrator message pruning per project
  design. Go does not implement that lifecycle.
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
- **Gap:** Go merge requires target clean but does not auto-commit source dirty state, does not verify
  stale review commit SHA, does not lock worktrees, does not remove source worktree after merge, does
  not emit merge events, and does not generate/inject merge summaries
  (`go_backend/internal/chapters/service.go:612-688`, `799-811`).
- **Gap:** Go AI resolve uses a one-shot `agentbasic.Generate` and direct file check
  (`go_backend/internal/chapters/service.go:690-797`), whereas TS uses the full narrator session loop
  with merge context and evented behavior (`server/services/chapter-merge.ts:390-538`). This affects
  permissions, tools, streaming, and auditability; ownership overlaps Narrator / Agent Parity.
- **Gap:** Go unmerge has the core reset/revert path and merge-edge cleanup, but lacks TS commit sync,
  merge-summary cleanup parity, event broadcasts, and branch/worktree path derivation details
  (`go_backend/internal/chapters/service.go:829-917` vs `server/services/chapter-merge.ts:690-830`).
- **Gap:** Go batch merge is synchronous and tries AI resolve immediately on conflict
  (`go_backend/internal/chapters/service.go:368-434`). TS batch merge persists a session, emits
  progress/conflict/AI events, waits for user continue/cancel decisions, rolls back newly-created
  target chapters, and cleans stale sessions (`server/services/chapter-batch-merge.ts:81-427`).
  Go therefore cannot safely support the interactive batch-merge UI flow.

#### Review lifecycle

- **Implemented in Go:** `/api/chapters/:id/review` creates a review chapter with review role,
  review edge, basic prompt, and optional user message (`go_backend/internal/chapters/service.go:281-366`);
  review conclusion/dismiss/convert/promote routes exist in
  `go_backend/internal/misccompat/handlers.go:1425-1566`.
- **TS evidence:** review creation copies dirty/untracked source files, builds a capped diff context,
  creates a fresh narrator with review-specific system prompt, sends the review start message, rolls
  back on failure, checks/resets review worktree mutations, concludes reviews, converts to subagent,
  promotes, dismisses, and emits review events (`server/services/review-service.ts:31-197`,
  `208-670`; `server/routes/reviews.ts:11-53`).
- **Gap:** Go review creation does not copy dirty source files, does not build the rich diff-context
  system prompt, does not auto-start the full review session, does not implement review worktree
  reset-on-mutation, and has no event bus parity. Basic review nodes exist, but review quality and
  lifecycle side effects are not TS-parity.

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
  containers flag, positions, exploration groups, and opened terminals in
  `go_backend/internal/graph/handlers.go:38-104`; exploration group CRUD/decide/abandon exists in
  `go_backend/internal/explorations/handlers.go:19-26`.
- **TS evidence:** TS graph builds nodes and edges from chapters plus narrator counts/statuses and
  supports graph position patch in `server/routes/graph.ts:52-135`, `285`; TS ruler positions accept
  width/height field aliases in `server/routes/ruler.ts:436-458`.
- **Gap:** Go graph hard-codes `hasUpstreamUpdates: false`
  (`go_backend/internal/graph/handlers.go:77-79`), while dependency status exists separately in Go;
  graph consumers will not see dependency update state. Go graph position patch expects
  `panelWidth`/`panelHeight` (`go_backend/internal/graph/handlers.go:107-147`), while TS ruler
  positions also accept `width`/`height` (`server/routes/ruler.ts:436-458`).

#### Chapter edges and dependency lifecycle

- **Implemented in Go:** edge list/create/delete supports project/chapter/type filters and direct
  dependency creation/deletion in `go_backend/internal/chapteredges/handlers.go:19-115`; dependency
  status and upstream sync exist in `go_backend/internal/chapters/handlers.go:375-458`.
- **TS evidence:** TS validates no self-edge, source/target existence, same project, and manual delete
  only for dependency edges in `server/services/chapter-edge-service.ts:12-54`, `123-181`.
- **Gap:** Go dependency create validates only `sourceId`, `targetId`, and `type == dependency`, then
  derives project from source (`go_backend/internal/chapteredges/handlers.go:73-104`). It does not
  reject self-dependencies or cross-project target chapters and may create invalid graph edges.
- **Gap:** Go dependency status computes commit counts but graph does not surface them; upstream sync
  updates commit metadata only partially (`go_backend/internal/chapters/handlers.go:375-458`).

#### Git panel lifecycle

- **Implemented in Go:** status/stage/unstage/commit/discard/diff/stash/log/reset/AI commit message
  are registered in `go_backend/internal/gitroutes/handlers.go:24-36`; implementation shells through
  `go_backend/internal/git/service.go` with output limits and timeouts.
- **TS evidence:** TS git routes cover the same route names in `server/routes/git.ts:42-196` and rely
  on richer git service behavior.
- **Gap:** Go git panel is functionally basic but degraded: `discardAll` uses `git restore .` and does
  not remove untracked files (`go_backend/internal/git/service.go:530-538`); `ai-commit-message` is a
  static placeholder (`go_backend/internal/gitroutes/handlers.go:244-255`); status line counts are
  shallow and do not compute line stats (`go_backend/internal/git/service.go:465-496`). The untracked-file
  discard behavior is P1 because users can believe all local changes were discarded while untracked files
  remain; the AI commit placeholder and line-stat polish stay P2.

### P0

- **Project deletion parity:** replace direct Go project delete with TS-equivalent full resource
  cleanup for chapters, worktrees/branches, terminals, containers/ports, narrators/messages,
  merge sessions, exploration groups, project tabs/proxy-cache effects, and fallback FK cleanup.
  Evidence: Go `projects.Service.Delete` at `go_backend/internal/projects/service.go:181-184`; TS
  cleanup at `server/routes/projects.ts:313-443`.
- **Fork-at-message / inherit-mode safety:** implement or explicitly disable TS fork modes in Go.
  Required before replacement because Go accepts `inheritMode`/`forkAtMessageUuid` but silently
  performs a fresh commit-only fork. Evidence: Go handler/service at
  `go_backend/internal/chapters/handlers.go:75-84`, `go_backend/internal/chapters/service.go:210-279`;
  TS fork at `server/services/chapter-fork.ts:55-397`.
- **Ruler lifecycle semantic parity or explicit degraded mode:** Go has misccompat ruler compatibility
  routes, but they must either reach TS-equivalent lifecycle semantics or clearly advertise degraded
  behavior to the frontend. Evidence: TS `server/routes/ruler.ts:81-733`; Go route registration at
  `go_backend/internal/app/router.go:98-103`, route compatibility handlers at
  `go_backend/internal/misccompat/handlers.go:2046-2054`, and related implementation code at
  `go_backend/internal/misccompat/handlers.go:1652-1891`, `2058-2329`.
- **Batch merge interactive lifecycle:** implement TS merge session/event/decision/rollback behavior
  or disable batch merge under Go. Evidence: Go synchronous batch merge at
  `go_backend/internal/chapters/service.go:368-434`; TS orchestrator at
  `server/services/chapter-batch-merge.ts:81-427`.
- **Chapter update/dormant/wake invariants:** enforce root/trunk/status invariants and implement
  worktree-aware dormant/wake semantics instead of raw status updates. This is P0 because invalid root/
  trunk mutation and DB/worktree state divergence can corrupt the story graph and active/dormant chapter
  lifecycle. Evidence: `go_backend/internal/chapters/service.go:483-505`,
  `go_backend/internal/chapters/handlers.go:541-550`; TS invariants in
  `server/services/chapter-service.ts:278-319`.
- **Merge lifecycle data-integrity protections:** add source dirty auto-commit behavior, required-review
  stale/clean commit validation, target/source worktree locking, source worktree cleanup after merge,
  commit sync, event broadcasts, and merge-summary injection. This is P0 because merging without these
  protections can lose uncommitted source state, accept stale review approvals, or leave DB/git/worktree
  state inconsistent. Evidence: Go `go_backend/internal/chapters/service.go:612-811`; TS
  `server/services/chapter-merge.ts:72-185`, `232-659`.

### P1

- **Review lifecycle parity:** copy dirty source state, build rich diff context, auto-start review
  session with full narrator loop, reset review worktree mutations, and emit review events. Evidence:
  Go `go_backend/internal/chapters/service.go:281-366` and
  `go_backend/internal/misccompat/handlers.go:1425-1566`; TS
  `server/services/review-service.ts:31-670`.
- **P1-high / release blocker — Dependency edge validation:** reject self and cross-project dependency
  edges and preserve edge metadata semantics. This is not classified as P0 because the invalid edge is an
  explicit DB graph-row problem rather than direct file/worktree data loss, but it is a release blocker:
  cross-project or self dependency edges can poison graph/dependency status and make later lifecycle
  operations unsafe. Evidence: Go `go_backend/internal/chapteredges/handlers.go:73-104`; TS
  `server/services/chapter-edge-service.ts:12-54`.
- **Unmerge parity:** sync target/source commits, clean merge summary messages, and emit reload/wake
  events as TS does. Evidence: Go `go_backend/internal/chapters/service.go:829-917`; TS
  `server/services/chapter-merge.ts:690-830`.
- **Git panel discard-all safety:** clean untracked files on discard-all when TS does, or rename/disable
  the action so users do not believe all local changes were removed while untracked files remain.
  Evidence: Go `go_backend/internal/git/service.go:530-538`.

### P2

- **Project create backup lifecycle:** initialize `.narrafork/project.db`, ensure `.gitignore`, and
  commit ignore entry during Go project creation. Evidence: TS `server/routes/projects.ts:237-241`;
  Go explicit backup sync/import at `go_backend/internal/projects/service.go:219-341` but no
  create-time initialization.
- **Split/cherry-pick enhancements:** implement true split-at-commit prefix/continuation semantics and
  `cherry_pick` edge/history recording. Evidence: Go simplified split/cherry-pick in
  `go_backend/internal/chapters/handlers.go:310-372`; TS fork/split design and edge model.
- **Graph polish:** surface upstream dependency updates in graph data and accept TS ruler position
  aliases (`width`/`height`) where relevant. Evidence: Go graph
  `go_backend/internal/graph/handlers.go:77-79`, `107-147`; TS ruler positions
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
  edges enforce TS validation; git panel operations match route semantics and safety expectations,
  with exact response-shape/API details coordinated with API Contract Parity and Frontend Contract /
  Smoke Tests.

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
- **Message storage parity is partial.** Go uses `narrator_messages` + `narrator_message_refs`, supports seq cursor pagination, top-level filtering, child messages via `parent_tool_use_id`, and tree building (`go_backend/internal/narrators/service.go:451-508`, `624-668`, `671-752`). Go also copies refs during fork (`service.go:196-256`, `363-378`). TS has the same core junction-table pattern and message-version bumping (`server/services/narrator-persistence.ts:51-129`, `157-199`) plus richer catch-up cursor/child-anchor support and sidecar-aware message loading (`server/services/narrator-messages.ts:118-222`). Go does not fully reproduce TS catch-up cursors, `narrator_sidecars`, creator enrichment for all paths, or exact block-preserving agent history.
- **Agent history is lossy in Go.** Go builds provider history from `content_text` only and skips non-user/assistant roles; tool-use/tool-result blocks in stored `content_json` are not reconstructed into provider-native history (`go_backend/internal/narrators/service.go:1105-1162`). TS persists full SDK content blocks and reuses them for history/tool-call continuity (`server/services/narrator-persistence.ts:172-183`; `server/services/subagent-executor.ts:229-240`). This can break multi-turn tool reasoning or provider transcript parity after restart/fork/compact.
- **Tool call persistence is partial.** Go records running/pending/completed `narrator_tool_calls`, input/output JSON, file snapshots, and links results to the assistant draft (`go_backend/internal/narrators/agent_event_handler.go:227-274`; permission records in `service.go:928-966`). It lacks TS-level metadata around hook outcomes, progress/long-running output streaming, redirected inputs, malformed-input protection, usage/cost linkage, and full permission suggestion lifecycle. TS executor explicitly performs permission handling, `PreToolUse` hooks, malformed/truncated JSON guards, progress/long-running events, throttled output streaming, and redirected-input tracking (`server/lib/agent/tool-executor.ts:145-308`).
- **Permission behavior is functional but not equivalent.** Go supports pending tool calls, REST/WS decisions, answers merged into `input_json`, a 5-minute wait, danger reflection, and status broadcasts (`go_backend/internal/narrators/service.go:914-966`; `go_backend/internal/narrators/handlers.go:1504-1533`; `go_backend/internal/ws/hub.go:216-312`). It does not match TS plan-mode permission redirection, approval reprocessing, reflection loop control, nuanced permission modes/whitelist/blacklist interaction, or permission hooks imported by TS session routing (`server/routes/narrators.ts:137-177`).
- **Plan mode is only shallow parity.** Go creates `plan_file_id` when `startInPlanMode` is used and appends a plan-mode system prompt (`go_backend/internal/narrators/service.go:159-168`, `1038-1047`), and route/tool toggles set `plan_mode` (`handlers.go:1709-1721`; `service.go:1425-1438`). TS plan mode also manages traits, previous permission mode, generated plan file IDs, relaxed-plan auto-enable for active goals, and plan-mode exit cleanup (`server/services/narrator-plan-mode.ts:28-147`). Go therefore lacks the core safety contract where write tools are redirected/controlled by the plan file and plan reflection approval.
- **Subagent parity is now targeted but still not full TS-native runtime parity.** Go implements `Agent`, `Send`, `Await`, `TeamStatus`, foreground/background child message trees, detach-to-background parent unblocking, attach/await parent-blocking semantics, background result injection into parent tool calls, and stale running recovery. TS still has richer abort-controller swapping, team inbox/file-change state, custom subagent prompts, conclusion-file permission exceptions, recovery/compact retry, and more detailed broadcasts (`server/services/subagent-runner.ts:27-54`, `61-169`, `177-220`; `server/services/subagent-executor.ts:33-46`, `85-131`, `182-240`).
- **Hooks are missing in Go.** TS tool execution runs `PreToolUse` hooks fail-open and can block tool execution on explicit hook result (`server/lib/agent/tool-executor.ts:181-204`). No equivalent hook handler was found in `go_backend/internal/agentbasic/*` or `go_backend/internal/narrators/*`; Go directly executes `agentbasic.ExecuteTool` after its simplified permission path (`go_backend/internal/narrators/service.go:960-976`).
- **Sidecars are missing in Go.** TS has `sidecar.ts` helpers for user-message/tool-result sidecars and API formatting (`server/lib/agent/sidecar.ts:1-60`), and session state includes one-shot goal sidecars (`server/services/narrator-session-state.ts:101`). No Go equivalent sidecar persistence/formatting was found; Go agent history and message sending are plain text/content blocks.
- **Compact exists but is not TS-equivalent.** Go can create compact/segment compact marker messages, set `is_compact`, update `prune_boundary_message_id`, and inject the most recent compact summary into future history (`go_backend/internal/narrators/handlers.go:867-899`, `1605-1633`; `go_backend/internal/narrators/service.go:1112-1143`, `1165-1183`). TS has locks, background/blocking compact modes, context-overflow recovery, prune boundary handling, custom compact, and segment operations wired through session services (`server/services/narrator-session-state.ts:286-301`; `server/services/narrator-recovery.ts:74-115`; `server/routes/narrators.ts:155-168`). Go compact is acceptable as a compatibility marker only, not a replacement for TS recovery.
- **Buffers and goals are API-present but behavior differs.** Go exposes buffer CRUD and goals CRUD (`go_backend/internal/narrators/handlers.go:2203-2345`, `2348+`). TS buffers are runtime-aware, cap queues, persist text files under `~/.narrafork/buffered-files`, support priority front insertion, and coordinate soft-stops (`server/services/narrator-buffer.ts:17-23`, `60-183`). TS goals auto-activate next pending goals, broadcast `goals_set`, attach creator data, track token/time usage, and drive relaxed plan/continuation (`server/services/narrator-goal-service.ts:102-153`, `156-188`, `190-240`). Go goals are mostly CRUD and do not fully implement autonomous continuation/usage accounting.
- **Usage/cost parity is incomplete.** Go provider events define usage snapshots, metering, API request events, and broadcasts (`go_backend/internal/agentbasic/provider_types.go:56-85`, `107-173`; `go_backend/internal/narrators/agent_event_handler.go:117-180`), and route `/usage-stats` loads DB stats (`go_backend/internal/narrators/handlers.go:1970-1980`). No verified Go path updates `total_cost_usd`, persisted usage history, per-goal token/time counters, or full provider credential/cost accounting comparable to TS usage/cost services imported by TS routes (`server/routes/narrators.ts:185`).
- **Provider runtime gap remains P0/P1 depending on configured models.** Go has provider adapters and routing for Anthropic, OpenAI, Kiro, Codex, NKP/NUG/Cline-style providers and fallback logic (`go_backend/internal/agentbasic/provider_resolver.go:20-61`, `63-220`), but TS provider runtime has richer provider-specific features in `server/lib/agent/**` including sidecars, request dumps, recovery, pipeline state, image generation, and provider-specific event semantics. Go's own resolver still treats unsupported provider prefixes via fallback/error (`provider_resolver.go:50-60`), so replacement safety depends on explicit supported-provider smoke tests.
- **Rollback/edit-regenerate are now active Go routes, not disabled fallbacks.** Go now truncates `narrator_message_refs`/message blocks, marks affected tool calls stale, optionally rebuilds file state from snapshots, preserves shared-message safety with copy-on-write user edits, broadcasts message updates/deletions, and can optionally rerun the agent after an edit (`go_backend/internal/narrators/rollback_edit.go`; focused coverage in `go_backend/internal/narrators/rollback_edit_test.go`). Remaining work is broader TS-vs-Go fixture comparison across provider/runtime edge cases, not a `FEATURE_DISABLED` fallback.

### P0

- **P0-A: Preserve full content-block/tool transcript history.** Before replacement, Go must reconstruct provider history from stored `content_json` including `tool_use`, `tool_result`, reasoning/redacted blocks where applicable, trailing tool results, and subagent child-message context; `content_text`-only history (`go_backend/internal/narrators/service.go:1105-1162`) is unsafe for ongoing sessions with tools.
- **P0-B: Implement TS-equivalent permission safety for writes/exec.** Go must match TS permission modes, whitelist/blacklist behavior, approval reprocessing, plan-mode redirection, malformed-input guards, and denial/error persistence before it can safely execute destructive tools. Current Go path executes tools directly after simplified approval (`service.go:914-976`) and lacks TS hook/guard coverage (`server/lib/agent/tool-executor.ts:145-308`).
- **P0-C: Plan mode safety contract.** Go must preserve `previousPermissionMode`, `planFileId`, plan traits, relaxed-plan behavior, exit cleanup, and write redirection/approval semantics from TS (`server/services/narrator-plan-mode.ts:28-147`). Simply setting `plan_mode` is not enough (`go_backend/internal/narrators/handlers.go:1709-1721`).
- **P0-D: Provider/runtime allowlist or parity.** If Go is used as replacement, it must either fully support the configured provider/runtime semantics or reject unsupported prefixes before run start with clear UI fallback. Provider fallback alone (`go_backend/internal/agentbasic/provider_resolver.go:50-60`) is not a safe replacement guarantee.

### P1

- **P1-A: Subagent execution parity.** Go now covers detach/attach parent-blocking, background task persistence/notifications, result injection, Send/Await continuation, and stale recovery. Remaining TS parity gaps are abort-controller source swapping, custom subagent prompts, conclusion-file permission exception, richer subagent buffers, team inbox/file-change semantics, and compact/recovery polish (`server/services/subagent-runner.ts:27-54`, `61-169`; `server/services/subagent-executor.ts:85-131`, `182-240`).
- **P1-B: Hooks and sidecars.** Implement `PreToolUse` hook execution/blocking semantics and sidecar formatting/persistence for user messages and tool results (`server/lib/agent/tool-executor.ts:181-204`; `server/lib/agent/sidecar.ts:1-60`).
- **P1-C: Compact/recovery parity.** Add compact locks, background vs blocking compact behavior, context-overflow recovery, retry-after-compact, and exact prune-boundary semantics; current Go compact is a marker/summary implementation (`go_backend/internal/narrators/handlers.go:1605-1633`).
- **P1-D: Buffer runtime semantics.** Match TS active-run buffer behavior, priority/soft-stop behavior, persisted text-file attachments, and max-queue handling (`server/services/narrator-buffer.ts:17-183`).
- **P1-E: Usage/cost accounting.** Persist provider usage, credential metadata, total cost, per-run usage stats, and goal token/time counters; current Go mainly broadcasts usage-shaped events and reads existing stats.
- **P1-F: Message catch-up and WS event parity.** Implement TS catch-up cursors/child anchors and ensure Go WS events match frontend expectations for `subagent_started`, `subagent_status_changed`, `permission_resolved`, `goals_set`, compact progress, tool progress, and streamed tool output.
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
- Go WS permission and broadcast hub: `go_backend/internal/ws/hub.go:120-136`, `216-312`.
- Go provider/runtime types and resolver: `go_backend/internal/agentbasic/provider_types.go:56-85`, `107-173`; `go_backend/internal/agentbasic/provider_resolver.go:20-61`, `63-220`.

### Acceptance Criteria

- **Replacement candidate:** all P0 items pass automated or scripted smoke tests using an existing TS-created narrator with tool calls, compact marker, plan mode, pending permission, buffered message, goal, and a subagent tree; no data loss and no unsafe tool execution.
- **Compatibility beta:** if P0-D provider parity is not complete, Go must expose an explicit supported-provider allowlist and fail unsupported narrator models before starting a run, with frontend-visible fallback messaging.
- **Message acceptance:** `GET /api/narrators/:id/messages` and agent replay preserve `content_json` block semantics, nested subagent messages, tool-call IDs, message refs/seq, compact boundaries, and `messageVersion` increments across delete/retry/fork.
- **Permission acceptance:** write/exec tools in default, read-only, plan, and deny modes produce the same pending/approved/denied/cancelled DB rows and WS events as TS; denied or malformed inputs cannot mutate files.
- **Subagent acceptance:** foreground, background, detached, interrupted, messaged, awaited, team-status, file-change, and conclusion-file flows match TS-visible statuses/events and persist background task results.
- **Usage acceptance:** streamed provider usage updates persist to narrator total cost/usage history and per-goal token/time counters, and `/usage-stats` reflects the latest run without requiring TS backend.

### Notes

- Current Go implementation is a useful compatibility backend for simple chat/tool sessions but remains **unsafe as a transparent replacement** for TS in projects relying on plan-mode safety, rich tool transcripts, subagents, hooks, sidecars, or provider-specific runtime behavior.
- Suggested execution order: P0-A message transcript fidelity → P0-B/P0-C permission + plan safety → P0-D provider allowlist/runtime smoke → P1 subagents/hooks/compact/usage → P2 UI polish.

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

- **WebSocket route coverage is partial.** Go exposes `/ws/narrator` and `/ws/terminal`; TS also
  upgrades `/ws/vnet` and has heartbeat/stale-connection handling for narrator, terminal, and VNet
  channels. Go terminal WS supports subscribe/input/resize/kill/create/rename, but it lacks TS
  terminal scrollback and buffer-state replay and does not gate terminal input/resize to subscribed
  active clients. Evidence: `server/websocket/ws-handler.ts:37`, `server/websocket/ws-handler.ts:57`,
  `server/websocket/ws-handler.ts:81`, `server/websocket/terminal-ws.ts:78`,
  `server/websocket/terminal-ws.ts:106`, `server/websocket/terminal-ws.ts:159`,
  `go_backend/internal/app/router.go:142`, `go_backend/internal/app/router.go:143`,
  `go_backend/internal/ws/hub.go:173`, `go_backend/internal/ws/hub.go:195`,
  `go_backend/internal/ws/hub.go:220`.
- **Terminal runtime is functional but not TS-equivalent.** TS uses dtach when available, persistent
  `BufferManager` scrollback, platform-specific Bun/portable PTY runtimes, event-bus lifecycle
  events, reattach after detach/restart, and graph-state/admin helpers. Go uses `creack/pty` with a
  simple in-memory session map, emits output/exit/requestResize, and updates DB status, but does not
  persist replay buffers, support dtach detach/reattach, or emit TS lifecycle events. Evidence:
  `server/services/terminal-service.ts:171`, `server/services/terminal-service.ts:272`,
  `server/services/terminal-service.ts:277`, `server/services/terminal-service.ts:301`,
  `server/services/terminal-service.ts:337`, `server/services/terminal-service.ts:504`,
  `go_backend/internal/terminalruntime/manager.go:38`,
  `go_backend/internal/terminalruntime/manager.go:76`,
  `go_backend/internal/terminalruntime/manager.go:147`,
  `go_backend/internal/terminalruntime/manager.go:163`.
- **Container routes exist, but orchestration/proxy/port parity is incomplete.** TS configures
  rootless Podman environment/cgroup fallback, allocates ports, derives proxy labels/URLs, runs an
  in-process container proxy cache, and emits lifecycle events. Go probes Podman/compose and can run
  compose actions/logs, but live instance normalization sets `proxyLabel`, `proxyUrl`, and
  `containerIp` to nil and does not implement TS proxy routing or allocator semantics. Evidence:
  `server/services/container-service.ts:45`, `server/services/container-service.ts:70`,
  `server/services/container-service.ts:15`, `server/services/container-proxy.ts:38`,
  `go_backend/internal/chapters/handlers.go:696`,
  `go_backend/internal/chapters/handlers.go:786`,
  `go_backend/internal/chapters/handlers.go:895`,
  `go_backend/internal/chapters/handlers.go:1079`,
  `go_backend/internal/chapters/handlers.go:1106`.
- **Uploads/shares/storage/fs are compatibility implementations with different guarantees.** TS
  shares are ephemeral in memory and cleaned on restart; Go copies files to `~/.narrafork/shares`,
  falls back to directory scan for unknown records, and keeps POST share creation on both public and
  authenticated mounts. Go storage scan/cleanup is present and conservative for worktrees/containers;
  TS has admin-gated storage cleanup/scan services. Evidence: `server/lib/shares.ts:18`,
  `server/lib/shares.ts:106`, `server/routes/storage.ts:13`, `server/routes/storage.ts:91`,
  `go_backend/internal/misccompat/handlers.go:108`,
  `go_backend/internal/misccompat/handlers.go:513`,
  `go_backend/internal/misccompat/handlers.go:582`,
  `go_backend/internal/misccompat/handlers.go:614`,
  `go_backend/internal/misccompat/handlers.go:658`.
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
- **MCP, gateway, VNet, benchmarks, and volume snapshots are not full TS runtimes.** TS MCP uses
  `mcpManager` to connect/disconnect/test external servers and list live external tools; Go keeps an
  in-memory MCP state and embedded tools. Go gateway/VNet status endpoints explicitly report
  fallback/unsupported, and Go volume snapshots only insert metadata/application rows instead of
  running Podman tar/cp archive flows. Evidence: `server/routes/mcp.ts:338`,
  `server/routes/mcp.ts:407`, `server/routes/mcp.ts:442`, `server/routes/mcp.ts:560`,
  `server/routes/vnet.ts:7`, `server/routes/gateway.ts:30`,
  `server/services/volume-snapshot-service.ts:106`,
  `server/services/volume-snapshot-service.ts:225`,
  `go_backend/internal/mcpcompat/handlers.go:26`, `go_backend/internal/mcpcompat/handlers.go:64`,
  `go_backend/internal/mcpcompat/handlers.go:384`,
  `go_backend/internal/systemroutes/handlers.go:514`,
  `go_backend/internal/systemroutes/handlers.go:538`,
  `go_backend/internal/misccompat/handlers.go:2437`,
  `go_backend/internal/misccompat/handlers.go:2477`.

### P0

- **P0 — Terminal/WebSocket replacement parity is incomplete.** A Go backend replacement would break
  or degrade terminal reconnect/replay and VNet WebSocket flows because Go lacks `/ws/vnet`, TS
  terminal scrollback/bufferState replay, dtach reattach semantics, and subscribed-client guards for
  terminal input/resize. Acceptance: implement or explicitly disable unsupported channels/features
  with frontend-visible fallbacks; terminal WS smoke tests must cover create, subscribe, input,
  resize, reconnect scrollback, bufferState, kill, rename, unauthorized access, and VNet route
  behavior. Evidence: paths listed in the first two Findings.
- **P0 — Container/proxy/port runtime side effects are not TS-equivalent.** Go can invoke compose,
  but does not reproduce TS rootless setup, proxy URL routing, port allocator state, container IP
  reconciliation, and lifecycle/event semantics. Acceptance: either implement the TS
  container-service/container-proxy/port-allocator contract, or mark container runtime/proxy features
  unsupported in Go and keep UI/API from implying parity. Evidence: container Finding paths above.
- **P0 — Volume snapshots are metadata-only and request fields mismatch.** TS creates and applies real
  archives via `podman exec`/`podman cp`; Go only inserts `volume_snapshots` and
  `volume_snapshot_applications` rows. Frontend/TS create requests use `chapterId`, while Go reads
  `sourceChapterId`, so even metadata-only snapshot creation can miss the intended source. Acceptance:
  real create/apply/delete flows must exercise Podman archive copy and event/error semantics, accept
  TS/frontend request fields, or snapshot routes must return explicit unsupported payloads instead of
  apparent success. Evidence: `server/services/volume-snapshot-service.ts:106`,
  `server/services/volume-snapshot-service.ts:225`, `frontend/lib/api/projects.ts:238-251`,
  `frontend/hooks/useVolumeSnapshots.ts:20-38`, `server/routes/volume-snapshots.ts:23-37`,
  `server/lib/validators/containers.ts:9-20`, `go_backend/internal/misccompat/handlers.go:2437`,
  `go_backend/internal/misccompat/handlers.go:2453`,
  `go_backend/internal/misccompat/handlers.go:2477`.
- **P0 — Share lifecycle persistence/expiry is a security and privacy blocker.** TS share records are
  process-local and stale share directories are cleaned at startup; Go copies shares to
  `~/.narrafork/shares`, can serve unknown records by directory fallback after a Go restart, and lacks
  equivalent expiry validation on fallback serving. Acceptance: either match TS ephemeral cleanup and
  expiry checks, or explicitly redesign shares as persistent with authenticated/admin controls, expiry
  enforcement, and user-visible privacy semantics before replacement. Evidence: `server/lib/shares.ts:18`,
  `server/lib/shares.ts:106`, `go_backend/internal/misccompat/handlers.go:557`,
  `go_backend/internal/misccompat/handlers.go:582`.

### P1

- **P1 — Gateway fallback is not parity.** Go returns `supported:false` or fallback payloads for
  gateway while TS has runtime hubs, QR/login platform handling, and session routing. VNet is not
  duplicated here at lower priority because missing `/ws/vnet` remains part of the P0 Terminal/WebSocket
  blocker unless the feature is explicitly disabled at the entry points. Acceptance: frontend smoke tests
  must assert fallback copy/disabled UI for gateway, or Go must bundle an equivalent gateway runtime.
  Evidence: `server/routes/gateway.ts:30`, `go_backend/internal/systemroutes/handlers.go:514`.
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

- **P2 — Notification sounds/hooks/provider-model cache coverage needs endpoint-level checks.** Go
  includes compatible routes, but auth boundary, table availability, and provider model cache details
  overlap API/DB/narrator tracks. Acceptance: route tests should verify auth, persistence, masking,
  and empty-state fallbacks. Evidence: `go_backend/internal/misccompat/handlers.go:75`,
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
  expects TS-compatible narrator WS event names, control messages, and message identifiers, while
  Go currently emits a reduced/different surface and serializes message UUIDs with
  `sdkMessageUuid` where frontend code consumes `messageUuid`. Evidence:
  `frontend/lib/narrator-ws-manager.ts`, `frontend/hooks/useNarratorWS.ts`,
  `frontend/lib/api/types.ts:391-399`, `frontend/lib/api/narrators.ts`,
  `server/routes/narrators.ts`, `server/websocket/*`, `server/db/schema.ts:342`,
  `go_backend/internal/narrators/service.go:89-104`,
  `go_backend/internal/narrators/service.go:636-668`, `go_backend/internal/ws/hub.go`.
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
- **P0 — Narrator message field mismatch: Go returns `sdkMessageUuid`, frontend uses
  `messageUuid`.** This breaks message reconciliation, streaming updates, and tool-call association
  unless Go aliases the field or frontend accepts both. Evidence: `frontend/lib/api/types.ts:391-399`,
  `frontend/lib/api/narrators.ts`, `frontend/components/narrator/*`, `server/routes/narrators.ts`,
  `server/db/schema.ts:342`, `go_backend/internal/narrators/service.go:89-104`,
  `go_backend/internal/narrators/service.go:636-668`.
- **P0 — Public/auth boundary mismatch for notification-sound mutations.** Frontend/API security
  expectations follow the TS app where notification-sound mutation routes are authenticated; Go exposes
  broader public handlers. Replacement must align auth boundaries or the UI and API tests must treat it
  as an explicit security exception. Evidence: `server/app.ts`, `server/routes/notification-sounds.ts`,
  `go_backend/internal/app/router.go`, `go_backend/internal/misccompat/handlers.go`.
- **P0 — Settings raw-secret exposure/shape must match frontend expectations.** The settings UI and
  provider forms use `frontend/lib/api/settings.ts`; Go must not expose raw secrets where TS masks or
  omits them, and must still provide enough masked/provider metadata for editing flows. Evidence:
  `frontend/lib/api/settings.ts`, `frontend/components/settings/*`,
  `server/routes/settings.ts:606-660`, `server/routes/settings.ts:965-1011`,
  `go_backend/internal/settings/handlers.go:31-48`,
  `go_backend/internal/config/config.go:108-139`.
- **P0 — Batch merge lacks TS async merge-session semantics and response field.** The current frontend
  may not read `mergeSessionId` directly in every path, but interactive/reconnect/conflict flows require
  session semantics for progress, decisions, and recovery; Go's synchronous response shape is not enough
  for replacement. Evidence: `frontend/lib/api/chapters.ts`, `frontend/components/chapter/*`,
  `server/routes/chapters.ts`, `server/services/chapter-batch-merge.ts:81-427`,
  `go_backend/internal/chapters/handlers.go`.
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
- **P0 — Volume snapshot workflow is frontend-breaking and runtime-incomplete.** Frontend sends
  `chapterId`, while Go reads `sourceChapterId`, and Runtime P0 records that Go only writes snapshot
  metadata instead of real Podman archive flows. Replacement must support both the TS/frontend request
  field and real create/apply/delete side effects, or return explicit unsupported states that prevent the
  UI from implying snapshot success. Evidence: `frontend/lib/api/projects.ts:238-251`,
  `frontend/hooks/useVolumeSnapshots.ts:20-38`, `server/routes/volume-snapshots.ts:23-37`,
  `server/lib/validators/containers.ts:9-20`, `go_backend/internal/misccompat/handlers.go:2437-2453`.

### P1

- **P1 — FS browse response is missing navigation metadata.** File browser UI expects separator,
  parent path, and drive/root metadata where applicable; Go fallback responses without `sep`, `parent`,
  or `drives` degrade navigation, especially on Windows/WSL paths. Evidence: `frontend/lib/api/misc.ts`,
  `server/routes/fs.ts`, `go_backend/internal/systemroutes/handlers.go`.
- **P1 — Routines project fields are incomplete.** The routines UI expects project-scoped override and
  global-enabled semantics; Go must preserve `override`/`globalEnabled`-style fields or document the UI
  fallback. Evidence: `frontend/lib/api/misc.ts`, `frontend/routes/routines*`,
  `server/routes/routines.ts`, `go_backend/internal/misccompat/handlers.go`.
- **P1 — Provider/model endpoints need endpoint-level contract checks.** Cline/NUG/Kiro/OpenAI/Codex/
  Anthropic/NKP model, auth, usage, balance, and refresh endpoints are route-covered but not yet proven
  response-compatible with the frontend provider settings pages. Evidence: `frontend/lib/api/settings.ts`,
  `frontend/lib/api/misc.ts`, `server/routes/cline.ts`, `server/routes/nug.ts`,
  `server/routes/kiro.ts`, `server/routes/openai.ts`, `server/routes/codex.ts`,
  `server/routes/anthropic.ts`, `server/routes/nkp.ts`, `go_backend/internal/systemroutes/handlers.go`.
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
  fallbacks rather than implying TS-equivalent execution. VNet remains P0 while `/ws/vnet` is missing or
  not explicitly disabled. Evidence: `frontend/lib/api/misc.ts`,
  `frontend/lib/api/streams.ts`, `server/routes/storage.ts`, `server/routes/runtime.ts`,
  `server/routes/gateway.ts`, `server/routes/vnet.ts`, `server/routes/benchmarks.ts`,
  `go_backend/internal/systemroutes/handlers.go`.
- **P2 — Edge features can be documented as degraded.** Favorites, learning, uploads, hooks, and
  project-db status helpers should keep basic UI paths stable, but exact side effects can remain lower
  priority if replacement notes call them out. Share lifecycle is not included here because persistent
  directory fallback and missing expiry enforcement are P0 security/privacy concerns. Evidence:
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

- **API/security/setup:** fix notification-sound mutation auth boundary and implement the TS `GIT_NOT_INSTALLED` guard for Git-dependent routes. Evidence: API Contract P0; Frontend P0 repeats the auth risk.
- **DB/search/bootstrap:** Go search must use TS-compatible FTS/trigram/snippet/rank semantics or be explicitly degraded; Go also needs a schema/FTS readiness or migration/bootstrap policy before standalone replacement. Evidence: DB / Schema / FTS P0.
- **Domain lifecycle/data integrity:** project deletion, fork-at-message/inherit modes, chapter root/trunk/status invariants, dormant/wake DB/worktree consistency, merge dirty-source/review/worktree-lock protections, partial/degraded ruler lifecycle semantics, and interactive batch merge are not safe replacements. Evidence: Domain Lifecycle P0.
- **Narrator safety/runtime:** full content-block/tool transcript replay, TS-equivalent permission/write safety, plan-mode safety, and provider/runtime allowlist or parity remain blockers. Evidence: Narrator / Agent P0.
- **Runtime side effects/security:** terminal/WS replay and VNet behavior, container/proxy/port side effects, real volume snapshot archive flows plus snapshot field compatibility, and share persistence/expiry semantics are incomplete or unsafe if treated as success. Share fallback serving after Go restart without TS-equivalent expiry checks is a security/privacy blocker. Evidence: Runtime / System Integration P0.
- **Frontend-visible contracts:** narrator WS/message identity, settings secret masking/shape, batch merge async session semantics/response shape, ruler partial/degraded lifecycle compatibility, terminal/container degraded states, and volume snapshot field/runtime behavior must be fixed or adapted before replacement. Evidence: Frontend Contract P0.

### Cross-track P1 Rollup

P1 classification:

- **P1-critical:** not a P0 data-loss/security blocker by itself, but required for `replacement candidate` because it affects common workflows, release safety, or TS-compatible observability.
- **P1-deferred:** acceptable for `compatibility beta` if documented with stable fallback/disabled UI and smoke evidence, but still required before claiming broad feature parity.

- **P1-critical — Route/provider contract detail:** Cline/NUG/provider/model endpoint shapes, chapter/narrator response/stream semantics, and provider settings pages need endpoint-level smoke tests. VNet is tracked as P0 until `/ws/vnet` exists or the entry points are disabled. Evidence: API and Frontend P1.
- **P1-critical — Schema capability and FTS lifecycle:** startup/readiness capability reporting for tables/columns/indexes/FTS and FTS verifier/rebuilder or clear readiness failure are required for replacement-candidate safety. Optional-table ownership and backup scope can be P1-deferred if documented. Evidence: DB P1.
- **P1-critical — Lifecycle correctness:** review lifecycle, unmerge side effects, dependency edge validation, and git discard-all safety are replacement-candidate required because they affect common code-review/graph/git correctness. Evidence: Domain P1.
- **P1-critical — Narrator advanced observability:** subagent execution, hooks/sidecars, compact/recovery, catch-up cursors, usage/cost persistence, and WS event parity are replacement-candidate required for existing TS sessions that use agent tooling. Evidence: Narrator P1.
- **P1-deferred — Runtime fallback quality:** gateway, MCP, update/runtime/storage/admin, and benchmark workflows may remain disabled/degraded for compatibility beta if the fallback contract is met; full runtime parity is required for broad replacement. Evidence: Runtime P1.
- **P1-deferred — Frontend fallback quality:** FS navigation metadata, routines project/global fields, MCP/custom-subagent empty states, update disabled UI, and provider pages need stable contract tests; they may be deferred only with visible unsupported states and no crashes. Snapshot field aliases remain part of the P0 snapshot blocker. Evidence: Frontend P1.

### Cross-track P2 Rollup

- **Documentation/extensions:** document Go-only routes, health/version metadata, optional degraded edge features, and project-db/status helpers as compatibility extensions where accepted. Persistent share semantics are not P2 documentation-only unless expiry/security behavior is fixed or explicitly redesigned. Evidence: API/Runtime/Frontend P2.
- **Polish and validation:** centralize enum constants/validators, verify index assumptions, improve graph/git-panel fidelity beyond the P1 discard-all safety issue, title/retry/fork compressed-context polish, usage display, admin 403 UX, and optional tool-call/detail fields. Evidence: DB/Domain/Narrator/Frontend P2.
- **Safety maintenance:** keep output limits, timeouts, explicit admin intent, and non-destructive cleanup constraints when adding parity for storage/runtime/benchmark/container flows. Evidence: Runtime P2.

### Recommended Execution Order

| Phase | Work items | Required tests | Exit condition | Artifacts |
|---|---|---|---|---|
| 1. Safety gates | Notification-sound auth, Git-missing guard, settings secret masking, share expiry/fallback serving, narrator permission/plan-mode safety, provider allowlist | AUTH-NS-01, API-GIT-01, SETTINGS-SECRET-01, RUNTIME-SHARE-01, NARRATOR-PERM-01 | No auth bypass, raw secret, unexpected mutation, or unsafe provider/tool start | HTTP transcripts, secret-scan evidence, file/DB diffs |
| 2. Capability + fallback contract | Schema/FTS readiness, supported-feature matrix, disabled/fallback responses for ruler, batch merge, container proxy, VNet, snapshots, shares, unsupported runtimes | DB-FTS-01 plus disabled-flow checks from relevant rows | Every unsupported P0 is either hidden/disabled before mutation or remains a blocker | Readiness payload, feature matrix, frontend screenshots |
| 3. Domain integrity | Root/trunk/status invariants, dormant/wake worktree consistency, merge protections, project deletion cleanup, fork-at-message/inherit modes, interactive batch merge | DOMAIN-CHAPTER-01, DOMAIN-MERGE-01 | DB/git/worktree side effects match TS or unsupported modes reject before mutation | DB row diffs, git status/log, worktree listing |
| 4. Frontend contracts | Narrator WS names and `messageUuid`, batch merge session fields/events, ruler fields/lifecycle, terminal reconnect/fallback, snapshot `chapterId`, provider/settings payloads | NARRATOR-WS-01, DOMAIN-RULER-01, RUNTIME-TERM-01, RUNTIME-SNAPSHOT-01, FRONTEND-NOCRASH-01 | No frontend uncaught exceptions; required fields/events present or visibly disabled | WS/SSE logs, browser console, screenshots |
| 5. Runtime side effects | Terminal scrollback/dtach, container proxy/ports, real volume snapshots, `/ws/vnet` or disabled entry points, share lifecycle | RUNTIME-TERM-01, RUNTIME-CONTAINER-01, RUNTIME-SNAPSHOT-01, RUNTIME-SHARE-01 | Real side effects match TS, or fallback contract is proven with no misleading success | Podman/terminal logs, port/proxy state, archive evidence |
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

- **Notification-sound public mutation blocker requires re-check before implementation.** The current Go router registers `notification-sounds` inside the authenticated API group (`go_backend/internal/app/router.go`), so the older P0 in the API section may be stale. Keep an auth-boundary smoke test, but do not spend implementation time on this item until a focused route test confirms the mismatch still exists.
- **Changelog is partial, not missing.** Current Go has `/api/changelog`, but it reads file-system candidate directories and does not match TS embedded-changelog behavior. Track as P2/P1 ops parity, not a missing route.
- **Chapter split route exists but is semantically shallow.** Current Go `split` delegates to `Fork` with `StartCommitSHA`/`AnchorCommitSHA`; it does not implement full prefix/continuation split semantics. Track as P1/P0 depending on whether the UI exposes split as a TS-equivalent operation.
- **Many previous wave items are marked DONE because fallback behavior was accepted.** Treat fallback completion as compatibility scaffolding, not as proof that Go is replacement-ready.

### Wave H — Revalidated P0/P1 Blockers

| ID | Owner | Priority | Scope | Acceptance criteria | Verification |
|---|---|---:|---|---|---|
| H01-admin-boundary-current | codex-h01-api | P0 | Implemented 2026-05-15: re-checked and fixed current admin-only mismatches for `/api/chapters/podman/install`, `/api/storage/*`, and benchmark result/summary/compare paths. | Normal users are denied before privileged mutations/scans with TS-compatible 403 `FORBIDDEN`; admin smoke reaches safe handlers. | Focused route tests added in `go_backend/internal/app`, `chapters`, and `systemroutes`; TS evidence: `server/routes/chapters.ts`, `storage.ts`, `benchmarks.ts`. Attempted `go test ./internal/app`, `go test ./internal/chapters`, `go test ./internal/systemroutes`, `go test ./internal/misccompat`; blocked before H01 test execution by unrelated `internal/ws` build errors and `internal/narrators` duplicate helper declarations. |
| H02-api-contract-guard | Wave H02 implementation subagent | P0 | **Implemented 2026-05-15:** Go now applies the TS `GIT_NOT_INSTALLED` guard only to `GIT_REQUIRED_RULES`-equivalent project/chapter/ruler/git-panel routes, aligns `GET /api/narrators?standalone=true` to the TS paginated object shape, rejects bare `GET /api/narrators` with `VALIDATION_ERROR`, and returns `TOKEN_EXPIRED` for expired JWTs. | Frontend-required narrator list response shape matches TS; Git-missing routes return stable 503 `GIT_NOT_INSTALLED`; expired tokens return a distinct expired code; setup/settings/provider/search/standalone narrator routes are not over-blocked by the Git guard. | PASS: `go test ./internal/app ./internal/auth ./internal/narrators ./internal/gitroutes ./internal/chapters`. Focused tests cover guard matching/response, non-Git over-block checks, standalone list shape, bare list validation, and expired JWT code. |
| H03-agent-history-content-json | codex-h03-agent | P0 | **Implemented 2026-05-15:** Go now rebuilds replay history from `content_json` and completed tool-call rows instead of `content_text` only. Preserves text, tool_use, tool_result, supported reasoning/redacted thinking, compact boundaries, and trailing tool results; images/sidecars/native web-search/image-generation exact replay remain provider-specific follow-ups. | Existing TS-created sessions with prior tool calls can continue in Go without losing the core tool transcript context. | PASS: `go test ./internal/narrators`; PASS: `go test ./internal/agentbasic`. Fixture tests cover multi-turn tool calls, Responses request shape, Anthropic thinking/redacted blocks, compact summary replay, and explicit unsupported-reasoning degradation. |
| H04-subagent-message-tree | Wave H04 implementation subagent | P0 | **Implemented 2026-05-15:** Go now binds subagent user/assistant/tool-result message records to the parent Agent tool use via `parent_tool_use_id`, including foreground/background Agent launches and later Send/TeamStatus message paths. Child message broadcasts are also mirrored to the parent narrator with child/parent tool metadata. | Parent narrator message tree shows complete subagent child output; reconnect/catch-up preserves child tree and statuses. Detach/attach parent-blocking semantics are now covered by H10. | PASS: `go test ./internal/narrators -run 'TestH04|TestSubagent|TestMessagesAfter|TestMessagesCursor|TestBackgroundAgent'`; PASS: `go test ./internal/narrators`; PASS: `go test ./internal/ws`. Focused tests assert `parent_tool_use_id`, parent tree/catch-up, parent WS broadcast, foreground/background Agent, Send/Await, and TeamStatus. |
| H05-tool-registry-mcp | Wave H05 implementation subagent | P0/P1 | **Phase 2 implemented 2026-05-18:** Go static tool specs still resolve through the inventory layer, and connected stdio MCP tools are now injected into narrator provider tool lists as `mcp__server__tool` entries with guarded execution. MCP behavior permissions (`readOnly`/`readWrite`/`ask`/`deny`) and subagent-visible MCP filtering now follow the TS contract for the stdio path. | Connected stdio MCP tools can be called from Go narrator sessions through per-call JSON-RPC stdio processes; denied/unavailable tools are not exposed and hallucinated external tools are guarded. Persistent MCP runtimes and non-stdio transports remain documented follow-ups. | PASS: `go test ./internal/agentbasic`; PASS: `go test ./internal/mcpcompat`; PASS: `go test ./internal/narrators`. Added coverage for dynamic MCP inventory/provider formatting, guarded external execution, stdio fake-server call flow, permission mapping, first-token/silent-disconnect handling, and subagent disabled-tool filtering. |
| H06-schema-fts-readiness | Wave H06 implementation subagent | P1-critical | Implemented 2026-05-15: Go read-only readiness now checks canonical tables/columns plus high-value TS indexes/unique constraints, representative core FKs, FTS5 trigram external-content SQL, and FTS sync triggers; Go settings defaults were aligned with TS for first-write defaults that Go may create. | Go still does not mutate the main DB canonical schema; readiness failures name exact missing/incompatible objects and instruct operators to run TS Drizzle/FTS setup. Go-owned optional tables such as `narrator_browser_sessions` remain outside canonical readiness. | PASS: `go test ./internal/db ./internal/config ./internal/settings`. Static schema mutation guard remains in `go_backend/internal/db` and passed as part of `./internal/db`. |
| H09-browser-session-runtime | main narrator | P0/P1 | **Implemented 2026-05-18:** Go Browser sessions are now narrator-bound in the Rod runtime, persisted to a Go-owned optional `narrator_browser_sessions` table, and surfaced through lifecycle observer events. Browser API list returns frontend-compatible millisecond fields, close/TTL/screenshot/stop-tracing manage live sessions with DB fallback, screenshots/traces use bounded metadata persistence, and health capability reports supported `go-rod` runtime with Chrome required. | Browser sessions no longer masquerade as disabled fallback when Go owns the runtime; cross-narrator session access is denied, active count broadcasts are driven by observer persistence, and runtime scan/cleanup can combine live and persisted sessions. | PASS: `go test ./internal/agentbasic`; PASS: `go test ./internal/narrators`; PASS: `go test ./internal/app`. Added unit/contract tests for narrator binding, resource limit, expiration observer, optional table creation, bounded artifact persistence/count, list ms shape, and capability contract. |
| H10-subagent-detach-reattach | main narrator | P0/P1 | **Implemented 2026-05-18:** Go subagents now detach foreground Agent calls into background tasks that unblock parent-blocking Await/Agent tool calls, persist aliases/task rows, broadcast detach/start/status events, and inject completed/failed/cancelled background results back into the parent `narrator_tool_calls` output. Attach/Await handle running/completed/stale background agents, Send continuations persist the caller tool-use id, and stale running tasks are reconciled to failed instead of hanging. | Detach no longer waits for timeout to unblock the parent; background completion survives reconnect/history replay through DB result injection; stale runtime recovery prevents permanent running state after restart. Remaining parity gaps are TS abort-controller source swapping, richer team inbox/file-change state, and custom prompt/runtime polish. | PASS: `go test ./internal/narrators`; PASS: `go test ./internal/app`; PASS: `go test ./...`. Added tests for parent-blocking detach, attach active/stale behavior, result injection, cancellation injection, Send parent-tool-use persistence, and capability contract. |
| H07-domain-split-sync | codex-h07-domain | P1-critical | Phase 1 implemented: TS-equivalent split is explicitly disabled before mutation; `chapter_edges` manual create/delete is restricted to dependency edges; graph/git responses expose commit-sync/worktree-watcher fallback metadata. | Split cannot masquerade as full TS split unless prefix/continuation/message truncation/edge semantics are implemented; manual edges cannot corrupt fork/merge/review graph semantics. Phase 1 leaves true split and background watcher parity as documented gaps. | Edge/graph/git tests pass; chapters split test is added but package execution is currently blocked by unrelated `internal/narrators` duplicate helper definitions. |
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

#### H07 Phase 1 Result — 2026-05-15

- `POST /api/chapters/:id/split` now returns a stable `501` disabled response before any
  database/git mutation: `{ ok:false, code:"FEATURE_DISABLED", feature:"chapter.split", ... }`.
  This prevents the prior Go behavior from masquerading as TS split-at-commit while only doing a
  shallow fork from `commitSha`.
- Manual `chapter_edges` operations now only allow `dependency`. Lifecycle-owned edges
  (`fork`, `merge`, `review`, `cherry_pick`) remain readable but cannot be manually created/deleted
  through the generic edge route, preventing redundant field/graph corruption.
- Graph responses expose domain fallback capabilities for disabled split, dependency-only manual
  edges, and commit-sync/worktree-watcher status. Git commit responses include `commitSync` metadata
  showing the current manual-route-record fallback and `worktreeWatcher:false`.
- Focused evidence added: split disabled contract test, edge self/cross-project/type/manual
  restriction tests, graph capability/git metadata assertions, and git commit-sync metadata/assertions.
- Test result: `go test ./internal/chapteredges ./internal/graph ./internal/gitroutes ./internal/git`
  passes. `go test ./internal/chapters -run TestSplitRouteDisabledBeforeMutation` is blocked before
  running H07 tests by current unrelated `internal/narrators` compile errors (`firstNonEmptyString`
  and `stringFromAny` redeclared between `service.go` and `agent_history_content.go`).
- Remaining H07 gaps: true TS split prefix/continuation/message truncation semantics are still not
  implemented; background commit-sync and TS-equivalent worktree watcher are still fallback-only.

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
