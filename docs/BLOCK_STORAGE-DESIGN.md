# 叙述者消息原生块存储设计

> 状态：讨论稿（分支 `claude/sleepy-bell-wfodnn`）。本文只做设计，不含实现。
> 调研基线：`main` @ `a5a2128e`。文中 `path:line` 都相对仓库根目录；标「推测」的结论没有在代码或测试中直接验证。
> 前提：本设计不以 vlist 性能为目标。vlist 变快只是附带结果，取舍以数据模型的正确性和可扩展性为准。

## 0. 摘要

**现状**
- 一条消息的正文是 `narrator_messages.content_json` 里的一个 JSON 数组。
- 模型可见的工具结果只存在 `narrator_tool_calls`。
- 另有 `content_text`、`context_chars_json`、`original_content_json` 等派生列和快照列。
- 块没有身份。几十处代码按「消息 id + 数组下标」寻址，或者在 SQL 里读 `$[0]`、用 `LIKE` 扫 JSON。

**提案**
- 把块升级为一等存储单位：新表 `narrator_blocks`，一块一行。每行带全局 id、序号、生命周期，以及正文、状态、provider 凭据三类分开存放的列。
- 消息退化为信封：角色、作者、用量、模型、快照。
- `narrator_message_refs` 和 fork 的共享粒度不变。
- 工具执行账本 `narrator_tool_calls` 保留，改为通过 `block_id` 挂到块上。

**三条贯穿规则**
1. **块 id 由服务端在块开始产生时分配。** 流式事件、数据库行、前端、HTTP API、回退、导出，全程使用同一个 id。
2. **模型可见的正文写定后不可变。** UI 状态和运行期状态放在独立的 `state` 列，修改它们不影响模型历史的 revision，也不需要 COW。
3. **结构在写入时盖章。** 轮、步、attempt、工具配对、并行组都写进数据，读取方不再推导。

**读路径**
- 引入规范历史视图。各 provider 的历史构造改成「规范块 → 线上格式」的纯投影。
- loop 内的实时追加和每个 pass 开头的 DB 重建共用同一套投影，消除两条路径的字节差异。

**迁移**
- 每个信封有一个 `body_format`：1 表示旧 JSON，2 表示原生块。读取层两种都认，不需要停机切换。
- 新数据先双写，旧数据由后台分页回填。
- `content_json` 最终只作为兼容投影，供 project.db 归档和导出使用，由块组装；新行写 `'[]'`。

**分四期实施**
- P0：收口写入与投影，不建新表。
- P1：建新表，双写，切换到流式 v2。
- P2：读路径切换，回填存量。
- P3：退役 JSON 依赖，开放块级能力。

## 1. 现状

### 1.1 存储形态

| 存储 | 内容 | 备注 |
|---|---|---|
| `narrator_messages.content_json` | 块数组（TEXT，json mode） | 定义在 `schema.ts:1584`。没有统一的 TS union，写入层用 `{type:string; [k]:unknown}` 兜底（`narrator-persistence.ts:2266`） |
| `narrator_messages.content_text` | 搜索文本和回退文本 | **不是** content_json 的纯函数，见表下说明 |
| `context_chars_json` → `narrator_message_context_stats` | 按块统计的上下文字符数 | 应用层计算（`server/lib/context-characters.ts:49-136`），再由触发器投影到统计表（`drizzle/0206_context_numeric_stats.sql`） |
| `original_content_json` | 首次编辑前的整数组快照 | 写入点 `narrator-session.ts:6989`；publication 用 `COALESCE(original_content_json, content_json)` 读取权威结果 |
| `compact_pending` | VIRTUAL 生成列，读 `$[0]` | 依赖「compact 块永远是首元素」（`schema.ts:1650-1669`） |
| `narrator_tool_calls` | 工具执行账本；**模型可见的工具结果只存在这里** | 每个 attempt 一行。`output_json` 是截断后的文本（2000 行 / 50KB）。与块只靠 `(messageId, toolUseId)` 对应，而 toolUseId 不唯一（`schema.ts:1888`） |
| `narrator_message_refs` | 叙述者 → 消息，带 seq | fork 时共享消息行。seq 不唯一，插入时整体移位（`narrator-refs/seq-store.ts:160-181`） |

`content_text` 与 `content_json` 不一致的几处：
- user 行含附件提示（`narrator-session.ts:3222-3236`）。
- 控制行写的是 `[Compacting]` 一类哨兵字符串（`narrator-persistence.ts:1550,1676,4179`）。
- assistant 行的拼接符两处不一致：`narrator-persistence.ts:2002-2007` 用 `\n`，`narrator-session.ts:7060-7063` 用 `\n\n`。

触发器：
- **FTS 3 个。** update 触发器不限定列，任何列更新都会重建该行的索引（`server/db/fts.ts:378-385`）。
- **`model_history_v1` 共 11 个。** 覆盖消息、工具、ref、用户名、叙述者标题与摘要（`agent-runtime/model-history-revision-sql.ts`）。运行时会核对触发器数量，数量不对就不信任 revision（`model-history-revision.ts:54,107`）。
- **context stats 2 个。**

### 1.2 一次 provider 响应怎样落库

1. 首个 `block_complete` 到达时，懒建一个空壳 assistant 行：`contentJson: []`，不 bump（`narrator-event-handler.ts:2005-2060`）。
2. `OutputContentAccumulator` 在以下时刻产出 checkpoint：lane 切换、provider 内容边界、工具出现前、turn 结束或 abort（`server/lib/agent/output-content.ts:43-56`）。每个 checkpoint 在一个同步事务里：
   - 读出整列；
   - 按 `id+revision` 合并，或按 `outputIndex` 插入；
   - 整列写回，同时重算 `content_text` 和 `context_chars_json`；
   - 随后 bump，并广播整条 `message_updated`（`narrator-persistence.ts:2208-2383`）。
3. tool_use 块落库时建账本行（状态 `initializing`）。之后权限、执行、结果都写账本（`narrator-persistence.ts:2385-2420,3674-3824`）。
4. 收到 `assistant_message` 事件后：
   - 补写 `messageUuid` 和用量；
   - 把块标为 `completed:true`，经 COW 写回；
   - 按调用顺序重排 tool_use 块，这一步是直接 UPDATE（`narrator-event-handler.ts:2270-2391`）。

### 1.3 现状问题

**A. 块没有身份。**
- 现有的块 id：
  - text / reasoning 有 lane id（`${attempt nanoid}:${order}`，`output-content.ts:52,112`），每个 attempt 都会换。
  - tool_use 用 provider 给的 toolUseId，跨消息不唯一。
  - redacted_thinking 和绝大多数自定义块没有 id。
- 因此寻址全靠数组下标：
  - 块删除 / 回滚 API（`routes/narrators.ts:4005-4036,6221-6260`）；
  - 前端 key `${msgId}-b${bi}`，并用正则从 key 字符串反解下标（`vlist-block-target.ts:66-75`）；
  - `toolUseOrder`（`tool-continuation-service.ts:403-413`）；
  - 注入块配对用的 `sourceIndices`（`shared/native-injection.ts:132-138`）；
  - 丢弃 attempt 时按块数基线截断（`narrator-event-handler.ts:1201-1211`）。
- 文件回退为此自建了一套身份：「消息 id + 内容摘要 + 出现序号」。它还在 worker 里手写了一个 JSON 顶层逗号扫描器，用来切出原始片段（`revert-history-commit.ts:1788-1830`；`revert-selection-service.ts:77-99`）。
- 重复的 toolUseId 在前端靠 `#dupN` / `dedupeSuffix` 补丁区分（`pretext-layout-manifest.ts:64-91`；`render-units.ts:488-495`）。

**B. 模型可见正文与 UI 状态混在一列。**
- `translatedText`、review 的 `applied`、merge 的 `isLatest`、卡片 status、`completed` 都写在 content_json 里。
- 改其中任何一个，都会：
  - bump `body_revision` 和所有持有者的 `model_history_revision`；
  - 重建该行的 FTS 索引；
  - 刷新 context stats。
- 其中多处为了省事，绕过 COW 原地修改共享行（`merge-summary-service.ts:186-199`、`narrator-messages.ts:4888-4994`、`narrator-persistence.ts:2429-2458` 等）。

**C. 同一事实多份存储，派生规则各写各的。**
- 工具输入存了两份：`tool_use.input` 和 `input_json`。各 provider 重放时取的不是同一份：
  - Anthropic 取账本（`anthropic-provider.ts:3225-3247`）；
  - Responses 优先账本（`openai-provider.ts:4025-4030`）；
  - Gemini 优先块（`gemini-provider.ts:707-709`）。
- `content_text` 有约 15 个写入点，各自生成。它还被当作查询条件，例如 `eq(contentText,"[Compacting]")`（`narrator-compact.ts:527`）。
- 字符统计在 worker 里另有一份副本（`revert-history-commit.ts:1788-1830`）。

**D. 模型历史有两条产出路径，结果不一致。**

每个 pass 开头从 DB 重建一次历史；loop 内再用 `pushAssistantTurn` / `formatToolResult` 增量追加。已知差异：
- **工具结果顺序。** 重建时，同一消息的工具调用按账本行 id 排序，而行 id 是随机 nanoid（`model-history-source.ts:247-248`），所以工具结果顺序与调用顺序无关。
- **只在内存里的内容。** 工具结果里的图片、turn nudge 都不落库（`loop.ts:7275-7276,7451-7458`）。
- **broken 调用。** 实时路径会剔除，重建时会回放（`loop.ts:7583-7611`）。
- **mid-pass 注入。** 实时路径拼进下一个 user turn，重建后变成独立的 sys 行（`loop.ts:3309-3311`）。
- **strict 判定。** `buildHistory` 用带前缀的 model，`chat()` 用裸 model（`openai-provider.ts:697,710` 对比 `806-807`），对 `nug:gpt-5.5` 两边结论相反。
- **测试。** 目前只有一条「重建 == 实时」的字节等价测试（`openai-provider-history.test.ts:986-1043`）。

**E. provider 不透明数据挂载不全。**
- 已持久化的：
  - reasoning 的 `providerMetadata` + `signatureSource`；
  - tool_use 的 `thoughtSignature`，同时以保留键嵌进 `input_json`（`narrator-persistence.ts:515-536`）；
  - `redacted_thinking.data`。
- 没有持久化的：Responses 的 function_call item id、message `phase`、Gemini 普通 text part 的签名。
- `messageUuid` 只能存一个值，存在两个问题：
  - 多个 message item 时被逐个覆盖（`loop.ts:5254`）；
  - 重放时只挂在第一个 message item 上，而且不检查来源（`openai-provider.ts:3910-3919`）。

**F. 结构靠读取方推导。**
- 服务端只有叙述者级的 `turnStartedAt`（`schema.ts:817`）。
- 前端至少有三套「轮」的定义：
  - recency：以任何非 assistant 消息为界（`run-segments.ts:31-45`）；
  - 流式交接：只以 user 消息为界（`streaming-handoff.ts:19-29`）；
  - 工具排队：按单元前缀（`segment-adapter.ts:4577-4593`）。
- 并行分组在服务端（`tool-execution-groups.ts`）和前端（`shared/tool-parallel-groups.ts`）各算一遍。

**G. 写放大。**
- 每个 checkpoint 都重写整列，并依次触发：
  - FTS 删除再插入；
  - `model_history` 更新，其内部的嵌套 UPDATE 又会再触发一次 FTS；
  - 所有持有者叙述者行的 UPDATE；
  - context stats 的 upsert。
- 然后整条消息广播一次。
- 前端每收到一个 delta，就对已加载窗口重跑 segment → group → adapt → measure：900 条约 5.3ms，6000 条约 35.7ms（`vlist-head-trim.ts:8-17`）。

**H. 共享与 COW 有五套实现。**
- 五处实现对新行的 `narrator_id`、是否克隆工具行，处理各不相同：
  - `copyMessageForNarratorTx`
  - `copyOnWriteMessage`
  - `applyBlockDeletionTx` 的共享分支
  - `copySharedCompactMessageTx`
  - revert 的 `copy_on_write`
- 另有十余处原地修改不检查共享，包括流式追加、翻译、输入改写、卡片状态、段压缩操作。

**I. SQL 依赖 JSON 的数组位置和序列化格式。**
- `compact_pending` 读 `$[0]`。
- compact finalize 的 CAS 用 `json_extract(content_json,'$[0]…')`（`narrator-persistence.ts:1914-1929`）。
- 问题补充读 `$[0]/$[1].body.supplement`（`narrator-question-service.ts:794`）。
- merge summary 和孤儿上传检测都用 `LIKE` 全表扫 JSON（`merge-summary-service.ts:175-183`；`storage-service.ts:826-835`）。
- External API 用 `json_each` 抽取推理块（`external-resource-service.ts:1225-1263`）。

## 2. 目标、收益与非目标

### 2.1 目标

1. **块是一等存储单位。** 有全局唯一的 id，跨流式、持久化、UI、API、回退都不变；有显式序号；可以按种类和状态查询。
2. **模型可见内容只有一个来源，模型历史只有一种产出方式。** 同一版本内，任何时候从 DB 重建出的请求，都与当时实际发送的请求逐字节一致。
3. **正文、UI 状态、provider 凭据分列存放。** 改 UI 状态不会让模型历史失效，也不需要 COW。
4. **多 provider 组装是规范块上的纯投影。** 跨 provider 时携带、降级还是丢弃，规则集中在一处。
5. **结构在写入时确定。** 轮、步、attempt、工具配对、并行组都在写入时记录。
6. **写入按块增量进行。** 流式与持久化共用同一个块对象。
7. **旧数据零停机迁移。** 新旧格式按信封共存；每一期都能单独上线、单独回退。

### 2.2 收益

**修正现有缺陷**（对应 §1.3）：
- 重建历史时工具结果的顺序；工具结果里的图片和附言丢失。
- strict 判定前后不一致；外来 provider 的 item id 被回放给 Responses。
- 改 UI 状态会让模型历史和 prompt cache 失效。
- 下标身份在删块、重试、编辑时错位。
- 五套 COW 和各处绕过共享的原地写入。
- 段压缩标记被 sender 投影当作人类消息（见附录 B）。

**块原生存储新打开的能力**：

| 能力 | 说明 |
|---|---|
| 块级永久链接与引用 | 链接形如 `#blk-<id>`。引用锚点从「消息 id + 前 48 字 needle」（`quote-jump.ts:73-185`）换成块 id。经 COW 复制后，靠 lineage 仍能解析到原块。 |
| 块级搜索 | 可选块级 FTS：命中直接定位到块并高亮。现在只能定位到消息（`frontend/lib/api/types.ts:1055-1062`）。 |
| 块级评论 / 批注 | 以块的 lineage 为键挂评论，fork 后依然可见。现在没有这项能力。 |
| 块级编辑历史 | `original_payload` 保持现有的「看原文」能力；之后可以按需扩展成块 revision 表，支持撤销和对比。 |
| 按种类查询 | 例如 merge summary、图片引用、推理块，都能走索引，不再 `LIKE` 全表扫描或 `json_each`。 |
| 按块的存储治理 | 统计每块字节数；可以按种类制定保留策略，例如旧截图降级为占位、过期的加密推理凭据清除；超大正文的定位也细到块。 |
| 按块的上下文构成 | 「哪些块吃掉了上下文」可以细到单个工具结果或单个推理块，现在是消息级分段。 |
| provider 扩展成本降低 | 新增 provider 只需写一个投影函数和一条携带策略。插件 DTO（`plugin-provider-rpc.ts:350-359`）可以与规范块一一对应。 |
| 增量同步与导出 | WS、catch-up、project.db 同步都能按块变化推送，也就不再出现「原地修改不会再次同步」的缺口（`project-db-sync.ts:289-324`）。 |
| 对外接口演进 | External API 可以在 `full` 层追加 `blockId`，属于向后兼容的扩展；插件也可以按权限拿到块级只读视图。 |
| 前端去推导 | 流式替身、交接启发式、`#dupN`、三套「轮」的定义都可以删除（§8.3）。vlist 因此变简单，变快只是附带结果。 |

### 2.3 非目标

- **不改变 fork 的共享粒度。** `narrator_message_refs` 仍然指向消息（信封），不下放到块。
- **不把工具执行账本并入块表。** 权限、attempt、执行身份、文件历史仍然放在 `narrator_tool_calls`。
- **不改变对外数据契约。** 包括 External API v1、`project.db` 归档格式（只扩展）、插件 API 的可见范围。
- **不在本轮让 PG 承载叙述者写路径。** 但新 schema 必须能被 PG 转译生成，触发器两种方言各写一份。
- **不以 vlist 性能为验收目标。**

## 3. 核心模型

### 3.1 三个层次

```
narrator_message_refs ──▶ narrator_messages（信封）
  （叙述者, seq）          role · 作者 · origin · provider/model/usage · turn/step · 快照
                                │ 1:N，按 ordinal 排序
                                ▼
                          narrator_blocks（块）
                            id · kind · phase · payload · model_text · state · provider_meta
                                │ 每个 tool_use 块 1:N
                                ▼
                          narrator_tool_calls（执行账本，每个 attempt 一行）
                            block_id · status · permission · input/output · 执行身份
```

- **信封**回答「谁、在什么时候、以什么角色、用什么模型说了这一段」。它是 fork 共享与 COW 的单位。
- **块**回答「说了什么」。它是身份、寻址、投影、搜索、编辑的单位。
- **账本**回答「工具实际是怎么执行的」。它不属于会话内容；模型只通过 tool_result 块，看到其中被选定的那一次结果。

### 3.2 块的身份

| 字段 | 含义 |
|---|---|
| `id` | 全局唯一，由 `generateId()` 生成。**在块开始产生时**由服务端分配，时机包括：流式 lane 打开、工具调用开始流式输入、用户消息落库、注入写入。流式事件、DB 行、前端、HTTP API、回退、导出都用这个 id。 |
| `origin_block_id` | COW 复制出的块指回原块；原块本身为 null。「同一个逻辑块」即同一条 lineage。跨叙述者解析永久链接时，在本叙述者的 refs 范围内按 `id` 或 lineage 找唯一的一行；这与账本 `executionOriginToolCallId` 的定位规则一致（`narrator-persistence.ts:3349-3394`）。 |
| `wire_id` | provider 侧的 id，例如 toolUseId / call_id、web_search id。只供投影使用，不承担身份，也不要求唯一。 |
| `attempt_id` | 产生该块的 provider attempt，即现在的 `OutputContentAccumulator.attempt`。丢弃某个 attempt 时按它精确删除，不再按数组下标基线截断。 |

旧数据的块 id 按确定性规则 `${messageId}:${index}` 生成（§10.2）。这样在回填之前、回填过程中、回填之后，同一个块的 id 都不变。

### 3.3 块的种类

现在约 40 种 `type` 归并为下表中的 kind。逐个 type 的映射见附录 A。

「模型投影」列说明 provider 怎样看到这个块：
- **原生**：各 provider 有专门的映射。
- **文本**：投影 `model_text` 字段。
- **不可见**：所有投影都跳过。

| kind | 来源（现有 type） | payload 要点 | 模型投影 |
|---|---|---|---|
| `text` | text | `text`、`citations?`、`fileReferenceContext?` | 原生 |
| `reasoning` | reasoning、旧的 thinking | `text`（摘要或明文）、`format` | 原生；凭据放在 `provider_meta` |
| `redacted_reasoning` | redacted_thinking | 无；数据放在 `provider_meta` | 原生，仅同源时 |
| `tool_use` | tool_use | `name`、`input`（重放给模型的输入） | 原生 |
| `tool_result` | 新增。现在由账本重建，手动 bash 例外 | `toolUseBlockId`、`toolCallId`、`isError`、`images?`、`appendix?` | 原生；文本取自账本输出 |
| `server_tool` | web_search、image_generation | `tool`；搜索类为 `query/queries/action`，生图类为 `savedPath/size/revisedPrompt` | Responses 原生，其它 provider 投成文本摘要 |
| `image` | image | 上传引用：`imageId`、`mediaType`、尺寸、`uploadNarratorId` | 支持图片的 provider 原生，其它投成附件提示 |
| `attachment` | text_file、file_reference | text_file 为 `filename/size/filePath`；file_reference 为 `reference/snapshotText/snapshotHash` | 文本（沿用现有的附件投影和引用展开） |
| `context` | system_injection、6 种自带 modelText 的原生块、review_feedback 等 | `subtype` + 原 body | 文本（`model_text`）；没有 `model_text` 时不可见 |
| `marker` | compact、segment_compact、清空上下文、file_history_checkpoint | `subtype`、`summary?`、`messageCount?` | 不可见（compact 摘要走 system prompt）；段压缩例外，以 `model_text` 呈现 |
| `notice` | info、error、tool_loaded/unloaded、spec_*、subagent_recovery、cwd_recovery、publication result 等 | `subtype` + 原字段 | 不可见；个别按现行规则投影 `model_text`（附录 A 标注） |

两条规则让投影保持简单：

1. **是否对模型可见，在写入时就定下来。** 原生 kind 交给各 provider 的投影处理；其余 kind 只有 `model_text IS NOT NULL` 时才对模型可见。`model_text` 一旦写定，以后不会按新规则重算。现在的原生注入块已经这样处理 `modelText`（`shared/native-injection.ts:20-48`），这里推广到所有非原生块。
2. **旧格式里「text + 注入块」的配对，在解码时合并成一个 `context` 块。** 合并沿用 `injectionBlockViews` 的配对逻辑。此后不会再出现一个逻辑块对应多个物理下标的情况。

### 3.4 正文、状态、凭据三分

| 列 | 内容 | 可变性 | 影响模型历史 revision | 共享信封上的修改 |
|---|---|---|---|---|
| `payload` | 块正文，可能对模型可见，也可能只用于展示 | `done` 之后不可变；编辑会产生新的 revision | 是 | 必须 COW |
| `model_text` | 非原生块对模型可见的文本 | 同上 | 是 | 必须 COW |
| `state` | 非模型可见的可变元数据，见表下列举 | 随时 | **否** | 原地更新，所有持有者都能看到，与现状一致 |
| `provider_meta` | provider 凭据与 id，结构见表下 | 随正文一起写定 | 是 | 必须 COW |

`state` 收纳的字段：
- 翻译结果；
- review 的 `applied`、merge 的 `isLatest`；
- 卡片 status；
- compact 的 attempts；
- 并行组标注；
- AskUserQuestion 的作答状态。

`provider_meta` 的形状：

```
{source, anthropic?:{signature}, openai?:{itemId, encryptedContent, phase}, gemini?:{thoughtSignature, stepId}}
```

- `provider_meta.source` 就是现在的 `signatureSource`（`shared/agent-protocol/reasoning-source.ts`）。
- text 块也可以带 `provider_meta`，用来补上 §1.3 E 的缺口：Responses 的 message item id 与 `phase`，以及 Gemini text part 的签名。
- 信封上的 `messageUuid` 退回为「provider 响应 id」，不再参与 item id 的重放。

### 3.5 块的生命周期 `phase`

```
open ──▶ done
  ├──▶ aborted   用户中断、进程退出：已产生的内容保留
  └──▶ failed    compact / 段压缩失败
```

tool_use 不经过 `open`：输入完整时直接以 `done` 落行。输入解析失败（broken）则以 `failed` 落行。

现在「进行中」有三种分散的表示，都由这一个字段统一：
- 流式中的 text / reasoning：`completed:false`；
- compact 进行中：`compact_pending` 生成列；
- 段压缩进行中：标记块上的 status。

由此带来的规则：

- **启动恢复只扫 `phase='open'` 的部分索引。**
  - 流式块改为 `aborted`；
  - compact 和段压缩改为 `failed`。
  - 这取代了 `recoverStaleCompactingMessages`，以及冷启动时对 `completed:false` 的清理。
- **fork 不共享任何含 `open` 块的信封。**
  - 这取代了现在「排除进行中 compact」的特例。
  - 同时修复一个问题：流式中的 partial 行可能先被 fork 共享，之后仍被原地追加（`narrator-persistence.ts:2208-2333`；`narrator-session-state.ts:895-901`）。
- **模型历史只读 `done` 和 `aborted` 的块。** `open` 块不进模型历史，也不触发 revision（§4.5）。
- **`aborted` 块的重放规则**：
  - reasoning 不重放，因为凭据不完整；
  - text 照常重放，因为用户已经看到过它。
  - 现在这条规则在两条路径上结论不同：运行中会重放 `completed:false` 的文本，冷启动后却把它删掉（`narrator-output-recovery.ts:16-31,72-97`）。

### 3.6 轮、步、attempt：写入时盖章

| 概念 | 定义 | 存放位置 |
|---|---|---|
| 轮（turn） | 一次运行：从叙述者被一次输入唤醒，到回到 idle 或等待用户为止，即 orchestrator 一个 active run 的生命周期（`orchestrator.ts:813`）。期间的插话、注入、自动续跑、错误卡都属于该轮；idle 时写入的卡片不属于任何轮 | 信封 `turn_id` |
| 步（step） | 一次 provider 响应，即一个 assistant 信封；同一步内的重试 attempt 共用这个信封 | 信封 `step_index`（轮内序号） |
| attempt | 同一步内的一次 provider 请求；被丢弃的 attempt，其块整体删除 | 块 `attempt_id` |
| 并行组 | loop 的 `groupToolExecutions` 给出的执行分组 | tool_use 块的 `state.execGroup` |
| 工具配对 | tool_result 块与 tool_use 块的对应关系 | tool_result 块 payload 的 `toolUseBlockId`；账本的 `block_id` |

- `turn_id` 由 orchestrator 在一次运行开始时生成，随持久化上下文传给 store。
- 前端的 recency（最近两轮）、流式交接、工具排队都改为读这些字段，原来的三套定义合成一套（§8.3）。
- 子代理在自己的叙述者上有自己的轮。

### 3.7 工具：tool_use 块、账本、tool_result 块

**tool_use 块**：模型发出的调用。
- `payload.input` 是**重放给模型的输入**，所有 provider 都只读这一份。
- 执行时输入被重定向或修复的，写成这个块的新 revision。现在的做法有三处不一致：
  - `overwriteToolCallInput` 会同时改两处，但 AskUserQuestion 例外（`narrator-persistence.ts:4093-4117`）；
  - WS `update_timeout` 只改账本（`narrator-ws.ts:2640-2657`）。
- AskUserQuestion 的作答字段移到 `state`，投影时不再需要剥离（`question-model-input.ts:43-84`）。
- Gemini 的 thoughtSignature 放进 `provider_meta`，不再嵌入 `input_json`。

**账本行**：
- 每个 attempt 一行，新增 `block_id`，指向它执行的那个 tool_use 块。
- 这样，provider 给出重复的 toolUseId 时也不再有歧义；`#dupN` 和 `resolveToolCallWriteId` 里的猜测都可以去掉。

**tool_result 块**（新增）：
- 时机：当被选为模型可见的那次 attempt 完成时写入。
- 位置：追加在**同一个 assistant 信封**末尾，按 tool_use 的调用顺序排列，而不是按完成顺序。
- payload 只存模型可见、但账本里没有的东西：
  - `toolCallId`：被采用的那次 attempt；
  - `images`：工具结果里的图片，按上传文件落盘，块里只存引用（现在只存在内存里）；
  - `appendix`：追加给模型的附言，例如 turn nudge（现在只存在内存里，`loop.ts:7275-7276`）。
- 文本输出仍然只放在账本的 `output_json`，不复制一份。账本的 `input_json/output_json` 已经约 1.5GB（`execution-log-service.ts:4-6`）。
- 投影时，tool_result 的文本 = `outputToText(账本.output_json)` + `appendix`；图片按 `images` 里的引用从磁盘恢复。

**效果**：
- 从 DB 重建出的工具结果，在顺序、图片、附言上都和实时发送的一致。
- broken 调用表示为 `phase=failed` 的 tool_use 块，实时路径和重建路径按同一条规则处理。

## 4. 数据模型（schema 草案）

### 4.1 `narrator_blocks`（新表）

```
narrator_blocks
  id                text  PK                       §3.2
  message_id        text  NOT NULL → narrator_messages.id ON DELETE CASCADE
  ordinal           integer NOT NULL               信封内的顺序
  kind              text  NOT NULL                 §3.3
  subtype           text                           context/marker/notice 的子类型（原 type 值）
  phase             text  NOT NULL DEFAULT 'done'  open | done | aborted | failed
  revision          integer NOT NULL DEFAULT 0     正文版本；流式 checkpoint 与编辑时递增
  payload           text(json) NOT NULL
  model_text        text                           冻结的模型可见文本（非原生 kind）
  search_text       text                           冻结的可搜索文本（content_text 派生与块级 FTS 的来源）
  state             text(json)                     非模型可见的可变元数据
  provider_meta     text(json)                     凭据与 provider 侧 id，带 source
  wire_id           text                           provider 侧 id
  attempt_id        text                           产生它的 provider attempt
  origin_block_id   text                           COW lineage
  original_payload  text(json)                     首次编辑前的正文（替代 original_content_json）
  model_chars       integer NOT NULL DEFAULT 0     计入上下文的字符数
  byte_size         integer NOT NULL DEFAULT 0     payload + model_text + provider_meta 的字节数
  created_at        text  NOT NULL
  completed_at      text
```

索引：

| 索引 | 用途 |
|---|---|
| `UNIQUE (message_id, ordinal)` | 读取信封的块，同时约束顺序 |
| `(phase) WHERE phase = 'open'` | 启动恢复；判断 fork 是否可共享。替代 `idx_messages_compact_pending` |
| `(origin_block_id) WHERE origin_block_id IS NOT NULL` | 按 lineage 解析永久链接、引用、评论 |
| `(subtype) WHERE subtype IS NOT NULL` | 按种类查找，例如 merge summary、compact 标记 |
| `(wire_id) WHERE kind IN ('tool_use','tool_result')` | 按 toolUseId 查找工具详情（兼容旧 API） |
| `(kind) WHERE kind = 'image'` | 孤儿上传回收，替代 `LIKE '%"type":"image"%'` |

说明：
- **不设 `narrator_id` 列。** 块属于哪个叙述者，一律经 refs → 信封 判定。块的作者就是信封的作者。
- **`ordinal` 用稠密整数。** 极少数需要在中间插入的情况（例如 provider 按 `outputIndex` 乱序到达），在同一事务里对这一个信封重新编号即可。单个信封通常只有几十块，开销可以忽略。

### 4.2 `narrator_messages` 新增列（只做 `ALTER … ADD`）

| 列 | 含义 |
|---|---|
| `body_format integer NOT NULL DEFAULT 1` | 1 = `content_json` 是权威；2 = 块行是权威 |
| `turn_id text` | §3.6 |
| `step_index integer` | §3.6 |
| `author_narrator_id text` | 不可变的作者，COW 时保留；为 null 时视为 `narrator_id`（旧行）。sender 投影和归属展示读这一列（§6.3） |

已有列在 `body_format = 2` 下的语义：

| 列 | 语义 |
|---|---|
| `content_json` | P1–P2 期间写入由块派生的兼容投影（供回退开关使用）；P3 起新行写 `'[]'`（§10.4） |
| `content_text` | 由块的 `search_text` 按统一规则派生（§4.6）；只在块封闭、信封封口、编辑时重算，不在每个 checkpoint 重算 |
| `context_chars_json` | 由块的 `model_chars` 和 kind 派生，context stats 触发器不变 |
| `original_content_json` / `compact_pending` | 只对 `body_format = 1` 有意义 |
| `body_revision` | 块触发器同样会 bump 它（§4.5） |

### 4.3 `narrator_tool_calls` 新增列

- `block_id text → narrator_blocks.id`：这次 attempt 执行的 tool_use 块，旧行为 null。COW 克隆账本行时，`block_id` 重映射到克隆后的块。

### 4.4 可选：`narrator_turns`（P2 视需要）

`id, narrator_id, status, trigger_message_id, started_at, ended_at, stop_reason, usage 汇总`。

这张表用来支撑「本轮用了多少时间、调用了几次工具」这类轮级 UI，以及 External API 的轮列表。只有 `turn_id` 列已经足够完成本设计的其余部分，所以这张表不是前置条件。

### 4.5 revision 触发器契约 v2

块表触发器，SQLite 与 PG 各一套。动作都是：bump 所属信封的 `body_revision`，并 bump 所有持有者叙述者的 `model_history_revision`（与现有 `holderUpdate` 相同）。触发条件：

| 事件 | 条件 |
|---|---|
| INSERT | `NEW.phase <> 'open'` |
| DELETE | `OLD.phase <> 'open'` |
| UPDATE OF `payload, model_text, provider_meta, kind, subtype, ordinal, phase, message_id` | 排除 open → open 的更新（即流式 checkpoint） |

要点：
- **`state` 的变化不触发。** 这就把「UI 状态让模型历史失效」从根本上去掉了。
- **流式 checkpoint 不触发。** 块在 open → done 时触发一次。
- **契约版本升级。** 契约 id 改为 `model-history-v2`，运行时核对的触发器数量随之更新。
- **由生成器产出。** 生成器（`scripts/lib/generated-model-history-triggers.ts`）需要学会产出 v2，以「带版本的重装迁移」形式交付。finalizer 拒绝直接重建触发器表时，报错信息也指向这条路：`"Model-history trigger table rebuild requires a versioned reinstall migration"`（`scripts/finalize-sqlite-resource-migration.ts:744-751`）。
- **禁止手改迁移文件。** 不得手改 `drizzle/`，也不重复 0206 那种在生成结果后人工追加 SQL 的做法。
- **增量复用改为按块。** `model-history-delta` 仍按信封的 `body_revision` 判断哪些信封变了，然后只重读这些信封的块。

### 4.6 派生字段由一个函数生成

`deriveEnvelope(role, blocks, adoptedToolCalls)` 输出三项：`content_text`、`context_chars_json`，以及 P1–P2 期间的兼容 `content_json`。

**调用方式**
- 由 store 在同一事务内调用。
- 时机：块封闭、信封封口、编辑或删除之后。

**派生规则**
- 第一版逐条复刻现状，包括 user 行的附件提示和控制行的哨兵字符串，所以 External API `detail=text` 的输出不变（`external-resource-service.ts:1559-1577`）。
- 只统一已知的不一致，即 assistant 行拼接用 `\n` 还是 `\n\n`。

**以哨兵字符串做查询条件的地方，改为查块**
- `eq(contentText,"[Compacting]")` 改为按块的 `subtype` / `phase` 查询。
- `isNotNull(contentText)` 改为「存在可见文本块」。

**FTS 触发器**
- update 触发器改为 `AFTER UPDATE OF content_text`。
- FTS 触发器由运行期的 `ensureFts` 安装（`server/db/fts.ts:372-393`），不在 drizzle 迁移里。改动时要给触发器定义加版本号，并做 DROP + CREATE，因为现在用的是 `CREATE TRIGGER IF NOT EXISTS`，不会替换已有定义。

### 4.7 PG

- `narrator_blocks` 经 `scripts/generate-postgres-schema.ts` 转译生成，JSON 列用 `jsonText`。
- 不使用生成列；`phase` 是普通列，不受 PG 生成列表达式白名单的限制。
- PG 的块触发器用 AFTER 触发器，更新父信封和持有者；增量迁移通过 `db:generate:pg` 生成。
- 回填游标在 PG 上用 `(created_at, id)` keyset，因为 PG 没有 rowid。
- PG 的叙述者写路径尚未完成（目前只有 5 个持久化方法有 PG 实现，`narrator-persistence.ts:4466-4499`），所以本设计只要求 PG 的 schema 和迁移可以生成、可以检查，不要求 PG 写路径可用。

## 5. 写入路径

### 5.1 唯一写入层

**现状**：直接写 `narrator_messages` 的非测试文件有 22 个。

**目标**：收口成一个模块（暂名 `server/services/narrator-store/`）。只有它可以写以下数据：
- 信封的正文列；
- `narrator_blocks`；
- refs；
- 账本中与块关联的列。

接口草案：

```ts
interface NarratorStore {
	// 信封
	appendEnvelope(tx, env: NewEnvelope, blocks: NewBlock[], ref: RefPlacement): MessageId;
	sealEnvelope(tx, messageId, usage: EnvelopeUsage): void;
	// 流式
	checkpointBlock(tx, messageId, block: BlockCheckpoint): void; // 首次插入（open），之后只更新这一行
	closeBlock(tx, blockId, phase: "done" | "aborted", final?: BlockPatch): void;
	discardAttempt(tx, messageId, attemptId): void;
	// 工具
	appendToolUse(tx, messageId, block: NewToolUse, ledger: NewToolCall): BlockId;
	appendToolResult(tx, toolUseBlockId, toolCallId, extras: ToolResultExtras): BlockId;
	// 修改
	editBlock(tx, holderNarratorId, blockId, patch): BlockId; // 共享则 COW，返回新 id
	deleteBlocks(tx, holderNarratorId, blockIds: BlockId[]): void; // 共享则 COW
	patchState(tx, blockId, statePatch): void; // 不 bump revision，不 COW
	// 读取
	loadBlocks(messageIds): Map<MessageId, Block[]>; // 两种 body_format 都认
}
```

**约束**
- 所有方法都是同步的，因为 bun:sqlite 的事务里不能 await（`db/transaction-atomicity-contract.test.ts`）。
- 派生字段在方法内部重算。
- 广播在事务提交之后进行。

**P0 先收口，再改存储**
- P0 就把 22 个写入文件改为走这个接口，此时底层仍是 `content_json`。这样 P1 更换存储只发生在 store 内部。
- 收口的同时修掉现有的非原子写入：
  - tool_loaded / tool_unloaded、/bash 的 ref 单独开事务追加（`narrator-persistence.ts:654-667`）；
  - `persistAssistantMessage` 在消息事务之后逐个 await 插入账本行（`2044-2081`）；
  - `overwriteToolCallInput` 分两次 await 读改写（`4073-4119`）。

### 5.2 流式：开块 → 单行 checkpoint → 封块

1. **step 开始。** 分配信封 id，同一步内的各 attempt 共用它；同时分配 attempt id。此时不写库。
2. **lane 打开。** 分配块 id，广播 `block_opened {messageId, blockId, kind, ordinal, attemptId}`。不写库。
3. **delta。** 广播 `block_delta {blockId, revision, textOffset, text}`。
   - 只带增量。
   - 按帧合并（约 16–50ms），遵守 CLAUDE.md 关于 WS 高频输出要合并、节流的规则。
4. **checkpoint。**
   - 首次：插入信封（如果还不存在）和块，块的 phase 为 open。
   - 之后：只 UPDATE 这一块的 `payload`、`revision`。
   - 广播 `block_checkpoint {blockId, revision}`，不再广播整条消息。
5. **lane 完成。** `closeBlock(done)`：
   - 触发一次 revision bump；
   - 重算该信封的 `content_text` 和 `context_chars_json`。
6. **tool_use 输入完整。** 在一个事务里插入 tool_use 块（phase 为 done）和账本行（带 `block_id`）。
7. **工具结果。** `appendToolResult` 与账本的结果写入放在同一个事务里。
8. **step 结束。** `sealEnvelope` 补写用量和 `messageUuid`。
   - 不重写块，也不需要 COW。
   - 现在 `completeAssistantOutput` 为了写 `completed:true` 要走一次 COW（`narrator-output-recovery.ts:46-63`），这一步随之消失。

**写入代价**
- SQLite 不能部分更新一个 TEXT 值，所以每个 checkpoint 仍要重写该块的 `payload`。不过开销只与这一块的长度成正比，不再与整条消息的长度成正比。
- open 块不触发 revision；FTS 只在块封闭时随 `content_text` 更新一次。

### 5.3 丢弃 attempt、中断、错误

| 场景 | 现在 | 改为 |
|---|---|---|
| `attempt_discarded` | 按 `api_request_start` 时记录的块数基线截断数组。基线是数组下标，而块又按 `outputIndex` 排序插入，可能切错（推测，`narrator-event-handler.ts:1201-1211` 与 `output-content.ts:67-81`） | `discardAttempt(messageId, attemptId)`：删除该 attempt 的块，但保留工具已执行过的 tool_use 块（以账本状态判断） |
| 用户中断 | 内容 flush 后保持 `completed:false`；之后运行中会重放，冷启动时又会删除 | open 块改为 `aborted`；重放规则见 §3.5；正在执行的工具照旧置 fail |
| 终止性错误 | `finalizeOrCleanupPartialMessage` 的三个分支在数组上做过滤 | 同样三个分支，改为按 kind / phase / 账本状态删除块行 |
| 冷启动 | 扫 compact 部分索引，并删除最后一条 assistant 中 `completed:false` 的块 | 只扫 `phase='open'` 部分索引，一次处理完 |

### 5.4 UI 状态写入

`patchState` 只做单行 UPDATE 加广播 `block_state`：
- 不 bump revision；
- 不 COW；
- 不触发 FTS。

下列现有写入点都迁到 `patchState`：

| 写入点 | 现在的问题 |
|---|---|
| `patchReasoningTranslation` | 没走 COW，却 bump 模型历史 |
| review 的 `markReviewFeedbackApplied` / `release` | 同上 |
| merge summary 的 `isLatest` | 之前要先 `LIKE` 全表扫描定位（定位改为按 `subtype` 查索引） |
| ask_in_passing 改为 resolved | |
| subagent_recovery 卡片 status | |
| compact 的 lifecycle（mode、attempts、error） | |

compact finalize 的 CAS 改为：

```sql
WHERE id = ? AND phase = 'open' AND revision = ?
```

即比较块的 `phase` 和 `revision` 列，不再对 JSON 路径做 `json_extract`。

## 6. 共享、fork 与 COW

### 6.1 不变的部分

- refs 仍以信封为粒度。
- fork 仍用 `INSERT…SELECT` 复制 ref（`narrator-service.ts:2959-2995`）。
- 惰性回填（`narrator-refs-backfill.ts`）、seq 的分配与移位语义都不变。

块只跟着信封走，fork 本身的成本不变。

### 6.2 不可变规则

1. **`done` 块的正文不可变**，这里的正文指 `payload` / `model_text` / `provider_meta`。在被共享的信封上做编辑或删除，一律经 COW。
2. **共享判定要补上惰性 fork 的缺口。**
   - 现在只数 refs。惰性 fork 的子叙述者对祖先区间还没有 ref，所以父叙述者原地修改旧消息时，子叙述者回填后会看到修改后的版本（推测）。
   - 新判定：refs 数大于 1，或者存在一个惰性继承者，其待回填区间覆盖该 ref 的 seq。后者经 `refs_inherited_from` 的部分索引查询，是常数级开销。
3. **含 `open` 块的信封不参与共享**（§3.5）。

### 6.3 一个 COW 实现，作者与属主分开

`cowEnvelope(tx, messageId, holderNarratorId, mutate)` 取代现有的五套实现。步骤如下：

1. 复制信封。
   - 新行的 `narrator_id` 为持有者，用于属主与回收。
   - `author_narrator_id` 保留原作者。
2. 复制全部块：分配新 id，`origin_block_id` 指回原块，然后应用 `mutate`。
3. 克隆账本行，沿用现有语义：
   - `executionOriginToolCallId` 指回原行；
   - 克隆行不可执行；
   - `block_id` 重映射到新块。
4. 把持有者的 ref 改指新信封。
5. 在同一事务里更新 mailbox 回执（`updateRecipientMessageRef`）。
6. 预算沿用现有上限（1000 个持有者、8MiB），改按块字节计算。

**为什么要把作者和属主分开**

现在几处 COW 对新行的 `narrator_id` 取值不同：
- 一处设为接收者（`narrator-persistence.ts:390-491`）；
- 一处保留原值（`revert-history-commit.ts:856-857`："historical authorship does NOT move to the fork owner"）。

而 sender 投影会读消息作者叙述者的标题（`model-history-query.ts:161-167`）。作者一变，模型看到的署名和请求字节都会跟着变。拆成两列之后：
- 属主可以随 COW 和「删除叙述者时转交」变化；
- 作者永远不变。

### 6.4 compact、段压缩、清空上下文

**compact 标记**
- 形态：role=system 的信封，内含一个 `marker` 块（subtype `compact`）。
- 生命周期：phase 从 open 变为 done 或 failed；attempts 记在 `state` 里。
- `refs.is_compact` 和 `narrators.context_summary` 的语义都不变。
- `compact_pending` 生成列及其部分索引，由块表的 `phase` 部分索引取代。

**段压缩标记**
- 现在是 role=user、没有 origin 的行。sender 投影把它当作人类消息（`narrator-persistence.ts:4166-4182`；`sender-projection.ts:80-81`，推测）。压缩进行中，模型还会看到 `[Segment compacting]`。
- 改为：
  - role 保持 user，以保住协议位置；`origin=system`。
  - `model_text` 明确标注这是摘要，例如以 `[Earlier conversation summarized]` 开头。
  - phase 为 open 时对模型不可见。

**计划 / 清空上下文**：都是 `marker` 块的 subtype，`refs.is_compact` 的写法不变。

**file_history_checkpoint**
- 仍用 `segment_compact_id` 指向自身来隐藏（`narrator-messages.ts:1662-1706`）。
- 块形态是 `marker` / `file_history_checkpoint`；被克隆的账本行带上 `block_id`。

### 6.5 子代理、提取、注入、mailbox

| 对象 | 设计 |
|---|---|
| 子代理消息 | 关联方式不变：`parentToolUseId` 是父叙述者那边的 wire id，子代理与 Agent 调用的精确关联仍用 `narrators.originToolCallId` |
| 子代理提升为 primary（extract） | 深拷贝信封、块和账本行（新 id，带 lineage）。现在不复制账本行，这些 tool_use 在模型历史里会被丢掉（`subagent-extract.ts`，推测） |
| 注入 | `deliverInjection` 写一个 `context` 块（subtype 为 `system_injection` 等），带 `model_text`。伴随块（`subagent_messages`、`background_agents_completed`）写成不可见的 `notice` 块 |
| mailbox | `recipientMessageId` 的预留与 materialize 保持在信封粒度，不变 |
| 问题补充 | 读取「第一个 subtype 为 `system_injection` 的 context 块」，不再读 `$[0]/$[1].body.supplement` |

## 7. 读取路径与 provider 投影

### 7.1 规范历史视图

```ts
interface HistoryView {
	envelopes: Array<{
		id: string;
		role: "user" | "assistant" | "sys";
		origin: Origin;
		author: { narratorId: string; title: string; username?: string };
		parentToolUseId: string | null;
		turnId: string | null;
		stepIndex: number | null;
		provider: string | null;
		model: string | null;
		blocks: CanonicalBlock[]; // 只含 done / aborted 块，按 ordinal 排序
	}>;
	adoptedToolCalls: Map<string, { status: "success" | "fail"; output: ToolOutput }>;
}
```

**加载器**
- 沿用模型历史 worker 和 `model_history_revision` 源缓存（`model-history-source.ts:334-445`）。
- 读取顺序：refs → 信封 → 块 → 被采用的账本行。
- `body_format = 1` 的信封在 worker 里解码（§10.2）。

**投影前的通用步骤**
- 附件提示、file_reference 展开、plan body 引用、citation 剥离、sender 标记、tool id 的 wire-safe 化（`server/lib/agent/index.ts:65-111`）。
- 改为在规范块上执行一次，不再在各 provider 的 builder 里重复做。

### 7.2 每个 provider 是一个投影函数

```ts
projectHistory(view: HistoryView, target: ProjectionTarget): ProviderWireHistory
```

`ProjectionTarget` 包含以下字段：

| 字段 | 取值 / 含义 |
|---|---|
| 协议 | `anthropic-messages`、`openai-responses`、`completions`、`gemini`、`gemini-interactions`、`plugin` |
| `sourceKey` | 现有各 provider 的 key，例如 `codex`、`nug:<p>:<channel>` |
| `strict` | 由**裸 model 名**计算一次，所有路径共用 |
| `officialApi` | 是否官方 API |
| 能力 | 是否支持图片、是否支持对话中途的 system |

投影只决定各 kind 落到线上的形态。现有语义全部保留，只把分散在各 builder 里的判断集中起来：

| kind | Anthropic | Responses | Chat Completions | Gemini |
|---|---|---|---|---|
| `text` | text | message 的 `output_text`；同源时带 `provider_meta.openai.itemId` / `phase` | content | text part（同源时带签名） |
| `reasoning` | thinking + signature（strict 时仅同源） | reasoning item（同源时带 `encrypted_content`）；否则降级或丢弃 | `reasoning_content`（仅非 strict） | thought part（同源时带签名） |
| `tool_use` | tool_use | function_call（`call_id=wire_id`，同源时带 item id） | tool_calls | functionCall（同源时带 thoughtSignature） |
| `tool_result` | 下一条 user 消息里的 tool_result，含图片 | function_call_output，图片随后作为 input_image | role=tool 消息 | functionResponse |
| `server_tool` | user 文本摘要 | web_search_call / image_generation_call | user 文本摘要 | 跳过 |
| `image` | image（从磁盘恢复） | input_image | 附件提示 | 附件提示 |
| 带 `model_text` 的块 | user / system 文本 | user `input_text` | user | user |

### 7.3 实时追加与重建合为一条路径

**做法**
- loop 持有一个 `HistoryView`。store 持久化块之后，把**同一个规范块对象**追加进 view。
- 每次发请求都调用 `projectHistory`。
- provider 接口删去 `pushAssistantTurn`、`pushUserTurn`、`formatToolResult`，只剩 `project` 和 `chat(wire)`。

**性能与缓存**
- 按信封记忆投影结果，键为 `(envelopeId, body_revision, targetFingerprint)`。每次请求只投影新增或变化的信封。
- 前缀字节天然稳定，有利于 prompt cache。

**Codex WS 的增量续写**
- 前提条件：新请求必须是「上次输入 + 上游返回的原始 item」的逐项前缀（`codex-websocket.ts:255-267`）。
- 要求：刚落库的 Responses 块经投影后，必须还原出与上游原始 item 相同的 JSON。这也规定了 `provider_meta.openai` 必须存哪些字段。
- 推测：现在 `pushAssistantTurn` 重新序列化出的 item 与上游原始 item 不一致时，会退化成全量 `response.create`；统一投影之后，命中率只会更高。

### 7.4 跨 provider 的携带策略集中在一处

`carryPolicy(block, target)` 返回三种结果之一：`native`、`degrade-to-text`、`drop`。

第一版逐条编码现有规则：
- Anthropic strict 模式下，外源签名整块丢弃；relay 模式保留。
- Responses 同源时回放 encrypted reasoning，否则降级为文本或丢弃。
- Gemini 的 thought 文本总是回放，签名只在同源时携带。
- 插件只携带同源的 continuation。

同时修正几处现状：
- strict 只由裸 model 名计算一次（§1.3 D）。
- message item id、Interactions 的 `stepId` 只在同源时回放（现在不检查来源，`openai-provider.ts:3910-3919`；`gemini-interactions-provider.ts:697-754`）。
- image_generation 结果的读盘挪出主线程：现在投影时在主线程 `readFileSync`（`openai-provider.ts:3991`），改为在加载器 worker 里预取。

### 7.5 字节稳定性约定

- **同一版本内**：「从 DB 重建 == 实时发送」由构造保证，并由每个 provider 的等价测试兜底（§13）。
- **跨越投影切换的那次发布**：允许一次性的 prompt cache 未命中。这与 compact 之后换 conversationId 重新起缓存（`narrator-session-state.ts:471-478`）的代价同级，在发布说明里写明即可。

## 8. 前端与传输

### 8.1 页面 API

`GET /:id/pretext-document` 分两步演进：

- **过渡形态（P1）**
  - 仍返回 `contentJson`，但每个元素附带 `blockId`、`blockRevision`、`phase`，属于加字段的扩展。
  - 前端可以先把身份换成块 id，渲染层暂时不动。
- **终态（P2）**
  - 返回规范化结构：`{messages: EnvelopeDTO[], blocks: BlockDTO[], ...}`，块按 `messageId + ordinal` 组织。
  - 工具 IO 截断仍按块的叶子做，每个叶子 8KiB（`shared/pretext-layout/tool-io-projection.ts:67`）。
  - 详情接口改为按块寻址，例如 `GET /:id/blocks/:blockId`；旧的 `/tool-calls/:toolUseId` 保留为兼容接口。

### 8.2 WS 协议 v2

| 事件 | 载荷 | 取代 |
|---|---|---|
| `block_opened` | `messageId, blockId, kind, ordinal, attemptId` | 前端合成 `streaming:text:<i>` 一类的 id |
| `block_delta` | `blockId, revision, textOffset, text`（按帧合并） | `content_block_delta` |
| `block_checkpoint` / `block_closed` | `blockId, revision, phase` | 每个 checkpoint 一次的整条 `message_updated` |
| `block_state` | `blockId, statePatch` | 卡片状态、翻译等整条消息重发 |
| `blocks_deleted` | `blockIds` | 只接受严格前缀截断、否则整窗重载的限制（`vlist-message-replace.ts`） |
| `message_upserted` / `message_sealed` | 信封元数据 | `message`、`message_updated` 中的信封部分 |

- `streaming_snapshot` 保留，用于重连，但内容改为「open 块列表」，以块 id 为键。
- catch-up 游标仍以信封为粒度（`shared/narrator-catch-up.ts`），补发的信封携带完整的块。

### 8.3 前端删除的推导与补丁

| 现在 | 改为 |
|---|---|
| `__streaming__` 合成消息、`projectStreamingMessage` / `projectStreamingDocument`、supersede 启发式、`dropPersistedStreamingTools`、`finalizedRef`（`streaming-handoff.ts`；`streaming-block-supersede.ts`；`useVListStreamingMessage.ts`） | 流式块就是 store 里 phase 为 open 的块，以 id 为键。交接这一步不再存在 |
| `#dupN`、`dedupeSuffix`、`tool-<toolUseId>` 行 key | `blk-<blockId>` |
| 从 key 字符串用 `-b(\d+)` 反解下标；API 用 `(messageId, blockIndex)` 寻址 | 块 id |
| 三套「轮」的定义；前端重算并行分组 | 读 `turn_id`、`state.execGroup` |
| live patch 按 toolUseId 写所有出现位置（`vlist-live-patch.ts:222-231`） | 按账本 `block_id` 精确写入 |

客户端仍然要做的只剩展示层的事：LOD 折叠、reasoning 步骤解析、测量。

## 9. 其它消费方的迁移

| 消费方 | 现在依赖 | 改为 | 阶段 |
|---|---|---|---|
| 模型历史加载与各 provider builder | `content_json` + 账本；各 builder 自行解析 | 规范视图 + 投影（§7） | P0 改为吃规范块；P2 切换到块表 |
| 展示页 worker / WS / catch-up | 整数组读取、按字节预检、`enrichToolUseBlocks` | 按块读取、按块计字节；过渡期仍组装数组 | P1–P2 |
| FTS / 搜索 / Recall | `content_text` | 保留信封级 `content_text`（集中派生）；块级 FTS 可选 | P1；块级 FTS 在 P3 |
| compact / 段压缩 | `$[0]`、生成列、JSON CAS、哨兵字符串查询 | `marker` 块 + `phase` + `state` | P1 |
| 文件回退 / 块删除 / 回滚到块 | 原始字节、摘要 + 出现序号身份、下标 | 块 id + `revision`；`RewriteWorker` 不再解析 JSON 片段 | P2 |
| 编辑与恢复 | 整数组快照 `original_content_json` | 块的 `original_payload`；publication 读 `COALESCE(original_payload, payload)` | P1 |
| merge summary / 孤儿上传回收 | `LIKE` 全表扫描 | 按 `subtype` / kind 查部分索引 | P2 |
| External API v1 | `content_text`、`json_each` 抽推理 | 读块；契约不变，可加 `blockId` 字段 | P2 |
| project.db 同步 / 导入 | `content_json` 列（格式固定，只能扩展） | 导出时由块组装 `content_json`；导入时解码；之后可在 project.db 中新增块表 | P2 |
| transcript 导出 / session interchange | 整数组 | 从块组装 | P2 |
| 问题补充 / parent-injection-queue | `$[0]`、`$[1]` 路径 | 按 subtype 取块 | P1 |
| tool continuation 恢复排序 | `toolUseOrder`，即数组下标 | 块的 `ordinal` | P1 |
| context 构成 | `context_chars_json` | 不变（派生）；可选扩展为按块构成 | P1；按块构成在 P3 |
| 插件 / IM 网关 / 标题 / 通知 | 只读 `content_text` 或元数据 | 不变 | — |
| 测试（约 350 个文件直接使用 contentJson） | 原生 INSERT 造数据 | fixture helper 走 store；过渡期 decode 保证旧 fixture 仍可读 | 贯穿各期 |

## 10. 迁移与兼容

### 10.1 按信封共存两种格式

- 每个信封自带 `body_format`，读取层（`loadBlocks`、两个 worker）对两种格式都认。没有全库切换日。
- 任何**修改** `body_format = 1` 信封的操作，都先在同一个事务里把它物化：解码，写入块行，改为 `body_format = 2`。然后再按块 id 执行修改。
- 前置条件：P0 已经把所有直接改 `content_json` 的旁路写入收口进 store；否则下标身份会在物化之前就被改乱。

### 10.2 旧数据的确定性解码 `decodeLegacyContent`

| 旧形态 | 解码结果 |
|---|---|
| 第 `i` 个元素 | 块 id `${messageId}:${i}`，`ordinal = i` |
| text + 无 modelText 的旧注入块配对 | 合并为一个 `context` 块，取配对中第一个物理下标作为 id |
| `$[0]` 的 compact 块 | `marker` / `compact`。status 映射为 phase；attempts、mode、error 放进 `state` |
| 旧 `thinking {thinking, signature}` | `reasoning`，签名放进 `provider_meta.anthropic` |
| `providerMetadata` / `signatureSource` / `thoughtSignature` | 并入 `provider_meta` |
| 账本里被采用的 attempt | 合成 `tool_result` 块，id 为 `${messageId}:r:${tool_use 的下标}`，排在全部 tool_use 之后 |
| `completed:false` | `aborted` |
| `translatedText`、`applied`、`isLatest`、卡片 status | 移入 `state` |

- 解码是纯函数，放在 `shared/`。读取路径、回填作业、project.db 导入都用它。
- 它的测试用例就是现有的各种块形态，包括 `injection-row-placement`、`provider-content-boundaries` 等测试里的数据。

### 10.3 后台回填作业

**为什么不能放在迁移 SQL 或启动回填里**
- finalizer 只接受白名单内的 DDL，迁移里不能做数据回填（`scripts/finalize-sqlite-resource-migration.ts:604-852`）。
- CLAUDE.md 禁止在主线程做大扫描。
- 启动回填（`db/data-backfills.ts`）会阻塞服务就绪。

**作法**：照持久游标作业的先例来做，即 `resource-protection-index.ts:603-633,979-993`，以及已删除的 `usage_token_repair_jobs`（`git show 85b2a46a:server/services/usage-token-repair-runner.ts`）。

| 方面 | 规则 |
|---|---|
| 进度 | 持久游标行，记录 `cursor` 和 `complete`。SQLite 按 `narrator_messages.rowid` 推进，PG 按 `(created_at, id)` |
| 单片 | 每片不超过 1 秒或 100 个信封 |
| 解码位置 | 在只读 db-worker 中解码（`lib/db-worker/pool.ts`） |
| 写入 | 回到主线程，用一个短的同步事务写入，带 `WHERE body_format = 1` 守卫：幂等，且能与同时发生的物化安全并发 |
| 节奏 | 片与片之间 `setTimeout(0)`；由定时器重入；重启后从游标继续 |
| 校验 | 对 P1 双写期间产生的 `body_format = 2` 信封，比对「块组装出的数组」与 `content_json`，不一致的写入诊断表。双写一致性因此有了持续的证据 |
| 进度展示 | 在存储页显示进度，可以暂停 |

### 10.4 `content_json` 的终态

| 阶段 | `body_format = 2` 信封的 `content_json` | 回退方式 |
|---|---|---|
| P1–P2 | 写入完整的派生投影 | 关闭 `narratorBlocks.read` 开关，读取方回到 `content_json` |
| P3 | 新行写 `'[]'`；旧行在回填和校验完成后，由第二个作业清空 | 需要先运行「重组作业」，由块回写 `content_json` |

- 这一列本身不删除。重建带触发器的表需要一次带版本的重装迁移，收益不值得这个代价。
- 清空释放出的空间，只能在管理员维护窗口里用 `/api/storage/database/vacuum` 回收。

### 10.5 对外格式

- **project.db**：保留 `content_json` 列，同步时由块组装（`project-archive/manifest.ts:5-7`，格式固定，只能扩展）。旧归档导入时走解码。
- **External API v1**：契约不变，内部实现改为读块；可以追加 `blockId` 字段。
- **session interchange / transcript 导出**：由块组装，输出格式不变。

### 10.6 开关

| 开关 | 作用 | 引入阶段 |
|---|---|---|
| `narratorBlocks.write` | store 写块表（双写） | P1 |
| `narratorBlocks.read` | 读取方读块表 | P2 |
| `narratorBlocks.wsV2` | WS 协议 v2 | P1 |
| `narratorBlocks.compactJson` | 新行的 `content_json` 写 `'[]'` | P3 |

每个开关都可以单独关闭。P3 之前，关闭 `narratorBlocks.read` 就完全回到现状。

## 11. 实施阶段

### P0 收口（不建新表）

**交付**
1. `NarratorStore` 成为唯一写入层，22 个写入文件全部迁入。顺带修掉 §5.1 列出的非原子写入。
2. `shared/narrator-blocks/`：规范块的 TS 类型和 Zod schema，以及 `decodeLegacyContent`、`deriveEnvelope`。
3. 各 provider builder 改为接收规范块（数据仍由 `content_json` 解码而来），通用投影步骤只执行一次。
4. 修正现存偏差，见附录 B 第 1–4 条：
   - 重建时按 tool_use 顺序排列工具结果；
   - strict 只由裸 model 名计算一次；
   - Responses 当前轮图片顺序：代码与文档对齐；
   - 段压缩标记的 origin。
5. 测试网：
   - 每种 provider 模式都有「重建 == 实时」等价测试；
   - 多工具结果顺序测试；
   - 新旧实现对真实会话的请求做字节 diff：开启 `requestEvidence.autoPin`，比对录制。

**退出条件**
- 生产代码只经 store 写入，有守卫测试，类似 `sqlite-dialect-inventory.guard.test.ts`。
- 所有 builder 只读规范块。
- 新旧投影在录制语料上逐字节相同（附录 B 中有意修正的项除外，逐项列出）。

### P1 新表与双写

**交付**
1. schema：
   - 新建 `narrator_blocks`；
   - `narrator_messages` 新增 `body_format`、`turn_id`、`step_index`、`author_narrator_id`；
   - `narrator_tool_calls` 新增 `block_id`。
2. v2 触发器契约：生成器支持，SQLite 与 PG 各一份。
3. FTS 触发器加列过滤并带版本号。
4. store 写块表，新信封一律为 `body_format = 2`，同时写派生的 `content_json`。
5. 流式按块写入（§5.2）；WS v2；按 `attemptId` 丢弃 attempt。
6. 新增 `tool_result` 块（持久化图片和附言）；`state` 写入（§5.4）；`phase` 统一。
7. 一个 COW 实现；作者与属主分开。
8. 前端先切换身份：把 `blockId` 作为 key 和 API 寻址，删除流式交接的启发式。

**退出条件**
- 新会话全部是原生块；关闭开关能回到现状。
- 双写校验没有差异。
- 流式期间块表每秒的写入次数，不高于现状的 checkpoint 次数。

### P2 切换读取与回填

**交付**
1. 模型历史 worker、展示页 worker、External API、回退、块删除、导出、project.db 同步、merge summary、存储 GC，都改为读块表。
2. 页面 API 改为规范化形态；前端 store 以块 id 为键；`turn_id` 驱动 recency。
3. 回填作业（§10.3）；视需要增加 `narrator_turns`。

**退出条件**
- 回填完成。
- 除 project.db 组装和 decode 兜底之外，代码中不再读取 `content_json`。
- SQLite 方言台账里与 `content_json` 有关的 `json_extract` / `json_each` / `LIKE` 全部清零。

### P3 退役与新能力

**交付**
1. 新行的 `content_json` 写 `'[]'`；运行清空旧行的作业；删除 `compact_pending` 的读取方。
2. 解码路径保留一个版本后删除，只留给 project.db 旧归档导入使用。
3. 按需开放新能力：
   - 块级 FTS；
   - 块级永久链接、引用、评论；
   - 按块的保留策略；
   - 块 revision 历史；
   - External API 追加 `blockId`。

## 12. 风险与成本

### 12.1 存储
- **每行固定开销。** 每个块一行，开销包括行头、`id`/`message_id`/`attempt_id` 三个 21 字节文本，以及主键和 `(message_id, ordinal)` 两条索引的条目，粗估每块 150–250 字节。按 100 万块计，约 150–250MB。
- **先测量再定。** P1 之前，在生产库副本上用存储扫描 worker 统计「每条消息的块数分布」和正文体积，确认上面的估算。参考规模：
  - 一个 3.1 万条消息的叙述者，前缀正文约 149MB（`narrator-service.ts:216-218`）；
  - 账本表 56.1 万行，所在库 6.6GB（`schema.ts:1789-1790`）。
- **双写期正文约占两份。** P1–P2 期间，新信封同时保留派生的 `content_json` 和块行，正文体积接近翻倍；P3 清空后需要一次管理员 VACUUM 才能回收。

### 12.2 写入与触发器
- **流式期间写得更少。** 每个 checkpoint 从「整列重写 + 四组触发器」降为「单块 UPDATE，不触发 revision」；revision 触发器只在块封闭时触发。
- **超长块的成本依旧。** 单块重写的开销与块长度成正比，所以 10 万字级别的单个文本块在每个 checkpoint 仍有成本，与现在的量级相同。必要时可以对超长块的 checkpoint 限频。
- **持有者扇出不变。** 块封闭时 bump 持有者叙述者的方式与现在一样；被 fork 树广泛共享的信封不会在流式中，所以不受影响。

### 12.3 投影改造的回归风险
这是最大的风险：一个字节的差异，就意味着 prompt cache 失效，甚至产生非法请求。缓解手段：
- P0 不改 schema、单独发布，先在生产上稳定运行。
- 每种 provider 模式都有等价测试，并对真实请求录制做 diff。
- 每处有意的行为修正（附录 B）单独列出，单独验证。

### 12.4 双格式期的复杂度
- 解码路径、写时物化、回填作业会同时存在一段时间。
- 回填完成后再保留一个版本，然后删除解码路径（project.db 旧归档导入除外），避免永久背着两套格式。

### 12.5 PG 与迁移工具
- 新表和触发器要维护两种方言，并由生成器支持 v2 契约。这是 P1 的前置工具改动。
- 不得手改 `drizzle/` 或 `drizzle-postgres/`。
- PG 写路径不在本设计范围内。PG 只要求 schema 能生成、`db:check:pg` 能通过。

### 12.6 语义变化
以下几处是有意的行为变化，需要逐项写进 changelog：
- COW 时作者与属主分开（§6.3）；
- 惰性 fork 的共享判定（§6.2）；
- 段压缩标记的 origin（§6.4）；
- `aborted` 块的重放规则（§3.5）。

「轮」的定义一旦落库就很难再改，因此 D3 必须在 P1 之前定下来。

## 13. 验收矩阵

| 场景 | 期望 | 验证 |
|---|---|---|
| 各 provider 投影：Anthropic 官方/中转、Responses HTTP、Codex HTTP/WS、Completions、Gemini generateContent/Interactions、NUG 各通道、TokenDance、插件 | 新旧实现在录制语料上逐字节相同（附录 B 中有意修正的项除外） | 单测 + 请求录制 diff |
| 重建 == 实时 | 同一 pass 内的任一请求，都能由 DB 重建出相同字节 | 每种 provider 的等价测试 |
| 并行工具 | tool_result 顺序等于调用顺序；图片和附言重建后仍在 | 单测 |
| 流式 | 块 id 从 `block_opened` 到落库不变；没有合成消息；重连后不重复、不回退 | 前端集成测试 |
| 丢弃 attempt | 只删除该 attempt 的块；已执行的工具保留 | 单测 |
| 中断与冷启动 | open 块改为 `aborted` 或 `failed`；按 §3.5 重放 | 单测 + 重启恢复测试 |
| fork | 含 open 块的信封不共享；惰性 fork 之后，父叙述者修改旧消息会走 COW | 单测 |
| COW | 只有一个实现；作者不变；克隆的账本行不可执行；mailbox 回执同步更新 | 单测 |
| UI 状态 | `patchState` 不改变 `model_history_revision`，不触发 FTS | 触发器测试 |
| compact 与段压缩 | 生命周期与现状等价；CAS 不再使用 JSON 路径 | 现有 compact 测试迁移 |
| 回退与块删除 | 以块 id 寻址；预算按块字节计算；共享信封走 COW | 现有回退测试迁移 |
| External API v1 | 现有契约测试不修改即通过 | 契约测试 |
| project.db | 导出再导入，内容相等；旧归档可以导入 | 往返测试 |
| 搜索 | 结果与现状一致（content_text 规则逐条复刻） | 搜索测试 |
| 回填 | 可中断、可恢复、幂等；校验无差异；主线程单片不超过 1 秒 | 作业测试 + 慢操作日志 |
| 性能 | 流式写入耗时、模型历史加载、页面首屏都不劣于现状 | benchmark |
| 存储 | 在生产库副本上测得的增量落在预估范围内 | 存储扫描 |
| PG | schema 和迁移可以生成，`db:check:pg` 通过 | CI |

## 14. 待决问题

每项都给出推荐选项。其中 D2、D3、D8 需要在 P1 之前拍板。

| # | 问题 | 推荐 | 备选 |
|---|---|---|---|
| D1 | tool_result 的文本是否复制进块 | 不复制。块只存 `toolCallId`、图片、附言；文本留在账本 | 复制。块自包含，便于导出与块级 FTS，代价是账本输出的存储翻倍 |
| D2 | tool_use 输入以哪份为准 | 重放给模型的，就是执行时实际使用的输入（重定向或修复之后）；账本保留每个 attempt 的执行输入 | 重放模型原始输入，把改写说明放进 tool_result |
| D3 | 「轮」的边界 | 以一次运行为界（§3.6）：插话不开新轮 | 以 user 输入为界：插话开启新轮 |
| D4 | `aborted` 块是否重放 | text 重放，reasoning 不重放 | 都不重放，与现在冷启动的行为一致 |
| D5 | 前端传输怎样演进 | P1 先给数组元素附加块 id，P2 再改成规范化结构 | 一步到位改成规范化结构 |
| D6 | 是否做块级 FTS | 放在 P3，作为可选项、独立开关；先保留消息级 | P1 起改为块级 |
| D7 | 是否建 `narrator_turns` 表 | P2 视 UI 需求再定 | P1 与 `turn_id` 一起建 |
| D8 | `content_json` 的终态 | 清空为 `'[]'`，需要一次 VACUUM 回收空间 | 长期保留派生投影：存储翻倍，还要持续维护一致性 |
| D9 | 作者与属主是否拆分 | 拆分，新增 `author_narrator_id` | 统一按某一种语义处理，但会改变现有某条路径的行为 |

## 附录 A：现有块 type 到 kind 的映射

标「待核」的条目，在 P0 实现 `decodeLegacyContent` 时用真实数据确认。

| 现有 type | 常见 role | kind / subtype | 模型是否可见（现状） | 说明 |
|---|---|---|---|---|
| text | 全部 | `text` | 是（disp、system 行除外） | `publicationResult` 字段转为 `notice/publication_result` |
| reasoning | assistant | `reasoning` | 按携带策略 | `translatedText` 移入 `state` |
| thinking（旧） | assistant | `reasoning` | 按携带策略 | signature 移入 `provider_meta.anthropic` |
| redacted_thinking | assistant | `redacted_reasoning` | 仅同源 | `data` 移入 `provider_meta` |
| tool_use | assistant | `tool_use` | 是（需有已完成的账本行） | `thoughtSignature` 移入 `provider_meta` |
| tool_result | assistant（/bash、旧行） | `tool_result` | 是 | /bash 改为由账本 + tool_result 块表示 |
| web_search | assistant | `server_tool` / web_search | Responses 原生；Anthropic、Completions 为文本摘要；Gemini 跳过 | |
| image_generation | assistant | `server_tool` / image_generation | 仅 Responses | `result` 只在落盘失败时存在 |
| image | user | `image` | Anthropic、Responses 原生；其它为附件提示 | |
| text_file | user | `attachment` / text_file | 附件提示 | |
| file_reference | user | `attachment` / file_reference | 展开为引用文本 | |
| attachment、document | user | `attachment` | 待核 | 调研中列出，但未找到写入点 |
| system_injection 及旧拼写（sidecar、side_car、injection、system-injection） | sys / user | `context` / system_injection | 是（modelText） | 旧形态与前一个 text 块合并 |
| spec_continuation、spec_blocked_continuation、knowledge_hint、container_ready、browser_session_lost、merge_summary | sys | `context` / 同名 | 是（modelText） | `merge_summary.isLatest` 移入 `state` |
| review_feedback | user（前面带一个 text 块） | `context` / review_feedback | 前面的 text 块可见 | `applied` 移入 `state`；前面的 text 并入 `model_text`（待核） |
| ask_in_passing | system | `notice` / ask_in_passing | 否 | status 移入 `state` |
| subagent_recovery、cwd_recovery | disp | `notice` / 同名 | 否 | status 移入 `state` |
| spec_goal_added、spec_fork_carryover、spec_context_cleared | disp | `notice` / 同名 | 否 | |
| tool_loaded、tool_unloaded、bash_command | user | `notice` / 同名 | 待核 | role=user；Completions、Gemini 回退读 contentText 时可能看得到 |
| info | disp | `notice` / info | 否 | |
| error | system | `notice` / error | 否 | |
| subagent_messages、background_agents_completed | 注入伴随块 | `notice` / 同名 | 否 | |
| compact（含 subtype plan，以及清空上下文） | system | `marker` / compact、plan、clear_context | 否（摘要走 system prompt） | status、attempts 移入 `phase` / `state` |
| segment_compact | user | `marker` / segment_compact | 是（现在经 contentText） | 改为 `model_text` + `origin=system` |
| file_history_checkpoint | disp | `marker` / file_history_checkpoint | 否 | |

## 附录 B：调研中顺带发现的现存问题

第 1–4 条在 P0 修复，其余建议单独立项。标「推测」的条目需要先复现。

1. **重建历史时，工具结果按随机 nanoid 排序**（`agent-runtime/model-history-source.ts:247-248`，已核实），与调用顺序无关，重建出的请求前缀与实际发送的不同。
2. **strict 判定用的 model 字符串不一致。**
   - OpenAI 的 `buildHistory` 用带前缀的 model，`chat()` / `pushAssistantTurn` 用裸 model（`openai-provider.ts:697,710` 对比 `806-807,1058-1060`）。
   - 结果：`nug:gpt-5.5`、`tokendance:gpt-5` 在前者判为 false，在后者判为 true。
3. **Responses 当前轮图片的位置有三处说法。**
   - 代码把图片放在后面，并有测试钉住（`openai-provider.ts:3559`）；
   - `docs/AGENT_PROVIDERS.md:21` 写的是放在前面；
   - 重建历史时放在前面（`openai-provider.ts:3675`）。
4. **段压缩标记被当作人类消息**（推测）。标记行 role=user 且没有 origin，sender 投影会判为 human（`narrator-persistence.ts:4166-4182`；`sender-projection.ts:80-81`）。压缩进行中，模型会看到 `[Segment compacting]`。
5. **FTS update 触发器不限定列**（`server/db/fts.ts:378-385`）：信封上任何列的更新都会重建该行索引。`model_history` 插入触发器内的嵌套 UPDATE 会再触发一次（推测）。
6. **丢弃 attempt 时可能切错块**（推测）。截断按下标基线进行，而块按 `outputIndex` 排序插入（`narrator-event-handler.ts:1201-1211`；`output-content.ts:67-81`）。
7. **子代理提升为 primary 时不复制账本行**（推测）。这些 tool_use 因为没有已完成的账本行，会在模型历史中被丢弃（`subagent-extract.ts`）。
8. **插件 provider 可能把最新输入发两次**（推测）。它的 `buildHistory` 不弹出末尾的 user 消息（`remote-provider-adapter.ts:135-183`）。
9. **惰性 fork 的两个问题**（推测）。
   - 父叙述者之后若插入移位，子叙述者回填时按父叙述者当前的 seq 区间拷贝，可能错位（`narrator-refs-backfill.ts:194-211`）。
   - 共享判定不计算惰性继承者（§6.2）。
10. **编辑 assistant 后前端可能显示旧内容**（推测）。编辑后块的 `revision` 不变（`narrator-session.ts:6917-6921`），前端测量缓存可能命中旧高度和旧内容（`NarratorPanel.tsx:856-866`）。
11. **重复 toolUseId 的块菜单可能作用到别的块上**（推测）：`tc-x#dup1` 在选择索引里查不到，会回退到 `blockIndex 0`（`vlist-block-target.ts:108-114`；`PretextExactMessageList.tsx:4977-4982`）。同样因为重复 id，live patch 会写入同一 toolUseId 的所有出现位置（`vlist-live-patch.ts:222-231`）。
12. **assistant `content_text` 的拼接符不一致**：一处用 `\n`（`narrator-persistence.ts:2002-2007`），另一处用 `\n\n`（`narrator-session.ts:7060-7063`）。
13. **外来 provider 的 id 会进入 Responses 请求。** Responses 回放 message item id 时用的是 `messageUuid`，并且不检查来源（`openai-provider.ts:3910-3919`）。
14. **段压缩标记被 fork 共享后可能删不掉**（推测）。`deleteSegmentCompact` 按 `narratorMessages.narratorId` 定位；标记被 fork 共享时，其他 ref 的外键会让删除失败（`narrator-persistence.ts:4376-4398`）。
15. **一次性脚本已经过时且危险**（推测）。`server/db/migrate-role-to-disp.ts` 会把所有 `role='system'` 改为 disp，现在运行会连 compact / plan 标记一起改掉。
16. **文档与代码不一致。**
    - `docs/INJECTION.md:138` 提到的 `loadSubagentHistory` 已经不存在，实际逻辑在 `agent-runtime/history.ts:190-195`。
    - `docs/INJECTION.md:16` 说「每次请求发送完整历史」，但 Codex WS 可能只发增量。
    - `docs/codex-websocket.md:50` 写的是 `parallel_tool_calls: false`，代码是 true（`codex-request.ts:85`）。
17. **`drizzle/0206_context_numeric_stats.sql` 疑似人工追加过 SQL。** 文件里有 finalizer 产不出的 `INSERT…SELECT` 和 `CREATE TRIGGER`，推测是生成之后人工追加的，与 CLAUDE.md「禁止手改 drizzle/」冲突。
