# go_backend 非 DB 迁移补齐规格

## 目标

在不实现、不修改主数据库迁移所有权的前提下，补齐 `go_backend` 相对 TypeScript 后端的非 DB gap，使 Go 后端从“兼容 API/runtime”推进到“可作为主要运行后端”的状态。

本规格明确排除：

- 不在 Go 后端创建或修改主库 schema。
- 不实现 Drizzle migration 的 Go 版本。
- 不删除、重建或修复 `~/.narrafork/narrafork.db`。
- 不修改 `drizzle/` 迁移文件。

允许做：

- 保持 `VerifyCanonicalSchema` 只读校验。
- 增强运行时对已有 schema 的使用。
- 增加非迁移类 contract tests / fixtures。
- 增加前端 capability 降级逻辑。
- 修复 Go 查询、服务、WS、Agent、Terminal、安全与运维行为。

## 成功标准

1. 前端主流程在 Go 后端下可以稳定使用：Dashboard、Story Network、Ruler、Narrator、Terminal、Settings、Search、Review。
2. 所有已知 fallback/stub 必须满足二选一：
   - 完整补齐；或
   - 通过 capability 明确暴露，并由前端隐藏/降级。
3. Go 后端不再返回由 schema 字段错误导致的运行时 SQL error。
4. Narrator WS 的核心事件 contract 与前端消费方对齐。
5. Go 后端的安全默认值不弱于 TS 后端的生产基线。
6. 测试覆盖 API contract、WS contract、Ruler、Terminal、Agent tools、权限和安全关键路径。

## 当前范围内的非目标

- 主数据库迁移 ownership。
- FTS 创建/重建/修复。
- Drizzle migration hash/stamp 兼容。
- 删除或重建用户数据库。
- 一次性完全复刻所有 provider 边缘特性；provider 深度补齐按验收矩阵推进。

## 工作包 1：API 契约与 capability 降级

### 背景

Go 后端已注册大量 TS 路由，但请求校验、错误格式、fallback 响应和响应 shape 仍有漂移风险。

### 要求

- 建立 TS ↔ Go API contract matrix。
- 对以下路由组建立 golden tests：
  - `/api/projects`
  - `/api/chapters`
  - `/api/chapter-edges`
  - `/api/narrators`
  - `/api/terminals`
  - `/api/settings`
  - `/api/fs`
  - `/api/search`
  - `/api/reviews`
  - `/api/update`
  - provider routes
- 统一 fallback 响应格式：
  - `ok: false`
  - `code`
  - `feature`
  - `supported: false`
  - `fallback: true`
  - `error` / `message`
- 前端读取 Go capabilities，对 disabled 功能隐藏或置灰。

### 验收

- Contract tests 可覆盖认证、admin、git-required guard、成功响应、错误响应。
- 前端不再展示 Go 已明确 unsupported 的危险操作入口。

## 工作包 2：Story Network / Ruler / Graph

### 背景

Graph 可打开，但 upstream updates、commit sync、split、Ruler 复杂操作存在降级；并且当前 Go Ruler 查询引用了 TS schema 不存在的列。

### 要求

- 修复 `rulerProjectChapters`：
  - 不再查询 `chapters.narrator_id` / `chapters.narrator_status`。
  - 改为 JOIN / 子查询 `narrators` 获取 primary narrator 信息。
- Graph response 字段对齐前端依赖：
  - narratorId
  - narratorStatus
  - narratorSubstatus
  - messageVersion
  - hasContainers
  - hasUpstreamUpdates
  - commitCount
  - headCommitSha
  - reviewSourceChapterId
  - reviewStatus
- Split 暂不实现时：
  - capability 标记 disabled。
  - 前端隐藏 split 入口。
- Ruler 操作补齐或显式降级：
  - fork
  - merge
  - abandon
  - rebase
  - rebase conflict resolve / abort / continue

### 验收

- Ruler 基础端到端：fork → merge → abandon → rebase。
- 有冲突 rebase 能返回可处理状态，不出现未捕获 500。
- Graph contract test 覆盖 nodes / edges / capabilities。

## 工作包 3：Git、Worktree Watcher、Commit Sync

### 背景

Go 当前 commit metadata 主要在显式 Go git 操作或 graph read 时刷新，不等价 TS 的后台 watcher / sync。

### 要求

- 实现 Go 版 worktree watcher 或轮询 fallback。
- 实现 commit sync service：
  - 记录外部 commit。
  - 更新 chapter head / commit count。
  - 与 merge/fork/cherry-pick/rebase 后续状态联动。
- 广播相关 WS 事件：
  - git status updated
  - graph stale / refreshed
  - chapter commit updated
- 保证 watcher 不阻塞主请求路径：
  - debounce。
  - 限制 git log/status 输出。
  - timeout。
  - cancel。

### 验收

- 手动在 worktree commit 后，前端无需刷新即可看到状态变化。
- Graph 中 commitCount/headCommitSha 与实际 git 状态一致。
- watcher 压测不会导致 WS/HTTP 无响应。

## 工作包 4：Narrator WS 合约

### 背景

Go 已有 WS 兼容层，但前端 `useNarratorWS` 监听事件谱比 Go 当前事件更宽。

### 要求

优先保证以下 P0 事件：

- `message`
- `user_message`
- `stream_event`
- `tool_started`
- `tool_use_chunk`
- `tool_completed`
- `permission_request`
- `permission_resolved`
- `status_change`
- `substatus_change`
- `catch_up`
- `sync_ok`
- `full_reload`
- `streaming_snapshot`

补齐 P1 事件：

- `tool_output`
- `tool_long_running`
- `todos_updated`
- `goals_set`
- `plan_mode_changed`
- `compact_done`
- `compact_failed`
- `context_usage`
- `metering`
- `git_status`
- `background_task_*`
- `subagent_*`
- `browser_session_count`
- `queue_status`
- `nkp_queue_status`

### 验收

- 以 `frontend/hooks/useNarratorWS.ts` 的 switch case 作为事件矩阵建立测试。
- 普通发消息、工具调用、权限审批、取消、retry、compact、subagent、background task、catch-up 都有测试。

## 工作包 5：Agent / Narrator Runtime

### 背景

Go 基础 agent loop 可运行，但工具系统、权限、上下文恢复、compact、subagent 和 provider 语义仍弱于 TS。

### 要求

#### 工具补齐

补齐或显式降级以下工具：

- Terminal
- Recall
- ForkNarrator
- NarraForkAdmin
- ContinueTask
- MCP injected tools
- Review tools

#### 权限系统

- 支持 global / project / narrator 三层权限合并。
- Bash command analysis 对齐 TS。
- 支持 updated input / feedbackText / answers。
- 补齐 danger reflection / plan reflection / goal completion reflection 的事件和状态。

#### Context / Compact / Recovery

- compact 从 fallback summary 升级为可调用模型生成摘要。
- compressed fork 生成真正上下文摘要。
- context overflow 后自动 compact/retry。
- retry / continue / interrupt 与消息历史重建一致。

#### Subagent

- 对齐 explore / plan / general / review 的工具过滤。
- 支持 custom subagent prompt 和工具权限配置。
- 支持 background completion、parent continuation、TeamStatus、Send、Await 的完整行为。

### 验收

- Agent 工具 inventory 与 TS 输出可比对。
- 权限审批端到端测试覆盖 allow/deny/updated input/ask user/danger reflection。
- 长会话触发 compact/retry 后能继续运行。
- Subagent foreground/background 都能正确写入 parentToolUseId 消息树。

## 工作包 6：Terminal

### 背景

Go Terminal 基础可用，但 cwd、Windows、scrollback 和 admin reattach 行为不等价。

### 要求

- 创建终端 cwd fallback 对齐 TS：
  1. explicit cwd；
  2. chapter worktree；
  3. narrator.chapter worktree；
  4. narrator.cwd；
  5. home。
- Windows 策略二选一：
  - 实现 Go Windows PTY；或
  - 在 health capability 和前端 UI 中明确禁用。
- scrollback：
  - 避免截断半截 ANSI sequence。
  - 支持持久化或明确降级。
- Admin terminal reattach/orphan route：
  - 补齐真实行为；或
  - 返回 unsupported，不再假成功。

### 验收

- 从 narrator 页面打开 terminal 必须位于正确 worktree/cwd。
- WS subscribe → scrollback → input → resize → output → exit 测试通过。
- Windows 平台前端不展示不可用终端操作，或 Go PTY 可用。

## 工作包 7：Snapshot / Revert / File State Rebuild

### 背景

Go 当前可展示部分 file modifications / patches，但删除消息或回滚 tool call 后的文件状态重建不等价 TS。

### 要求

- 迁移 TS 的 ordered tool call rebuild 思路到 Go。
- 支持：
  - Write
  - Edit
  - MultiEdit 如有
  - Delete / Remove 类行为如有
  - 删除 message 后排除对应 tool calls 重放剩余链路
- `revertFile` / `revertAll` / `unrevertAll` 行为明确且可测试。

### 验收

- 多次编辑同一文件后回滚任意一步，文件状态正确。
- 删除消息/工具块后，文件状态按 surviving tool chain 重建。
- 不覆盖用户在对应 tool call 之后的外部修改，除非用户明确确认。

## 工作包 8：核心服务补齐

### Review

- 补齐 review 防写保护 / git state reset。
- 评审 conclusion、convert、promote、dismiss 与 graph/narrator 状态同步。

### Routine

- 启用 routine 时物化对应 command / skill，禁用时按 TS 语义清理。
- builtin routine inventory 与 TS 对齐。

### Skill

- 校验 global/project skill 扫描规则、companion files、启停状态。
- Skill 工具输出与 TS 对齐。

### Notifications

- 监听 narrator status changed。
- 根据 user preferences 推 DingTalk / Feishu。
- 测试接口和真实自动推送共用发送逻辑。

### Merge / AI Resolve / Merge Summary

- AI conflict resolve 接入完整 narrator/agent runtime。
- Merge summary 从 fallback 升级为真实摘要服务。
- batch merge 冲突等待、AI resolve、rollback、WS progress 对齐。

## 工作包 9：前端 Go capability 降级

### 要求

前端根据 `/api/health.capabilities`、graph capabilities 和 route fallback：

- 隐藏 split。
- 显示 commitSync/worktreeWatcher 降级提示。
- Windows terminal unsupported 时隐藏 terminal 操作。
- vnet disabled 时隐藏 VNet。
- update manual-only 时调整按钮文案。
- gateway QR unsupported 时禁用二维码登录入口。
- storage vacuum disabled 时隐藏危险维护按钮。

### 验收

- 用户不会点击一个 Go 已知 unsupported 的按钮后才看到 501。
- 所有降级都有明确 UI 提示。

## 工作包 10：运维与安全

### TLS

- Go 主服务读取 settings.server.tls。
- tls enabled 时使用 TLS listener。
- 配置与 generateTLS 输出一致。

### CORS

- 对齐 TS：生产使用明确 origin，开发允许 Vite origin。
- 不再无条件回显任意 Origin。

### Share / FS 安全

- share preview 增加 HTML sanitize / CSP。
- 遵守 settings.shares.maxFileSizeMb。
- 非 ASCII 文件名下载头对齐。
- FS mkdir 校验 folder name。
- preview 文本/二进制大小限制对齐。

### Bash / Command Runner

- 支持跨平台 shell resolution。
- timeout、输出上限、kill tree、后台任务、长运行提示对齐 TS。
- 统一命令执行 helper，避免各模块策略分散。

### Logging / Build / Release

- 文件日志路径、JSON line、rotate、log level 对齐 TS。
- 增加 Go build/release 脚本或 Makefile。
- 明确 Go 后端版本注入方式。
- changelog 支持嵌入或打包复制策略。

## 验收测试总表

| 类别 | 必测项 |
|---|---|
| API | projects/chapters/narrators/terminals/settings/reviews/providers contract |
| Graph | nodes/edges/capabilities 字段完整性 |
| Ruler | fork/merge/abandon/rebase/conflict resolve |
| WS | useNarratorWS 全事件矩阵 P0/P1 |
| Agent | tools/permission/subagent/compact/retry/interrupt |
| Terminal | cwd、WS IO、resize、exit、Windows capability |
| Git | watcher、external commit、status broadcast |
| Snapshot | multi-edit revert、message deletion rebuild |
| Security | TLS、CORS、share sanitize、FS mkdir、bash timeout |
| Frontend | Go capability 降级 UI 不误导用户 |

## 实施顺序

1. 修复明确 bug 与 capability 降级。
2. 补 API/WS/Graph contract tests。
3. 补 Ruler、Terminal cwd、Narrator WS P0。
4. 补 watcher、commit sync、project-db sync、notification。
5. 补 Agent tools、permission、compact/recovery、subagent。
6. 补 snapshot/revert 精确重建。
7. 补安全运维：TLS、CORS、share、FS、bash、logging、release。

## 完成定义

本规格完成时必须满足：

- 不修改主 DB migration ownership。
- Go 后端启动仍只读校验主库 schema。
- 所有非 DB P0 gap 已补齐或明确 UI 降级。
- 关键 P1 gap 有测试覆盖并完成实现或 documented fallback。
- 前端主流程可在 Go 后端下连续使用，无未捕获 500、无误导性成功、无静默状态不同步。
