# 请求证据统一设计：循环录制 dump × 使用历史 dump

> 状态：讨论稿（分支 `claude/dump-unify-b2czhh`）。本文只做设计，不含实现。
> 现行行为说明见 `docs/REQUEST_DUMPS.md`，本文落地后应改写该文档。

## 1. 现状：一次请求，两条互不相识的管线

同一次 `provider.chat()` 现在由 `ApiRequestDumpCollector`（`server/lib/agent/request-dump.ts`）同时喂给两个系统：

| | 旧版使用历史 dump | 循环录制 |
| --- | --- | --- |
| 回答的问题 | 「这条使用记录发了什么、回了什么」 | 「这个叙述者最近实际发了什么」 |
| 采集 | collector 在主线程内存里攒 JSON 快照（`snapshot()`），受 `requestDumpMaxSize` / `HARD_DUMP_MAX_BYTES` 截断 | collector 把原始字节流式发给 recorder worker（`request-recorder.ts`） |
| 存储 | `api_requests.raw_dump_json`（≤512KiB 头部）+ `~/.narrafork/request-dumps/` 溢写文件；畸形请求另写 `malformed-request-dumps/` | `request-recordings/` 循环区 + `saved-request-recordings/` 固定区（chunks/*.jsonl + manifest） |
| 索引键 | `api_requests.id` | 录制 `requestId`（loop 内的 attempt id），靠 `bind` 消息挂上 `apiRequestId`，只存在 worker 里 |
| 何时保留 | `requestDumpEnabled`（+ErrorsOnly）、泄漏检测 `forceDumpPersist`、畸形请求 | 循环窗口常驻；`/save_dump`、报错卡片下载、`requestDumpEnabled` 持续保留 |
| 格式 | 单个 JSON（`request.body` 已解析、`response.events`、`attempts`…） | ZIP：manifest + 分段帧 |
| 入口 | 使用历史表格 → 查看/下载 raw dump | 报错卡片「下载请求」、`/save_dump`、SaveDumpModal |
| 消费方 | 使用历史 UI、token 修复（`usage-token-repair-evidence.ts` 读 `response.events` 里的 usage）、ShareFile 兼容 | 只有下载 |
| 存储统计 | `requestDumps` | `requestRecordings`、`savedRequestRecordings` |

### 1.1 割裂带来的具体问题

1. **同一开关、双写。** `requestDumpEnabled` 打开后，同一份请求既写进 `raw_dump_json`/溢写文件，又在 recorder 里持续固定一份；磁盘占两份，两份的截断规则和完整性标记还不一样（`bodyTruncated` vs `partial/truncated`）。
2. **使用历史看不到录制。** 用户在使用历史里找到报错记录，`hasRawDump=false` 就没有任何证据入口——尽管循环窗口里可能还留着这条请求的完整字节。反过来，报错卡片下载的 ZIP 和使用历史行之间也没有可见的关联。
3. **`hasRawDump` 名不副实。** `raw_dump_json` 在 dump 关闭时也会写入有界 diagnostics（`shouldPersistRawDump` 里的 `normalizedDiagnostics`），列表就显示「查看 dump」，点开只有诊断摘要。
4. **关联只活在 worker 内存/manifest。** `recordingRequestId → apiRequestId` 只通过 `bind` 进 recorder，数据库里没有可索引的列；使用历史无法有界地查询「这条记录有没有录制」。
5. **三套「固定」语义。** 溢写文件按数量 mtime 淘汰（可随意删）、畸形 dump 目录 20 个上限、固定区「保留到用户删除」。用户分不清哪个会被自动清掉。
6. **设置语义混杂。** `requestDumpEnabled` 的文案是「强制持续保留」，实际同时决定旧版 JSON 是否入库；`requestDumpMaxSize` 只约束旧版，循环双预算只约束录制，用户需要理解两套容量。
7. **主线程负担。** 旧版快照路径要在主线程 `JSON.parse/stringify` 整个请求体（`materializeRequest`、`toJsonSafe`），与 CLAUDE.md 的主线程性能规则相悖；录制路径已经是流式 + worker。

## 2. 目标与非目标

**目标**

- 一次请求只有**一份证据**，一条采集管线（流式 → recorder worker）。
- 一个**统一索引**：从使用历史行、报错卡片、`/save_dump` 列表都能定位到同一份证据，并看到它的状态。
- 一套**保留语义**：证据要么在循环窗口里（会被淘汰），要么被「固定」（到用户删除为止），固定原因可见。
- 一套**设置**和一个**存储分类**。
- 旧数据可读，不做大批量同步迁移。

**非目标**

- 不改 recorder 的段文件格式、崩溃恢复、下载租约等已经验证过的机制。
- 不改插件 provider 的 dump 事件协议（它们已经经过 collector）。
- 不在本轮处理 ShareFile 对旧目录的兼容（保持现状，旧目录只读老化）。

## 3. 核心模型：请求证据（Request Evidence）

把「dump」和「录制」合并为一个概念：**请求证据**。它就是现有的录制，旧版 JSON dump 降级为它的一种**派生视图**和**历史来源**。

```
provider.chat()
  └─ ApiRequestDumpCollector  ──(字节流)──▶ recorder worker
        │                                      ├─ 循环区  ring/       （会淘汰）
        │  有界 diagnostics（≤ 64KiB 前缀）      └─ 固定区  saved/      （到用户删除）
        ▼                                             ▲
  api_requests.raw_dump_json  ← 只存 diagnostics       │ pin(reason)
  api_requests.recording_id   ← 新增，关联证据 ─────────┘
```

### 3.1 证据状态

| 状态 | 含义 | 用户可做 |
| --- | --- | --- |
| `ring` | 仍在循环窗口内 | 固定并下载 |
| `pinned` | 已固定（带 `reason`） | 下载、删除 |
| `evicted` | 已被循环淘汰，未固定 | 只能看 diagnostics |
| `legacy` | 旧版 `raw_dump_json`/溢写文件 | 查看、下载（只读兼容） |
| `none` | 录制关闭或该请求未录 | 只能看 diagnostics |

### 3.2 固定原因（`pin.reason`）

把现在分散的「必须留下」路径全部收敛为一次 pin：

| reason | 来源（现状） | 统一后 |
| --- | --- | --- |
| `manual` | `/save_dump`、SaveDumpModal | 不变（窗口快照，可多请求） |
| `request` | 报错卡片「下载请求」（`save-specific`） | 不变；使用历史行新增同一入口 |
| `continuous` | `requestDumpEnabled` | 自动固定策略 = 全部 |
| `error` | `requestDumpEnabled + ErrorsOnly` | 自动固定策略 = 仅错误 |
| `leak` | 泄漏检测 `forceDumpPersist` 写旧版 dump | 改为在 `finish` 时请求 recorder 固定该请求 |
| `malformed` | `writeMalformedRequestDump` 写 `malformed-request-dumps/` | 同上；畸形摘要（`capture`）写进 manifest 元数据 |

`leak`/`malformed` 需要「即使循环录制关闭也要留下」：沿用现有持续模式的做法——collector 一律创建录制（这两类 provider/场景本来就强制收集），`begin` 带 `pinOnFinish` 提示，worker 收到 `end` 时决定固定。recorder 的单份 512MiB、保存数量、磁盘压力限制统一兜底。

## 4. 关键改动

### 4.1 数据库：一列关联，diagnostics 去名

- `api_requests` 新增 `recording_request_id text`（可空，建索引 `(recording_request_id)`）。tracker 在 `createApiRequest`/`finish` 时已经拿到 `options.recordingRequestId`，顺手写入；`bindRecordingRequest` 保留，worker 仍需要它写 manifest。
- 固定状态**不**写进数据库：它的权威在 recorder（manifest/summary），且循环淘汰频繁，镜像进 SQLite 会变成高频写。使用历史列表需要状态时，对当前页（≤100 条）的 `recording_request_id` 批量调用 recorder 新增的 `status` 操作（worker 内存索引查表，O(n)，有上限）。
- `raw_dump_json` 语义收窄为「诊断 + 旧版兼容」：新请求只写 diagnostics；列表字段拆成 `hasDiagnostics` 与 `hasLegacyDump`（`json_extract(raw_dump_json,'$.request') IS NOT NULL OR '$.spill'`——注意这是列表路径，需要避免全行读取；可改为写入时就设定一个小的 `dump_kind` 列，详见待定 Q3）。
- 迁移按 CLAUDE.md 流程：改 `server/db/schema.ts` → `bun run db:generate`；PG 走 `db:generate:pg`。

### 4.2 recorder 新增操作

```
{ type: "status", narratorId?, requestIds: string[] }      → Record<id, EvidenceStatus>
{ type: "save-specific", narratorId, requestId, reason }   ← 增加 reason
{ type: "begin", ..., pinOnFinish?: "leak" | "malformed" } ← 新提示
```

`EvidenceStatus = { state, saveId?, reason?, bytes, partial, truncated }`。`status` 只查内存索引，不读段文件。权限仍在路由层按叙述者 ACL 判定，worker 内再核对 narratorId 归属。

### 4.3 采集：去掉旧版快照

- `ApiRequestDumpCollector` 只保留：转发给 recording、有界 diagnostics 前缀（现有 `RING_DIAGNOSTIC_PREFIX_BYTES`）、畸形请求的结构摘要。删除 `snapshot()`/`materializeRequest()`/`attempts` 预览这类主线程 JSON 物化。
- `loop.ts` 的 `rawDump: requestDumpEnabled || forceDumpPersist ? snapshot() : …` 改为只传 diagnostics；`forceDumpPersist` 变成 `pin("leak")`。
- `api-request-dump-store.ts` 的溢写写入路径、`malformed-request-dump.ts` 的写文件路径下线；读取路径保留给 legacy。

### 4.4 派生视图：「旧版 JSON」只在导出时生成

很多人（和 token 修复）习惯的是 `{ request: { body }, response: { events } }` 这种 JSON。统一后它由 worker 在**打包 ZIP 时**从帧派生，作为 bundle 内的 `dump.json`（每个请求一份，有上限，超限就不生成并在 manifest 标注），而不是在请求热路径上维护。这样：

- 用户下载拿到的始终是同一种 ZIP：`manifest.json` + `chunks/` + `dump.json`（可读视图）。
- 旧版行的下载也包成同样的 ZIP：`manifest.json`（`source: "legacy"`）+ 原 dump JSON。使用历史的「下载」按钮只有一种产物。

### 4.5 header 完整度：给 headers 单独预算

现状：`sanitizeRecordingMetadata`（`request-recorder-sanitize.ts`）对整份 meta 一视同仁地限制为 32 个键、值 1024 字符、合计 8KiB，超出就丢弃尾部键并标 `metadataTruncated`。body 是逐字节录制的，但 header 多的网关（大量 `x-ratelimit-*`、trace 头）会丢失后面的 header，这一点不如旧版 dump（只掩码凭据、不限数量）。录制要取代旧版，这里必须补齐。

- `request` / `response` meta 中的 `headers` 字段单独处理，不计入通用 8KiB meta 预算：最多 256 个，单值 8KiB，合计 64KiB；按原始顺序保留，超出时保留前段并标 `headersTruncated`（与 `metadataTruncated` 区分）。
- 掩码规则不变（凭据类 header 值替换为 `[REDACTED]`），并与 `sanitizeHeaders` 的名单合并为一份，避免两处漂移。
- 重复 header（如多个 `set-cookie`、`via`）保留为数组，不再被 `Headers.entries()` 合并时丢失。
- 预算按次 attempt 计；`/v1` 回退、重连等多次 attempt 各自独立，不互相挤占。

### 4.6 token 修复改为读证据

`analyzeTokenRepairEvidence(row, rawDumpJson)` 改为接收一个证据源接口：

```ts
interface UsageEvidenceSource {
  kind: "recording" | "legacy";
  responseItems(signal): AsyncIterable<unknown>; // 已解析的 SSE/JSON 响应事件
  requestUrl?: string;
  complete: boolean; // 对应 partial/truncated/bodyIncomplete
}
```

recording 实现由 evidence worker 读固定区段文件流式解析（已在 worker 内，不碰主线程）；legacy 实现就是现有逻辑。注意：只有**被固定**的证据对修复可用，循环区的可能随时淘汰——修复任务遇到 `ring` 状态可以先 pin(`request`) 再读，或直接判 `insufficient`（待定 Q4）。

### 4.7 UI：一个证据面板，三个入口

新增 `RequestEvidencePanel` + `useRequestEvidence(narratorId, recordingRequestId | apiRequestId)`，由现在的 `useErrorRequestDump` 演化：

- **使用历史详情抽屉**：显示状态徽标（循环中 / 已固定·原因 / 已淘汰 / 旧版），按钮「固定并下载」或「下载」；diagnostics 单独一节，不再冒充 dump。列表的「查看 dump」改为证据状态图标。
- **报错卡片**：复用同一面板（现在的 ErrorRequestModal）。
- **SaveDumpModal（`/save_dump` 列表）**：每份固定保存列出包含的请求，可跳转到对应使用历史行。

### 4.8 设置：一节「请求证据」

| 新设置 | 取代 | 迁移规则 |
| --- | --- | --- |
| `requestEvidence.ringEnabled` | `requestDumpRecordingEnabled` | 原值 |
| `requestEvidence.ringRequestBytes` / `ringResponseBytes` | 同名旧键 | 原值 |
| `requestEvidence.autoPin: "off" \| "errors" \| "all"` | `requestDumpEnabled` + `requestDumpErrorsOnly` | 关→off；开+仅错误→errors；开→all |
| （删除） | `requestDumpMaxSize` | 丢弃；插件协议里给插件的 `requestDump.maxBytes` 改用内部常量 |

旧键在 `settings/index.ts` 的加载迁移中改写一次（与现有 `LEGACY_REQUEST_DUMP_MAX_SIZE` 改写同一处），API 读写在一个版本内同时接受旧键。是否改键名见待定 Q5。

### 4.9 存储统计

合并为一个分类 `requestEvidence`，下分三行：循环区、固定区、旧版（`request-dumps/` + `malformed-request-dumps/` + `raw_dump_json` 估算）。旧版行提供「清理旧版 dump」按钮，走现有 database-cleanup 后台任务。

## 5. 分阶段落地

每一阶段都可单独合并，且不破坏现状。

1. **P1 打通索引（纯增量）**：`recording_request_id` 列 + recorder `status` 操作 + 使用历史抽屉显示证据状态与「固定并下载」；headers 单独预算（4.5）。用户立刻能在使用历史里拿到循环录制。
2. **P2 收敛固定路径**：`leak`/`malformed` 改为 pin；`requestDumpEnabled` 不再写旧版 JSON（停止双写）；`raw_dump_json` 只写 diagnostics；列表字段拆分。ZIP 内增加派生 `dump.json`。
3. **P3 消费方迁移**：token 修复双源；设置合并与迁移；存储分类合并；`REQUEST_DUMPS.md` 改写。
4. **P4 下线旧写入代码**：删除溢写写入、畸形 dump 写入、collector 快照物化；旧版只读路径保留到后续版本再评估移除。

## 6. 待定问题（需要决定）

- **Q1 旧数据**：推荐**只读兼容**（旧行继续能查看/下载，随清理任务老化），不做后台转换成固定保存——转换要读大量溢写文件，收益只是格式统一。
- **Q2 `/save_dump` 窗口快照是否保留多请求语义**：推荐保留；它和单请求固定是两种不同用途（「刚才那一段」vs「这一条」）。
- **Q3 列表如何判断旧版 dump**：推荐新增小列 `dump_kind`（`diagnostics` / `legacy`）在写入时设定，避免列表路径 `json_extract` 大字段；旧行由后台一次性分批回填，未回填前按 `legacy` 对待。
- **Q4 token 修复遇到循环区证据**：推荐直接判 `insufficient`，不为修复自动 pin，避免修复任务悄悄占用固定区。
- **Q5 设置是否改键名**：推荐改为 `requestEvidence.*` 并迁移，因为 `requestDumpEnabled` 的含义已经变了，沿用旧名会继续误导；代价是一个版本的双键兼容。
