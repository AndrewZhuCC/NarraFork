# 叙述者消息原生块存储设计

> 状态：讨论稿（分支 `claude/sleepy-bell-wfodnn`）。本文只做设计，不含实现。
> 调研基线：`main` @ `a5a2128e`。文中 `path:line` 都相对仓库根目录；标「推测」的结论没有在代码或测试中直接验证。
> 前提：本设计不以 vlist 性能为目标。vlist 变快只是附带结果，取舍以数据模型的正确性和可扩展性为准。
> 修订：块的粒度与种类按附录 C 的推导修订，第二轮调研基线 `af933c09`。

## 0. 摘要

**现状**
- 一条消息的正文是 `narrator_messages.content_json` 里的一个 JSON 数组。
- 模型可见的工具结果只存在 `narrator_tool_calls`。
- 另有 `content_text`、`context_chars_json`、`original_content_json` 等派生列和快照列。
- 块没有身份。几十处代码按「消息 id + 数组下标」寻址，或者在 SQL 里读 `$[0]`、用 `LIKE` 扫 JSON。
- 编辑、展示、组装各自对「一个单元」有一套隐含定义，彼此对不上（§1.3 J、K）。

**提案**
- 分三层：信封 → 块 → part（§3.1）。
  - **信封**（`narrator_messages`）：一个 assistant 步（一次 provider 响应），或一条输入。它是作者、用量、fork 共享与 COW 的单位。`narrator_message_refs` 不变。
  - **块**（新表 `narrator_blocks`，一块一行）：落库、编辑、展示、组装四个维度里，任何一个需要独立寻址、独立生命周期或独立取舍的最小内容。共 7 种：`input`、`output`、`reasoning`、`tool`、`context`、`marker`、`notice`（§3.3）。非 assistant 信封恰好一块。
  - **part**：块内有序的内容片段，包括文本、图片、文件、文件引用、推理分段。按（块 id, 下标）引用，不单独成行。
- 工具调用与它被采用的结果合成一个 `tool` 块。执行账本 `narrator_tool_calls` 保留，每个 attempt 一行，通过 `block_id` 挂到块上。
- 这套粒度的推导见附录 C：四个维度的证据、五种候选方案的对比、业界做法。

**三条贯穿规则**
1. **块 id 由服务端在块开始产生时分配。** 流式事件、数据库行、前端、HTTP API、回退、导出，全程使用同一个 id。
2. **模型可见的正文写定后不可变。** UI 状态和运行期状态放在独立的 `state` 列，修改它们不影响模型历史的 revision，也不需要 COW。
3. **结构在写入时确定。** 轮、步、attempt、工具的配对与采用、并行组、因果关系都写进数据，读取方不再推导；loop 生成的模型可见文本也落库。

**读路径**
- 引入规范历史视图和两段投影：先投成与 provider 无关的轮次，再投成各家线上格式（§7）。
- loop 内的实时请求和从 DB 重建走同一套投影，消除两条路径的字节差异。

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

**J. 逻辑单元与物理块错位。** 编辑和展示各自认定的单元，与 `content_json` 的数组元素对不上：
- **用户消息。** 逻辑上是原子的，回滚代码明写 "User messages must remain atomic"（`narrator-session.ts:6112-6122`）；物理上却是 `[image…, text_file…, file_reference…, text]` 多块。前端把整条气泡登记为 blockIndex 0（`vlist-block-target.ts:117-120`），所以对带附件的消息点「删除」，删掉的是第一个附件（代码推断，未运行验证）。
- **assistant 编辑。** 入口是单个 text 行，作用域却是整条消息：所有 text 块合并进第一个，工具之后的文字被搬到工具之前。恢复用的是首次编辑时的整数组快照，可能复活已删的工具调用（`narrator-session.ts:6904-7094`；复活一点为推测）。
- **reasoning。** UI 以相邻块组成的 run 为单位，行菜单删除 run 时只删最后一块（`use-message-revert-confirm.ts:63-68`；推测）。
- **review 结论。** `[text, review_feedback]` 不在任何配对规则里，块删除 API 能把模型文本和卡片拆开（`shared/native-injection.ts:22-30,208-215`）。
- **内部执行。** Eval 的内部 Read 只有账本行、没有块（`narrator-persistence.ts:3199-3263`），而构造器会把「账本有、块里没有」的调用补进历史（`anthropic-provider.ts:3251-3271`），模型可能看到自己没发过的调用（推测）。

**K. 组装各自为政。**
- **构造器各写一套。** 所有构造器都逐行翻译 DB 消息，各自实现分组、图片位置、文本拼接、工具排序：
  - Chat Completions 与 Gemini 的重建路径丢掉历史图片（`openai-provider.ts:4276`；`gemini-provider.ts:770-776`）；
  - Gemini 的 functionResponse 按名称和顺序配对（`gemini-provider.ts:758-769`）。
- **实时与重建有 19 处差异。** 可归为几类：工具结果的顺序与图片、turn nudge、broken 调用、loop 自造文本不落库、mid-pass 注入的合并方式、当前轮的拼接顺序、strict 判定、item id 与分段（附录 B 第 1–3 条、第 26–35 条）。
- **投影依赖运行期文件系统。** 附件提示里的 worktree 路径在投影时读文件系统决定（`attachment-projection.ts:99-118`），所以构造器不是存储数据的纯函数。
- **Responses 结构在落库时丢失。**
  - 多段 summary 被压成一段（`openai-provider.ts:2288-2303`）。
  - Codex WS 的增量续写要求逐项 JSON 前缀相同，而本地重新序列化的 item 缺字段，推测几乎总是退化为全量请求（`codex-websocket.ts:259-292`）。

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
- 编辑、删除、回滚的目标与用户看到的单元不一致（§1.3 J）。
- loop 生成的模型可见文本、工具结果图片不落库，重建与实时不一致（§1.3 K）。

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

### 3.1 三层与切分原则

```
narrator_message_refs ──▶ 信封 narrator_messages
  （叙述者, seq）           一个 assistant 步，或一条输入
                            role · 作者 · origin · turn/step · provider/source · usage · cause
                                │ 1:N，按 ordinal 排序；非 assistant 信封恰好 1 块
                                ▼
                            块 narrator_blocks
                            id · kind · phase · payload(part) · model_text · state · provider_meta
                                │ 每个 tool 块 1:N
                                ▼
                            账本 narrator_tool_calls（每个 attempt 一行）
```

- **信封**回答「谁、在什么时候、以什么角色、在哪次请求里说了这一段」。它是 fork 共享、COW、用量统计的单位。
- **块**回答「说了什么」。它是身份、寻址、生命周期、模型可见性与携带策略、编辑、展示的单位。
- **part** 是块内有序的内容片段，回答「以什么形态说的」：文本、图片、文件、文件引用、推理分段。它只按（块 id, 下标）引用，不单独成行。
- **账本**回答「工具实际是怎么执行的」：权限、attempt、执行身份、文件历史。它不是会话内容，模型只通过 tool 块看到其中被采用的那一次结果。

切分原则：

> **块 = 落库、编辑、展示、组装四个维度里，任何一个需要独立寻址、独立生命周期或独立取舍的最小内容。四个维度都不单独处理的结构，留在块内做 part。**

下表逐条检验候选边界，证据与完整推导见附录 C：

| 候选边界 | 拆开的理由 | 合在一起的理由 | 结论 |
|---|---|---|---|
| 用户文本 / 图片 / 附件 | 线上 part 不同，但这可以由投影在块内处理 | 一次写入；所有写操作按整条进行（回滚、编辑、删除）；展示为一个气泡；正文里的 `[image #N]` 占位符把图片和文本绑在一起 | 合：一个 `input` 块，带 part 列表 |
| tool 调用 / 结果 | 写入时间与产生方不同；线上位置不同，但投影可以拆 | 配对是所有 provider 的硬约束；展示为一张卡；一起删除；没有单独作用于结果的写操作；只有调用、没有结果的信封不能被 fork 共享 | 合：一个 `tool` 块，分两段生命周期（§3.5） |
| assistant 可见文本 / 推理 | 各自流式、各自完成；凭据与携带策略不同，可被独立丢弃；展示、编辑、搜索、外部 API 都分开处理 | 都出自同一步 | 拆：`output`、`reasoning` |
| 同一步的多个输出 item | 各有线上 id、phase、签名；与工具调用交错 | — | 拆：一个 provider item 一块 |
| item 内的子段（Responses 的多段 summary、多个 output_text） | 展示成步骤 | 凭据属于 item；没有独立操作 | 合：做 part |
| 注入正文 / 伴随卡片 / 旧格式的 text 配对 | — | 删除时已经按逻辑块一起删；属于同一次投递 | 合：一个 `context` 块 |
| 同一工具的多次 attempt | 执行身份、权限、文件历史各不相同 | — | 拆：账本行；块指向被采用的那一次 |
| 同一轮的多步 | 用量、模型、响应 id、共享与 COW 都按步 | 展示上合成 activity 或 run | 信封 = 步；轮由 `turn_id` 表达 |

### 3.2 身份

| 字段 | 含义 |
|---|---|
| `id` | 全局唯一，由 `generateId()` 生成，在块开始产生时由服务端分配：流式 item 打开、工具调用开始流式输入、输入落库、注入写入。流式事件、DB 行、前端、HTTP API、回退、导出都用这个 id。 |
| part 地址 | `(块 id, part 下标)`。part 在块 `done` 之后不可变，所以下标稳定；编辑产生新 revision 时整体替换。 |
| `origin_block_id` | COW 复制出的块指回原块，原块为 null。「同一个逻辑块」即同一条 lineage。跨叙述者解析永久链接、引用时，在本叙述者的 refs 范围内按 `id` 或 lineage 找唯一的一行，与账本 `executionOriginToolCallId` 的定位规则一致（`narrator-persistence.ts:3349-3394`）。 |
| `wire_id` | tool 块的线上调用 id，写入时一次确定：provider 给的 id 合法、且在本叙述者历史内唯一，就沿用；否则生成一个 wire-safe 的替代值，规则沿用 `tool-use-id-dedup.ts`。原始 id 存进 `provider_meta`。投影不再做改名。 |
| `attempt_id` | 产生该块的 provider attempt，即现在的 `OutputContentAccumulator.attempt`。丢弃某个 attempt 时按它精确删除，不再按数组下标基线截断。 |

旧数据的块 id 按确定性规则 `${messageId}:${index}` 生成：多个物理块合成一块时，index 取组内第一个下标（§10.2）。这样回填前后 id 不变。

### 3.3 块的种类

7 种 kind 按「谁写的、怎样产生」划分，内容形态交给 part：

| kind | 是什么 | 所在信封 | payload / part | 模型可见 |
|---|---|---|---|---|
| `input` | 一个参与者写的一条输入，参与者可以是人，也可以是经 mailbox 投递的其他代理 | user，一信封一块 | part：`text`、`image`、`file`、`file_ref`；可带 composer 文档 `doc` | 是：`model_text` 加图片 part |
| `output` | assistant 的一个可见输出 item | assistant 步 | part：`text`（带 citations）、`image` | 是 |
| `reasoning` | assistant 的一个推理 item，含 redacted | assistant 步 | part：推理分段（`text` part，`segment` 取 `summary` 或 `content`）；redacted 时没有 part | 按携带策略（§7.4） |
| `tool` | 一次工具调用及其被采用的结果 | assistant 步 | `name`、`input`、`executor`、`issuer`、`result`（§3.7） | 调用与结果成对，`settled` 时可见 |
| `context` | 系统或运行期写给模型的内容 | sys 或 user，一信封一块 | `subtype` 加 `body`（UI 用的结构化正文） | 是：`model_text` |
| `marker` | 控制标记：compact、计划、清空上下文、段压缩、文件历史检查点 | system、user 或 disp，一信封一块 | `subtype`、`summary?`、`messageCount?` | 否；段压缩完成后以 `model_text` 呈现 |
| `notice` | 纯 UI 卡片：错误、info、spec 卡片、恢复卡、发布结果等 | disp 或 system，一信封一块 | `subtype` 加原字段 | 否 |

**`input` 和 `context` 按作者区分，不按 role 区分。**
- role=user 不等于人类输入。自动续跑、批准反馈、审查检查、`/load` 通知、段压缩标记现在都是 role=user 的行（`orchestrator.ts:2740-2915`；`narrator-service.ts:539-630`），sender 投影因此可能把它们当成人写的（附录 B 第 4 条）。
- 改为：人或其他代理写的才是 `input`，其余模型可见文本都是 `context`。协议上需要的 role 仍由信封决定。

**`output` / `reasoning` 的粒度是 provider 的原生输出 item：**

| provider | `output` 块 | `reasoning` 块 |
|---|---|---|
| Anthropic | 一个 text content block | 一个 thinking 或 redacted_thinking block |
| Responses | 一个 message item，多个 output_text 成为 part | 一个 reasoning item，多段 summary（或 content）成为 part |
| Gemini generateContent | 连续的 text part 合成一块；带签名的 part 结束当前块 | 连续的 thought part 合成一块，同样在签名处断开 |
| Gemini Interactions | 一个 model_output step | 一个 thought step，多段 summary 成为 part |
| Chat Completions / 插件 | 该步的 content | 该步的 `reasoning_content`，或插件的 reasoning |

这样带来两点好处：
- 凭据（签名、encrypted_content、item id、phase、step id）总是落在它所属的那一块上。
- Responses 的多段 summary 不再在落库时被压成一段（现在用 `\n\n` 拼接，`openai-provider.ts:2288-2303`），前端也不必再从 `**标题**` 反推步骤。

**part 类型在所有 kind 之间共用：**

| part | 字段 | 用在 |
|---|---|---|
| `text` | `text`、`citations?`、`segment?` | input、output、reasoning |
| `image` | `assetId`、`mediaType`、`width?`、`height?`、`name?`、`sha256?` | input、output、tool 结果 |
| `file` | `assetId`、`name`、`size`、`mediaType?` | input（text_file） |
| `file_ref` | `reference`、`snapshotText`、`snapshotHash`、`capturedAt` | input |

用户图片、工具结果截图、生成的图片因此共用一套投影，包括磁盘还原、按协议能力放置、NUG 的 sha256 引用。块与上传文件的引用关系另由 `narrator_block_assets` 记录（§4.1），供孤儿回收与保留策略使用。

### 3.4 正文、状态、凭据三分

| 列 | 内容 | 可变性 | 影响模型历史 revision | 共享信封上的修改 |
|---|---|---|---|---|
| `payload` | 块正文与 part | 信封可共享之前可以随流式更新；之后不可变，编辑产生新 revision | 是 | 必须 COW |
| `model_text` | `input`、`context`、`marker` 的模型可见文本，写入时冻结 | 同上 | 是 | 必须 COW |
| `state` | 非模型可见的可变元数据，见下 | 随时 | **否** | 原地更新，所有持有者可见，与现状一致 |
| `provider_meta` | provider 凭据与线上 id，带 `source` | 随正文写定 | 是 | 必须 COW |

`state` 收纳：
- reasoning 的翻译（按 part）；
- review 的 `applied`、merge summary 的 `isLatest`；
- 卡片 status、ask_in_passing 的 resolved；
- compact 的 attempts、mode、error；
- tool 的并行组标注（`execGroup`）。

`model_text` 的规则：
- **`input`**：用户正文（含 quote 与占位符）加附件提示，写入时一次生成。
  - 附件提示里的路径也在此时确定，投影不再读文件系统（现状见 `attachment-projection.ts:99-118`）。
  - sender 标记不进 `model_text`，由投影按信封作者生成。
- **`context`**：沿用原生注入的 `modelText`（`shared/native-injection.ts:20-48`），并推广到所有系统文本。
- **不再回退 `content_text`。** 现在块里没有文本时会回退到 `content_text`；今后 `/bash` 命令、`/load` 通知、段压缩摘要都显式写 `model_text`。

`provider_meta` 的形状：

```
output / reasoning: {source, anthropic?:{signature}, openai?:{itemId, encryptedContent, phase, format},
                     gemini?:{thoughtSignature, stepId}}
tool:               {source, originalId?, call?:{itemId?, thoughtSignature?}, result?:{...}}
```

tool 的调用侧和结果侧分开存，与 AI SDK 的 `callProviderMetadata` / `resultProviderMetadata` 一致（附录 C.3）：
- **调用侧**：Responses 的 `fc_` item id、Gemini 的 thoughtSignature。后者不再嵌进账本 `input_json` 的保留键（`narrator-persistence.ts:515-536`）。
- **结果侧**：留给 provider 执行的工具，例如 Anthropic 原生搜索的加密结果（现在不落库）。

### 3.5 生命周期 `phase`

```
output / reasoning:  open ──▶ done
                       └──▶ aborted        用户中断、进程退出：已产生的内容保留

tool:                open ──▶ called ──▶ settled ──┐
                       │        ▲                   │ 重跑被拒工具：新 attempt，回到 called
                       │        └───────────────────┘
                       └──▶ failed         输入损坏（broken），永不执行

marker:              open ──▶ done | failed     compact、段压缩

input / context / notice: 写入即 done
```

`tool` 的各阶段：
- **`open`**：输入还在流式生成。只走 WS，不落行。
- **`called`**：输入已定，等待结束。
  - 期间可能等权限、等前台子代理，或被 continuation 挂起并跨越重启（`tool-continuation-service.ts`）。
  - 块行与账本 attempt 1 在**同一个事务**里插入。现在是分两步写的（`narrator-persistence.ts:2210-2333` 与 `2385-2420`）。
  - 对模型不可见。
- **`settled`**：被采用的 attempt 到达终态（success / fail）。
  - 调用与结果成对对模型可见。
  - 被采用的结果之后仍可能改变：detach 的真结果覆盖中断占位、Agent 结论被改写、遗弃清扫补写（`run.ts:996-1049`；`abandoned-tool-cleanup.ts:97-121`）。每次改变都是这一块的新 revision。
- **`failed`**：broken 输入。
  - 对模型不可见，同时写一条 `context` 块说明被移除的调用（§5.3）。
  - 这与实时路径一致：实时路径剔除 broken 调用并加提醒（`loop.ts:7583-7619`），而现在的重建路径会把它们重放出来。

由此带来的规则：
- **信封可共享 ⇔ 信封里没有 `open` 或 `called` 的块。**
  - fork 只共享可共享的信封；工具执行期间 fork，子叙述者拿到的是上一步为止的历史。
  - 这取代了现在「排除进行中 compact」的特例。
  - 也修复了 partial 行先被共享、后被原地追加的问题（`narrator-persistence.ts:2208-2333`；`narrator-session-state.ts:895-901`）。
- **块在信封可共享之前可以原地修订**，例如迟到的签名、引文回补（`output-content.ts:175-200`）。此后模型可见内容只能经编辑 API 产生新 revision，共享时走 COW。
- **启动恢复只扫 `phase IN ('open','called')` 的部分索引。**
  - `open` 的 output / reasoning 改为 `aborted`，`open` 的 marker 改为 `failed`。
  - `called` 的 tool 不动，由账本与 continuation 服务决定。
  - 这取代了 `recoverStaleCompactingMessages` 和冷启动对 `completed:false` 的清理。
- **模型历史只读以下块：**
  - `done` 和 `aborted` 的 output 与 reasoning。其中 `aborted` 的 reasoning 不重放，因为凭据不完整；`aborted` 的 text 照常重放，因为用户已经看到。
  - `settled` 的 tool。
  - `input` 和 `context`。
  - 带 `model_text` 且 `done` 的 marker。

### 3.6 轮、步、attempt、因果：写入时盖章

| 概念 | 定义 | 存放位置 |
|---|---|---|
| 轮（turn） | 一次运行：从叙述者被一次输入唤醒，到回到 idle 或等待用户为止，即 orchestrator 一个 active run 的生命周期（`orchestrator.ts:813`）。期间的插话、注入、自动续跑、错误卡都属于该轮；idle 时写入的卡片不属于任何轮 | 信封 `turn_id` |
| 步（step） | 一次 provider 响应，即一个 assistant 信封；同一步内的重试 attempt 共用这个信封 | 信封 `step_index`（轮内序号） |
| attempt | 同一步内的一次 provider 请求；被丢弃的 attempt，其 item 块整体删除 | 块 `attempt_id` |
| 并行组 | loop 的 `groupToolExecutions` 给出的执行分组 | tool 块 `state.execGroup` |
| 工具采用 | 模型看到的是哪一次 attempt | tool 块 `payload.result.toolCallId`；账本 `block_id` |
| 因果 | 信封由哪个块引起，例如：<br>- 后台任务的完成通知 → 启动它的 tool 块<br>- 异步问答的答复 → AskUserQuestion 的 tool 块<br>- `/goal` 的确认卡与续跑注入 → `/goal` 的 input 块<br>- `/bash` 的合成 assistant 步 → `/bash` 的 input 块 | 信封 `cause_block_id` |

- `turn_id` 由 orchestrator 在一次运行开始时生成，随持久化上下文传给 store。
- 前端的 recency（最近两轮）、流式交接、工具排队都改为读这些字段，原来的三套定义合成一套（§8.3）。
- 因果链接让展示可以把后台通知挂在它的工具卡旁，也让删除可以提示连带内容；它不参与投影。现在一条 `/goal` 命令散落成三行，彼此没有关联。
- 子代理在自己的叙述者上有自己的轮。

### 3.7 tool 块与账本

**tool 块**：一次调用及其被采用的结果。

```
payload: {
  name, input,                   // input = 重放给模型的那一份（重定向、修复之后）
  executor: "local" | "provider",
  issuer: "model" | "user",      // /bash 由用户发起
  result?: {                     // settled 时写入
    toolCallId,                  // 被采用的 attempt（provider 执行的工具为空）
    isError,
    parts?: [image…],            // 工具结果里的图片（现在只在内存）
    appendix?: string            // 确实属于这个结果的附言（turn nudge 不放这里，见 D12）
  }
}
```

- **结果文本不复制进块**（D1）。它仍在账本被采用行的 `output_json`，账本的输入输出已约 1.5GB（`execution-log-service.ts:4-6`）。
- **turn nudge 不再随结果走**（D12）。现在它只拼在本轮最后一个工具的结果上，代码注释的理由是 loop 未必能走到下一个边界（`loop.ts:7271-7277`）。改为在该步最后一个工具 settle 的同一事务里写一条 `context` 块（subtype `turn_nudge`），投影时排在该步工具结果之后、同一个 user 轮里，所以不依赖 loop 走到下一个边界。`appendix` 字段保留给今后确实属于某个结果的附言。
- **AskUserQuestion** 的作答仍写账本 attempt，模型经结果文本（"User answered: …"）看到。块的 `input` 不含作答，投影不再需要剥离（`question-model-input.ts:1-84`）。
- **provider 执行的工具**（web_search、image_generation）也是 tool 块，`executor = "provider"`，没有账本行。现在它们本来就是「调用和结果在一个块里」（`narrator-event-handler.ts:2141-2187`）。
- **`/bash`**：
  - 用户输入是一个 `input` 块，`model_text` 为命令原文。
  - 合成的 assistant 步里是一个 `issuer = "user"` 的 tool 块，`cause_block_id` 指回输入。
  - 现在 assistant 行里那个只给显示用的 `tool_result` 块不再需要（`narrator-service.ts:1117-1131`）。
- **重跑与迟到结果。** 重跑被拒工具时，在共享信封上先 COW 再开新 attempt，与现状一致（`narrator-persistence.ts:3397-3501`）。settled 之后被采用结果的改变，在共享信封上按 `preserveExecutionOwner` 的语义处理：执行所有者原地更新，其他持有者保留克隆（`narrator-persistence.ts:4031-4058`）。

**账本行**：每个 attempt 一行，`block_id` 指向它执行的 tool 块。
- 重复的 toolUseId 不再有歧义，`#dupN` 和 `resolveToolCallWriteId` 里的猜测可以去掉。
- 被采用的 attempt 由块显式指向，不再在读取时用 `latestToolCallAttempts` 推导（`narrator-messages.ts:2026-2048`）。
- **内部执行**，如 Eval 的内部 Read（toolUseId 以 `internal_read_` 开头，`narrator-persistence.ts:3199-3263`），不挂块，以显式字段标记，投影一律跳过。现在构造器的向后兼容路径会把它们补成模型从没发过的调用（推测，附录 B 第 21 条）。

**投影**：tool 块拆成两部分。调用放进 assistant 轮；结果按协议放置，可能是下一个 user 轮里的 tool_result、function_call_output、role=tool 消息或 functionResponse。顺序就是块的 ordinal，即调用顺序。

### 3.8 非 assistant 信封：一块

`input`、`context`、`marker`、`notice` 所在的信封恰好一块。现有的多块形态在这一块里合并：

| 现有形态 | 合并为 |
|---|---|
| user 输入 `[image…, text_file…, file_reference…, text]` | 一个 `input` 块，part 按原顺序排列，`model_text` 冻结 |
| 子代理收到的消息 `[附件…, text, system_injection(无 modelText)]`（`narrator-service.ts:2134-2165`） | 一个 `input` 块。那个注入块只用来表达发送者（`sender-projection.ts:40-53`），改为写进信封的作者字段 |
| review 结论 `[text, review_feedback]` | 一个 `context` 块：`model_text` 是 text，卡片进 `body`，`applied` 进 `state` |
| 注入 `[system_injection, subagent_messages…]`、旧格式 `[text, system_injection]` | 一个 `context` 块，伴随卡片进 `body` |

这条规则让「删除、回滚、选择、编辑都以整条为单位」由结构保证：不再依赖前端借用 blockIndex 0，也不再依赖 `injectionBlockViews` / `contextBlockViews` 的配对扫描。

## 4. 数据模型（schema 草案）

### 4.1 `narrator_blocks` 与 `narrator_block_assets`（新表）

```
narrator_blocks
  id                text  PK                       §3.2
  message_id        text  NOT NULL → narrator_messages.id ON DELETE CASCADE
  ordinal           integer NOT NULL               信封内的顺序
  kind              text  NOT NULL                 input | output | reasoning | tool | context | marker | notice
  subtype           text                           context / marker / notice 的子类型；tool 的工具名
  phase             text  NOT NULL DEFAULT 'done'  open | done | aborted | called | settled | failed
  revision          integer NOT NULL DEFAULT 0     正文版本；流式 checkpoint、settle、编辑时递增
  payload           text(json) NOT NULL            块正文与 part
  model_text        text                           input / context / marker 冻结的模型可见文本
  search_text       text                           冻结的可搜索文本（content_text 派生与块级 FTS 的来源）
  state             text(json)                     非模型可见的可变元数据
  provider_meta     text(json)                     凭据与线上 id，带 source
  wire_id           text                           tool 的线上调用 id，写入时确定（§3.2）
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
| `(phase) WHERE phase IN ('open','called')` | 启动恢复；判断信封能否共享。替代 `idx_messages_compact_pending` |
| `(origin_block_id) WHERE origin_block_id IS NOT NULL` | 按 lineage 解析永久链接、引用、评论 |
| `(kind, subtype) WHERE kind IN ('context','marker','notice')` | 按种类查找，例如 merge summary、compact 标记 |
| `(wire_id) WHERE kind = 'tool'` | 按 toolUseId 查找工具详情（兼容旧 API） |

```
narrator_block_assets
  block_id   text NOT NULL → narrator_blocks.id ON DELETE CASCADE
  asset_id   text NOT NULL                       上传文件 id
  role       text NOT NULL                       input_image | input_file | output_image | tool_image
  PRIMARY KEY (block_id, asset_id)
  INDEX (asset_id)
```

- store 写入含 `image` 或 `file` part 的块时，在同一事务里维护这张表。
- 孤儿上传回收与保留策略改为按 `asset_id` 查，替代 `LIKE '%"type":"image"%'` 全表扫描（`storage-service.ts:826-835`），而且能覆盖工具结果截图与生成的图片。

说明：
- **不设 `narrator_id` 列。** 块属于哪个叙述者，一律经 refs → 信封判定。块的作者就是信封的作者。
- **`ordinal` 用稠密整数。** 极少数需要在中间插入的情况（例如 provider 按 `outputIndex` 乱序到达），在同一事务里对这一个信封重新编号即可。单个信封通常只有几十块，开销可以忽略。
- **非 assistant 信封恰好一块**，由 store 保证（§3.8）。

### 4.2 `narrator_messages` 新增列（只做 `ALTER … ADD`）

| 列 | 含义 |
|---|---|
| `body_format integer NOT NULL DEFAULT 1` | 1 = `content_json` 是权威；2 = 块行是权威 |
| `turn_id text` | §3.6 |
| `step_index integer` | §3.6 |
| `author_narrator_id text` | 不可变的作者，COW 时保留；为 null 时视为 `narrator_id`（旧行）。sender 投影和归属展示读这一列（§6.3）。其他代理经 mailbox 投递的消息也写在这里（§3.8） |
| `cause_block_id text` | §3.6 |
| `provider_response_id text` | provider 的响应 id。现在 `sdk_message_uuid` 会被当作 item id 回放；以后 item id 由各块的 `provider_meta` 携带，这一列只记响应 id |

已有列在 `body_format = 2` 下的语义：

| 列 | 语义 |
|---|---|
| `content_json` | P1–P2 期间写入由块派生的兼容投影（供回退开关使用）；P3 起新行写 `'[]'`（§10.4） |
| `content_text` | 由块的 `search_text` 按统一规则派生（§4.6）；只在块封闭、settle、编辑时重算，不在每个 checkpoint 重算 |
| `context_chars_json` | 由块的 `model_chars` 和 kind 派生，context stats 触发器不变 |
| `original_content_json` / `compact_pending` | 只对 `body_format = 1` 有意义 |
| `body_revision` | 块触发器同样会 bump 它（§4.5） |

### 4.3 `narrator_tool_calls` 新增列

- **`block_id text → narrator_blocks.id`**：这次 attempt 执行的 tool 块。
  - 新写入的非内部行必填；旧行为 null。
  - COW 克隆账本行时，`block_id` 重映射到克隆后的块。
- **内部执行标记**：现有的内部执行行（`createInternalRead` 写入，toolUseId 以 `internal_read_` 开头）改为显式标记。它们的 `block_id` 为 null，投影与展示配对都跳过。

### 4.4 可选：`narrator_turns`（P2 视需要）

`id, narrator_id, status, trigger_message_id, started_at, ended_at, stop_reason, usage 汇总`。

这张表用来支撑「本轮用了多少时间、调用了几次工具」这类轮级 UI，以及 External API 的轮列表。只有 `turn_id` 列已经足够完成本设计的其余部分，所以这张表不是前置条件。

### 4.5 revision 触发器契约 v2

块表触发器，SQLite 与 PG 各一套。动作都是：bump 所属信封的 `body_revision`，并 bump 所有持有者叙述者的 `model_history_revision`（与现有 `holderUpdate` 相同）。触发条件：

| 事件 | 条件 |
|---|---|
| INSERT | `NEW.phase NOT IN ('open','called')` |
| DELETE | `OLD.phase NOT IN ('open','called')` |
| UPDATE OF `payload, model_text, provider_meta, kind, subtype, ordinal, phase, message_id` | 排除前后都处于 open 或 called 的更新（流式 checkpoint、工具执行中） |

要点：
- **`state` 的变化不触发。** 这就把「UI 状态让模型历史失效」从根本上去掉了。
- **流式 checkpoint 与工具执行不触发。** 块进入 done、settled 或 failed 时触发一次；tool 从 settled 回到 called（重跑）也触发，因为调用与结果从模型历史中消失。
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
- `narrator_blocks`、`narrator_block_assets`；
- refs；
- 账本中与块关联的列。

接口草案：

```ts
interface NarratorStore {
	// 非 assistant 信封：一信封一块
	appendSingle(tx, env: NewEnvelope, block: NewSingleBlock, ref: RefPlacement): BlockId;
	// assistant 步
	openStep(tx, env: NewStepEnvelope, ref: RefPlacement): MessageId;
	checkpointItem(tx, messageId, item: ItemCheckpoint): void; // 首次插入（open），之后只更新这一行
	closeItem(tx, blockId, phase: "done" | "aborted", final?: ItemPatch): void;
	discardAttempt(tx, messageId, attemptId): void;
	sealStep(tx, messageId, usage: EnvelopeUsage): void;
	// 工具
	callTool(tx, messageId, call: NewToolCall): { blockId: BlockId; toolCallId: string }; // 块 called + 账本 attempt 1
	settleTool(tx, blockId, toolCallId, settlement: ToolSettlement): void; // 采用、图片、附言 → settled
	retryTool(tx, holderNarratorId, blockId): { toolCallId: string }; // 新 attempt，块回到 called；共享则先 COW
	failTool(tx, blockId, reminder: NewSingleBlock): void; // broken：块 failed，另写一条 context
	// 修改
	editBlock(tx, holderNarratorId, blockId, patch): BlockId; // 共享则 COW，返回新 id
	deleteBlocks(tx, holderNarratorId, blockIds: BlockId[]): void; // 共享则 COW；tool 连同账本
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
  - tool_use 块与账本行分两步落库（`narrator-persistence.ts:2210-2333` 与 `2385-2420`）；
  - tool_loaded / tool_unloaded、/bash 的 ref 单独开事务追加（`narrator-persistence.ts:654-667`）；
  - `persistAssistantMessage` 在消息事务之后逐个 await 插入账本行（`2044-2081`）；
  - `overwriteToolCallInput` 分两次 await 读改写（`4073-4119`）。

### 5.2 流式：开 item → 单行 checkpoint → 封 item

1. **step 开始。** 分配信封 id（同一步内的各 attempt 共用）和 attempt id。不写库。
2. **item 打开。** provider 开始一个输出 item（content block、output item、part run、step）时分配块 id，广播 `block_opened {messageId, blockId, kind, ordinal, attemptId}`。不写库。
3. **delta。** 广播 `block_delta {blockId, part, revision, textOffset, text}`。
   - 只带增量。
   - 按帧合并（约 16–50ms），遵守 CLAUDE.md 关于 WS 高频输出要合并、节流的规则。
   - `part` 是块内分段的下标，例如 Responses 的 `summary_index`。前端可以按段封存，只重解析活动的那一段；现在为此在落库文本里插 `\n\n`（`openai-provider.ts:2288-2303`），以后不再需要。
4. **checkpoint。**
   - 首次：插入信封（如果还不存在）和块，phase 为 open。
   - 之后：只 UPDATE 这一块的 `payload`、`revision`。
   - 广播 `block_checkpoint {blockId, revision}`，不再广播整条消息。
5. **item 完成。** `closeItem(done)`：
   - 写入最终的 `provider_meta`；
   - 触发一次 revision bump；
   - 重算该信封的 `content_text` 和 `context_chars_json`。
6. **工具输入完整。** `callTool` 在一个事务里插入 tool 块（phase 为 called）和账本 attempt 1（带 `block_id`）。
7. **工具结束。** `settleTool` 与账本的结果写入放在同一个事务里：写入被采用的 attempt、结果图片、appendix，phase 改为 settled。需要回合将尽提示时，同一事务里再写一条 `turn_nudge` context 块（D12）。
8. **step 结束。** `sealStep` 补写用量和 `provider_response_id`。
   - 不重写块，也不需要 COW。
   - 现在 `completeAssistantOutput` 为了写 `completed:true` 要走一次 COW（`narrator-output-recovery.ts:46-63`），这一步随之消失。
9. **信封变为可共享。** 信封里不再有 `open` / `called` 的块之后，fork 才能共享它（§3.5）。

**写入代价**
- SQLite 不能部分更新一个 TEXT 值，所以每个 checkpoint 仍要重写该块的 `payload`。不过开销只与这一块的长度成正比，不再与整条消息的长度成正比。
- open 块不触发 revision；FTS 只在块封闭时随 `content_text` 更新一次。

### 5.3 丢弃 attempt、中断、错误、broken、loop 文本

| 场景 | 现在 | 改为 |
|---|---|---|
| `attempt_discarded` | 按 `api_request_start` 时记录的块数基线截断数组。基线是数组下标，而块又按 `outputIndex` 排序插入，可能切错（推测，`narrator-event-handler.ts:1201-1211` 与 `output-content.ts:67-81`） | `discardAttempt(messageId, attemptId)` 删除该 attempt 的 item 块。<br>已经执行过的 tool 块保留，因为副作用已经发生。<br>投影时，同一信封里属于更早 attempt 的块单独成一个 assistant 轮并带上结果，排在当前 attempt 的内容之前，这样「thinking 必须在首位」一类约束仍然成立（§7.2）。<br>现在这些块留在信封里，重试请求却没带它们（`narrator-event-handler.ts:1318-1323`） |
| 用户中断 | 内容 flush 后保持 `completed:false`；之后运行中会重放，冷启动时又会删除 | open item 改为 `aborted`，重放规则见 §3.5。<br>执行中的工具照旧以 interruptedByUser 结束并 settle；detach 的真结果稍后到达时，作为新 revision 替换 |
| 终止性错误 | `finalizeOrCleanupPartialMessage` 的三个分支在数组上做过滤 | 同样三个分支，改为按 kind / phase / 账本状态删除块行 |
| broken 输入 | 实时路径剔除调用并加提醒；账本存净化后的输入与 brokenToolCallResult；重建时又重放出来 | `failTool`：tool 块改为 failed（不可见），再写一条 `context` 块，内容是提醒原文。实时与重建一致 |
| loop 自造的文本 | brokenToolCallReminder、userContinue、reflection 纠正只在内存（`loop.ts:6445,6889,7130,7179,7617`） | 各写一条 `context` 块。loop 的其他提醒（relaxed_plan、knowledge hint、silent_progress）本来就落 sys 行，两者规则统一 |
| 冷启动 | 扫 compact 部分索引，并删除最后一条 assistant 中 `completed:false` 的块 | 只扫 `phase IN ('open','called')` 的部分索引，一次处理完（§3.5） |

### 5.4 UI 状态写入

`patchState` 只做单行 UPDATE 加广播 `block_state`：
- 不 bump revision；
- 不 COW；
- 不触发 FTS。

下列现有写入点都迁到 `patchState`：

| 写入点 | 现在的问题 |
|---|---|
| `patchReasoningTranslation` | 没走 COW，却 bump 模型历史。改为按 part 存译文 |
| review 的 `markReviewFeedbackApplied` / `release` | 同上 |
| merge summary 的 `isLatest` | 之前要先 `LIKE` 全表扫描定位；定位改为按 `subtype` 查索引 |
| ask_in_passing 改为 resolved | |
| subagent_recovery 卡片 status | |
| compact 的 lifecycle（mode、attempts、error） | |

compact finalize 的 CAS 改为：

```sql
WHERE id = ? AND phase = 'open' AND revision = ?
```

即比较块的 `phase` 和 `revision` 列，不再对 JSON 路径做 `json_extract`。

AskUserQuestion 的作答不走 `patchState`：它是 attempt 的执行输入，仍写账本，模型经结果文本看到（§3.7）。

### 5.5 编辑与操作的目标

每个操作都落在一个块（或一组块）上，不再借用数组下标：

| 操作 | 目标 | 行为 | 修正的现状问题 |
|---|---|---|---|
| 编辑 user 输入并重新生成 | `input` 块 | 新 revision：`doc`、part、`model_text` 一起重新生成；截断其后的历史 | — |
| 编辑 assistant 文本 | 被点的那个 `output` 块 | 只改这一块；与新文本对不上的 citations 丢弃；首次编辑前的正文存 `original_payload` | 现在合并全部 text 块，把工具之后的文字搬到工具之前（附录 B 第 20 条） |
| 恢复原文 | 同一块 | 回到该块的 `original_payload`，不影响其他块 | 整数组快照会复活已删的工具调用 |
| 删除、回滚到此 | 一组块 id | 共享信封走 COW；tool 块连同全部账本 attempt 一起删；`input` 整块；reasoning run 传整组 id | user 气泡只删第一个附件；run 只删最后一块；回滚截掉 run 后半（附录 B 第 18、19 条） |
| 删除并回退文件 | tool 块 | 由 `block_id` 找到全部 attempt 的执行证据 | 要求 tool_use 与账本行 1:1，重跑过的工具会失败（附录 B 第 24 条） |
| 重执行被拒工具 | tool 块 | `retryTool`：开新 attempt，块回到 called | — |
| AskUserQuestion 作答 | tool 块的当前 attempt | 写账本；结果文本对模型可见 | — |
| reasoning 翻译 | reasoning 块的 part | 写 `state`，不 bump revision | — |
| 卡片状态 | context、notice、marker 块 | `patchState` | 现在原地改 JSON 并 bump 模型历史 |
| 引用、永久链接、搜索跳转 | 块 id，可附 part 下标与字符区间 | 锚点 `b:<blockId>`，经 lineage 解析 | 锚点只到消息，COW 换 id 后失效（附录 B 第 25 条） |
| 复制 | 块或 run | 复制块的显示文本；`input` 复制用户原文，不带附件提示 | 复制 user 消息时会带出附件提示 |

编辑菜单只对 `output` 与 `input` 块开放。现在对所有 markdown 行开放，点系统行会被服务端拒绝（附录 B 第 23 条）。

## 6. 共享、fork 与 COW

### 6.1 不变的部分

- refs 仍以信封为粒度。
- fork 仍用 `INSERT…SELECT` 复制 ref（`narrator-service.ts:2959-2995`）。
- 惰性回填（`narrator-refs-backfill.ts`）、seq 的分配与移位语义都不变。

块只跟着信封走，fork 本身的成本不变。

### 6.2 不可变规则

1. **信封可共享之后，块的正文不可变**，这里的正文指 `payload` / `model_text` / `provider_meta`。在此之前（信封里还有 `open` 或 `called` 的块）只有写入方会改它（§3.5）；之后在被共享的信封上做编辑或删除，一律经 COW。
2. **共享判定要补上惰性 fork 的缺口。**
   - 现在只数 refs。惰性 fork 的子叙述者对祖先区间还没有 ref，所以父叙述者原地修改旧消息时，子叙述者回填后会看到修改后的版本（推测）。
   - 新判定：refs 数大于 1，或者存在一个惰性继承者，其待回填区间覆盖该 ref 的 seq。后者经 `refs_inherited_from` 的部分索引查询，是常数级开销。
3. **含 `open` 或 `called` 块的信封不参与共享**（§3.5）。

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
- 生命周期：phase 从 open 变为 done 或 failed；attempts、mode、error 记在 `state` 里。
- `refs.is_compact` 和 `narrators.context_summary` 的语义都不变。
- `compact_pending` 生成列及其部分索引，由块表的 `phase` 部分索引取代。

**段压缩标记**
- 现状：
  - 它是 role=user、没有 origin 的行，模型文本靠 `content_text` 回退（`narrator-persistence.ts:4166-4182`）。
  - sender 投影可能把它当作人类消息（推测）。
  - 压缩进行中，模型还会看到 `[Segment compacting]`。
- 改为：
  - 信封 role 保持 user，以保住协议位置；`origin = system`。
  - `marker` 块（subtype `segment_compact`）在 done 时写入 `model_text`，以 `[Earlier conversation summarized]` 一类的前缀标明这是摘要。
  - phase 为 open 时对模型不可见。

**计划 / 清空上下文**：都是 `marker` 块的 subtype，`refs.is_compact` 的写法不变。

**file_history_checkpoint**
- 仍用 `segment_compact_id` 指向自身来隐藏（`narrator-messages.ts:1662-1706`）。
- 块形态是 `marker` / `file_history_checkpoint`；被克隆的账本行带上 `block_id`。

### 6.5 子代理、提取、注入、mailbox

| 对象 | 设计 |
|---|---|
| 子代理消息 | 关联方式不变：`parentToolUseId` 是父叙述者那边的线上 id，子代理与 Agent 调用的精确关联仍用 `narrators.originToolCallId` |
| 子代理收到的消息 | 一个 `input` 块；发送者写进信封的 `author_narrator_id` 与 origin，不再用一个无 `modelText` 的注入块表达（§3.8） |
| 子代理提升为 primary（extract） | 深拷贝信封、块和账本行（新 id，带 lineage）。现在不复制账本行，这些 tool_use 在模型历史里会被丢掉（`subagent-extract.ts`，推测） |
| 注入 | `deliverInjection` 写一个 `context` 块（subtype 为 `system_injection` 等），带 `model_text`；伴随卡片（`subagent_messages`）并入 `body` |
| loop 生成的文本 | broken 提醒、reflection 纠正、userContinue、knowledge hint 等都写 `context` 块（§5.3） |
| 后台任务与异步问答 | 完成通知、异步答复各写一条 `context` 块，`cause_block_id` 指向启动它的 tool 块（§3.6） |
| mailbox | `recipientMessageId` 的预留与 materialize 保持在信封粒度，不变 |
| 问题补充 | 读该信封唯一的 `context` 块的 `body.supplement`，不再读 `$[0]/$[1].body.supplement` |

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
		source: string | null; // 产出该步的 provider 来源，用于同源判定
		blocks: CanonicalBlock[]; // 只含模型可见阶段的块（§3.5），按 ordinal 排序
	}>;
	adoptedOutputs: Map<string /* toolCallId */, { status: "success" | "fail"; output: ToolOutput }>;
}
```

**加载器**
- 沿用模型历史 worker 和 `model_history_revision` 源缓存（`model-history-source.ts:334-445`）。
- 读取顺序：refs → 信封 → 块 → 被采用的账本行。账本行按 tool 块的 `result.toolCallId` 精确读取，不再先按行 id 排序、再推导最新 attempt。
- `body_format = 1` 的信封在 worker 里解码（§10.2）。
- 图片等二进制在 worker 里按 asset 预取。读不到的留空槽，规则与现在的 `orderUserContent` 一致（`user-content-order.ts:63-73`）。投影本身不读文件系统。

### 7.2 两段投影

```ts
toTurns(view: HistoryView, target: ProjectionTarget): NeutralTurn[]        // 第一段：与 provider 无关
toWire(turns: NeutralTurn[], target: ProjectionTarget): ProviderWireHistory // 第二段：各家线上格式
```

**第一段 `toTurns`** 决定结构，所有 provider 共用。
- **分组**
  - 每个 assistant 步投成一个 assistant 轮，其中的 output、reasoning、tool 调用按 ordinal 排列。
  - 同一信封里属于更早 attempt 的已执行 tool 块单独成一轮，连同结果排在前面（§5.3）。
  - 每个 assistant 轮之后紧跟该步 settled 的 tool 结果，按 ordinal 排列，也就是调用顺序。
  - 从这之后到下一个 assistant 步之前的 `input`、`context`、带 `model_text` 的 marker，按 seq 组成一个 user 侧分组。每项保留自己的 user / sys 角色，由第二段决定合成一条 user 消息还是拆出 system。
- **内容**
  - `input` 投成 `model_text` 加图片 part。图片按 `[image #N]` 占位符就位，规则沿用 `user-content-order.ts:15-56`。
  - file_ref 展开为引用文本。
  - 剥离 citation 标记。
  - 按信封作者加 sender 标记。
  - tool 结果 = `outputToText(账本输出)` + `appendix` + 结果图片。
- **取舍**：每块按携带策略（§7.4）决定原生回放、降级为文本或丢弃。strict 只由裸 model 名计算一次。
- **跨块规则**：少数规则依赖上下文，例如 Await 问答回执要看同一请求里是否已有答复（`question-model-input.ts:112-165`）。它们是 view 的确定性函数，统一写在第一段，作为显式规则测试。

**第二段 `toWire`** 只做格式映射，不再决定结构：

| 中立结构 | Anthropic | Responses | Chat Completions | Gemini generateContent | Gemini Interactions |
|---|---|---|---|---|---|
| assistant 轮 | 一条 assistant 消息，content block 按 ordinal 排列；相邻同角色合并，首条补 user（`ensureAlternating`） | 平铺的 item | 一条 assistant 消息：content + tool_calls + reasoning_content | 一个 model content | 平铺的 step |
| output 块 | text block | message item：part → output_text；同源时带 itemId、phase | 拼进 content | text part，同源时带签名 | model_output step，同源时带 step id |
| reasoning 块 | thinking（strict 时仅同源带签名）或 redacted_thinking | reasoning item：part → summary / content；同源时带 encrypted_content，否则降级或丢弃 | reasoning_content（仅非 strict） | thought part，同源时带签名 | thought step，part → summary |
| tool 调用 | tool_use | function_call：`call_id = wire_id`，同源时带 item id | tool_calls | functionCall，带 thoughtSignature | function_call，签名用前置 thought step 表达 |
| tool 结果 | 下一条 user 消息最前面的 tool_result；图片放进 content | function_call_output；图片放进紧随其后的 user `input_image`，支持时放进 output 数组 | role=tool 消息；图片放进紧随其后的 user 消息 | functionResponse；不支持多模态结果时，图片放进随后的 user content | function_result，非错误结果可带图片 |
| provider 执行的工具 | web_search 投成 user 文本摘要；image_generation 跳过 | web_search_call / image_generation_call | user 文本摘要 | 跳过 | 跳过 |
| user 侧分组 | user 消息；官方 API 下 sys 项投成对话中途的 system，位置受约束 | user input_text / input_image；system 与 developer 汇入 `instructions` | user 消息 | user content | user_input |

说明：
- 表中各格是现有构造器行为的整理，各处偏差见附录 B 第 26 条起。第一版逐条复刻现状，只修正 §11 P0 列出的项。
- 「图片放进随后的 user 消息」是现在 Responses 实时路径的做法（`openai-provider.ts:3591-3611`）。Chat Completions 与 Gemini 现在直接丢图（`openai-provider.ts:1077-1088`；`gemini-provider.ts:361-378`）。统一之后，由第二段按协议能力决定放在哪里。
- Gemini generateContent 的 functionResponse 现在不带 id，按名称和顺序配对（`gemini-provider.ts:758-769`），同名并行调用可能配错（推测）。协议支持 id 时由第二段带上。

### 7.3 实时请求与重建合为一条路径

**做法**
- loop 持有一个 `HistoryView`。store 写入块之后，把**同一个规范块对象**追加进 view。
- 每次发请求都调用 `toTurns` 和 `toWire`。
- provider 接口删去 `pushAssistantTurn`、`pushUserTurn`、`formatToolResult`、`buildHistory`，只剩 `toWire` 和 `chat(wire)`。
- loop 内不再拼接字符串。当前轮由哪些单元组成、以什么顺序合并，完全由 seq 和第一段的分组规则决定。现在由三处各自拼接：`history.ts:238-243`、`orchestrator.ts:2007`、`loop.ts:3309-3311`。

**性能与缓存**
- 按信封记忆两段的结果，键为 `(envelopeId, body_revision, targetFingerprint)`。每次请求只投影新增或变化的信封。
- 前缀字节天然稳定，有利于 prompt cache。

**Codex WS 的增量续写**
- 前提条件：新请求必须是「上次输入 + 上游返回的原始 item」的逐项前缀（`codex-websocket.ts:259-292`）。
- 现状：本地重新序列化的 item 缺 `status`、`annotations`、`id`、`phase` 等字段，并且合并了 summary 分段。推测几乎每次有 assistant 输出之后，都会退化为全量 `response.create`。
- 要求：Responses 的 output、reasoning、tool 块经第二段投影后，必须还原出与上游原始 item 相同的 JSON。这条要求规定了 `provider_meta.openai` 与 part 必须保存哪些字段，并由测试钉住。

### 7.4 跨 provider 的携带策略集中在一处

`carryPolicy(block, target)` 按块返回三种结果之一：`native`、`degrade-to-text`、`drop`。同源判定读块的 `provider_meta.source`，没有时读信封的 `source`。

第一版逐条编码现有规则：
- Anthropic strict 模式下，外源签名整块丢弃；中转模式保留文本，签名置空。
- Responses 同源时回放 encrypted reasoning，否则降级为文本或丢弃。
- Gemini 的 thought 文本总是回放，签名只在同源时携带。
- 插件只携带同源的 continuation。

同时修正几处现状：
- strict 只由裸 model 名计算一次。现在 `buildHistory` 用的是带前缀的 model（`openai-provider.ts:695-699`），NUG 也是如此（`nug-provider.ts:383-397`）。
- message item id、Interactions 的 step id 只在同源时回放。现在不检查来源（`openai-provider.ts:3910-3919`；`gemini-interactions-provider.ts:697-754`）。
- image_generation 结果的读盘挪出主线程。现在投影时在主线程 `readFileSync`（`openai-provider.ts:3989-3996`），改为在加载器 worker 里预取。

### 7.5 冻结的、显式输入的、传输层的

投影必须是数据的纯函数。每样东西属于三类之一：

| 类别 | 内容 | 处理 |
|---|---|---|
| 写入时冻结进块 | 附件提示与路径；`/bash` 等命令的模型文本；wire-safe 工具 id；turn nudge（作为 `context` 块）；broken 提醒与 loop 自造文本；ExitPlanMode 计划正文的引用形态（随 settle 冻结，现在随账本 status 变化，`strip-plan-body.ts:30-60`）；被采用的 attempt | 投影只读数据 |
| 投影的显式输入 | target（协议、裸 model、strict、官方或中转、能力）；作者标题与用户名（纪元内读 pin，§7.7）；asset 字节是否可读 | 进 `targetFingerprint` 或 view |
| 投影之后的传输层 | NUG 的图片 sha256 引用与网关 ack 剥离（`nug-provider.ts:444-507`）；Codex WS 的续写判定 | 不影响投影结果的缓存 |

### 7.6 字节稳定性约定

- **同一版本内**：「从 DB 重建 == 实时发送」由构造保证，并由每个 provider 的等价测试兜底（§13）。
- **跨越投影切换的那次发布**：允许一次性的 prompt cache 未命中。这与 compact 之后换 conversationId 重新起缓存（`narrator-session-state.ts:471-478`）的代价同级，在发布说明里写明即可。

### 7.7 前缀保持：compact 之间不改写已发送的历史

已经发给模型的前缀，在下一次 compact 之前不因派生数据的变化而改写；变化改为在末尾追加一条简短的 `context` 提醒。compact 之后前缀本来就要重建，这时完全按最新数据投影。这样 prompt cache 只在 compact 时失效一次，而不是每次改名或状态变化都失效。

**投影纪元**
- 一个叙述者的纪元从最近一次成功 compact（或叙述者创建）开始，到下一次 compact 为止。
- 纪元内，投影要用到的派生输入在信封**第一次被投影**时固定下来，存进 pin 表；之后的请求都读 pin，不读最新值。
- compact 成功后开启新纪元，旧 pin 作废，全部按最新数据重新投影。

```
narrator_projection_pins
  narrator_id  text NOT NULL
  epoch        text NOT NULL        开启该纪元的 compact 标记块 id（创建时为 'root'）
  message_id   text NOT NULL
  key          text NOT NULL        例如 sender_label
  value        text NOT NULL
  PRIMARY KEY (narrator_id, epoch, message_id, key)
```

- pin 按叙述者存，因为 fork 共享信封，但各自有各自的纪元和已发送前缀。

**受这条规则约束的派生输入**

| 输入 | 纪元内的处理 | 现状 |
|---|---|---|
| sender 标记里的叙述者标题、用户名（D14） | 用 pin 的旧值；改名时追加一条提醒，如「叙述者 A 已更名为 B」 | 改名会让全部历史的 sender 标记变化，触发器让源缓存失效（`model-history-revision-sql.ts:84-101`） |
| ExitPlanMode 计划正文的引用形态 | 已在 settle 时冻结（§7.5），不受影响 | 随账本 status 变化（`strip-plan-body.ts:30-60`） |
| Await 问答回执的改写 | 按回执第一次被投影时的判断固定；后来到达的答复照常作为新内容追加 | 每次投影时按「同一请求里是否已有答复」重新判断（`question-model-input.ts:112-165`） |
| 其他今后新增的、依赖可变外部数据的投影规则 | 默认按本节处理，进 pin 表 | — |

**不受约束的情况**（本来就改变前缀，接受 cache 未命中）
- 用户显式的编辑、删除、回滚、段压缩：改的就是历史本身。
- 切换 provider 或模型：`targetFingerprint` 变化，第二段整体重投。
- 携带策略因 target 变化而不同：同上。

**提醒块**
- 由触发变化的服务（改名、状态变更）写入，`subtype` 标明类别，`model_text` 简短。
- 在叙述者 idle 时写入的提醒，随下一次运行的第一条请求发出；同一纪元内同一对象的多次变化，只保留最后一条未发送的提醒。

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
| `block_delta` | `blockId, part, revision, textOffset, text`（按帧合并） | `content_block_delta` |
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
| user 气泡登记为 blockIndex 0（`vlist-block-target.ts:117-120`） | `input` 块 id；删除、选择、回滚都作用于整块 |
| `enrichToolUseBlocks` 把账本字段注入块，再由 `latestToolCallAttempts` 推导最新 attempt | 工具卡 = tool 块 + 被采用（或正在执行）的 attempt 的账本摘要，按 `block_id` 精确关联 |
| 从 reasoning run 的文本按 `**标题**` 反推步骤（`segment-adapter.ts:1225-1246`） | 有分段的 provider 直接用 part；只有单段的 provider（如 Anthropic）才解析标题 |
| reasoning run 的操作只带一个下标 | run = 同一信封里相邻 reasoning 块的 id 列表，操作带整组 id |
| 三套「轮」的定义；前端重算并行分组 | 读 `turn_id`、`state.execGroup` |
| 后台任务的完成通知与启动它的工具卡之间没有关联 | 读 `cause_block_id`，通知可以挂在工具卡旁 |
| live patch 按 toolUseId 写所有出现位置（`vlist-live-patch.ts:222-231`） | 按账本 `block_id` 精确写入 |

客户端仍然要做的只剩展示层的事：LOD 折叠、跨信封的 tool-run 与 activity 分组、测量。

## 9. 其它消费方的迁移

| 消费方 | 现在依赖 | 改为 | 阶段 |
|---|---|---|---|
| 模型历史加载与各 provider builder | `content_json` + 账本；各 builder 自行解析 | 规范视图 + 两段投影（§7） | P0 改为吃规范块；P2 切换到块表 |
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
| 块 id | `${messageId}:${i}`；多个物理块合成一块时，`i` 取组内第一个下标 |
| user 输入 `[image…, text_file…, file_reference…, text]`、命令类 `[file_reference…, text]` | 一个 `input` 块，part 按原顺序排列；`model_text = content_text`，即冻结现有的附件提示，旧行投影出的字节不变 |
| 子代理收到的消息里，无 `modelText` 的 system_injection | 并入信封作者；块仍是 `input` |
| `bash_command` | `input`，`model_text = content_text`（现在就是经 `content_text` 回退对模型可见的） |
| origin 为 system 的 user `[text]`（自动续跑、批准反馈、审查检查）、`tool_loaded`、`tool_unloaded` | `context`，`model_text = content_text` |
| review `[text, review_feedback]` | 一个 `context` 块：`model_text` 取 text，卡片进 `body`，`applied` 进 `state` |
| 注入及其伴随块、旧格式 `[text, system_injection]`、sys 的原生卡片与纯 text | 一个 `context` 块；配对规则沿用 `injectionBlockViews` / `contextBlockViews` |
| `$[0]` 的 compact 块、计划、清空上下文 | `marker`。status 映射为 phase；attempts、mode、error 放进 `state` |
| `segment_compact` | `marker`；已完成的取 `model_text = content_text` |
| `file_history_checkpoint` | `marker` |
| error、info、spec 卡片、恢复卡、ask_in_passing、发布结果 | `notice` |
| assistant `text` | `output`，一块对一块。旧数据没有 item 分组，第二段对没有 item id 的相邻 output 沿用现在「合成一个 message item」的做法 |
| `reasoning`、旧 `thinking`、`redacted_thinking` | `reasoning`。签名、`providerMetadata`、`signatureSource` 并入 `provider_meta`。旧数据的 summary 分段已不可恢复，当作单段 |
| `tool_use` + 账本 | `tool`：被采用的 attempt 取最新一次；终态 → `settled`，非终态 → `called`；`thoughtSignature` 并入 `provider_meta.call`。`/bash` 行里那个只给显示用的 `tool_result` 块丢弃 |
| `web_search`、`image_generation` | `tool`，`executor = "provider"` |
| 没有对应块的账本行 | 内部执行（`internal_read_` 前缀）标为 internal，不投影；只有账本的旧数据按现在的向后兼容路径合成 `tool` 块，保持旧行投影出的字节不变 |
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
| P3 | 继续写派生投影（D8）。确认稳定后再单独立项：新行写 `'[]'`，旧行由第二个作业清空 | 清空之前一直可以直接回退；清空之后需要先运行「重组作业」，由块回写 `content_json` |

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
1. `NarratorStore` 成为唯一写入层，22 个写入文件全部迁入，顺带修掉 §5.1 列出的非原子写入，包括 tool_use 块与账本行分两步落库。
2. `shared/narrator-blocks/`：规范块与 part 的 TS 类型和 Zod schema，以及 `decodeLegacyContent`、`deriveEnvelope`。
3. 两段投影（§7.2）：各 provider 只保留第二段；数据仍由 `content_json` 解码而来；loop 改为由同一投影生成每次请求。
4. 先在现有表里落库原来只在内存里的模型可见内容：
   - loop 自造的文本写成 sys 行；
   - 工具结果图片与附言写进账本行的输出结构。
5. 修正现存偏差，见附录 B 第 1–4、21、27 条：
   - 重建时按调用顺序排列工具结果；
   - strict 只由裸 model 名计算一次；
   - 各 provider 的用户图片与工具结果图片按同一规则放置；
   - 段压缩标记的 origin；
   - 内部执行不进模型历史。
6. 测试网：
   - 每种 provider 模式都有「重建 == 实时」等价测试，覆盖 loop 实际使用的 `orderedContent` 路径；
   - 测试多工具结果顺序、broken 调用、mid-pass 注入、当前轮合并；
   - 新旧实现对真实会话的请求做字节 diff：开启 `requestEvidence.autoPin`，比对录制。

**退出条件**
- 生产代码只经 store 写入，有守卫测试，类似 `sqlite-dialect-inventory.guard.test.ts`。
- 所有 provider 只实现第二段；loop 内没有字符串拼接式的历史组装。
- 新旧投影在录制语料上逐字节相同（附录 B 中有意修正的项除外，逐项列出）。

### P1 新表与双写

**交付**
1. schema：
   - 新建 `narrator_blocks`、`narrator_block_assets`；
   - `narrator_messages` 新增 `body_format`、`turn_id`、`step_index`、`author_narrator_id`、`cause_block_id`、`provider_response_id`；
   - `narrator_tool_calls` 新增 `block_id` 与内部执行标记。
2. v2 触发器契约：生成器支持，SQLite 与 PG 各一份。
3. FTS 触发器加列过滤，并带版本号。
4. store 写块表，新信封一律为 `body_format = 2`，同时写派生的 `content_json`。
5. 流式按 provider item 写入（§5.2）；WS v2；按 `attemptId` 丢弃 attempt。
6. tool 块合并调用与结果，采用两段生命周期（§3.5、§3.7）；`state` 写入（§5.4）；`phase` 统一。
7. 只保留一个 COW 实现；作者与属主分开。
8. 前端先切换身份：把 `blockId` 作为 key 和 API 寻址，操作目标按 §5.5；删除流式交接的启发式。

**退出条件**
- 新会话全部是原生块；关闭开关能回到现状。
- 双写校验没有差异。
- 流式期间块表每秒的写入次数，不高于现状的 checkpoint 次数。

### P2 切换读取与回填

**交付**
1. 模型历史 worker、展示页 worker、External API、回退、块删除、导出、project.db 同步、merge summary、存储 GC，都改为读块表。
2. 页面 API 改为规范化形态；前端 store 以块 id 为键；`turn_id` 驱动 recency；`cause_block_id` 驱动通知与工具卡的关联。
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
- 工具执行期间 fork，不再带上正在执行的那一步（§3.5）；
- `input` 与 `context` 按作者区分，系统写的 role=user 行不再带人类的 sender 标记（§3.3）；
- 段压缩标记的 origin（§6.4）；
- broken 调用在重建出的历史里不再出现，改为一条说明（§5.3）；
- 内部执行不进模型历史（§3.7）；
- assistant 编辑只改被点的那一块（§5.5）；
- `aborted` 块的重放规则（§3.5）；
- turn nudge 从工具结果里移出，改为该步工具结果之后的一条独立文本（D12）；
- 改名等派生数据变化，在 compact 之前不再改写已发送的历史，改为追加提醒（§7.7）；
- 跨越投影切换的那次发布，会有一次 prompt cache 未命中（§7.6）。

「轮」的定义一旦落库就很难再改，因此 D3 必须在 P1 之前定下来。

## 13. 验收矩阵

| 场景 | 期望 | 验证 |
|---|---|---|
| 各 provider 投影：Anthropic 官方/中转、Responses HTTP、Codex HTTP/WS、Completions、Gemini generateContent/Interactions、NUG 各通道、TokenDance、插件 | 新旧实现在录制语料上逐字节相同（附录 B 中有意修正的项除外） | 单测 + 请求录制 diff |
| 重建 == 实时 | 同一 pass 内的任一请求，都能由 DB 重建出相同字节。覆盖 broken 调用、mid-pass 注入、当前轮合并、工具结果图片、turn nudge、丢弃的 attempt | 每种 provider 的等价测试 |
| 并行工具 | 结果顺序等于 tool 块的 ordinal，即调用顺序 | 单测 |
| 单元一致性 | 非 assistant 信封恰好一块；`input` 的删除、选择、回滚作用于整块；reasoning run 的操作带整组 id | store 守卫 + 前端测试 |
| assistant 编辑 | 只改被点的 output 块，其他块的 id、位置、revision 不变；恢复不影响其他块 | 单测 |
| 流式 | 块 id 从 `block_opened` 到落库不变；没有合成消息；重连后不重复、不回退 | 前端集成测试 |
| 丢弃 attempt | 只删除该 attempt 的 item 块；已执行的 tool 块保留，并投影为更早的独立一轮 | 单测 |
| 中断与冷启动 | open 的块改为 `aborted` 或 `failed`；`called` 的 tool 不动；按 §3.5 重放 | 单测 + 重启恢复测试 |
| fork | 含 open 或 called 块的信封不共享；惰性 fork 之后，父叙述者修改旧消息会走 COW | 单测 |
| COW | 只有一个实现；作者不变；克隆的账本行不可执行，且 `block_id` 已重映射；mailbox 回执同步更新 | 单测 |
| UI 状态 | `patchState` 不改变 `model_history_revision`，不触发 FTS | 触发器测试 |
| compact 与段压缩 | 生命周期与现状等价；CAS 不再使用 JSON 路径 | 现有 compact 测试迁移 |
| 回退与块删除 | 以块 id 寻址；重跑过的工具也能删除并回退文件；预算按块字节计算；共享信封走 COW | 现有回退测试迁移 |
| Codex WS | 有 assistant 输出之后的下一次请求仍能增量续写 | 用上游原始 item 的录制做测试 |
| External API v1 | 现有契约测试不修改即通过 | 契约测试 |
| project.db | 导出再导入，内容相等；旧归档可以导入 | 往返测试 |
| 搜索 | 结果与现状一致（content_text 规则逐条复刻） | 搜索测试 |
| 回填 | 可中断、可恢复、幂等；校验无差异；主线程单片不超过 1 秒 | 作业测试 + 慢操作日志 |
| 性能 | 流式写入耗时、模型历史加载、页面首屏都不劣于现状 | benchmark |
| 存储 | 在生产库副本上测得的增量落在预估范围内 | 存储扫描 |
| PG | schema 和迁移可以生成，`db:check:pg` 通过 | CI |

## 14. 决策记录与待决问题

| # | 问题 | 结论 | 状态 |
|---|---|---|---|
| D1 | tool 结果的文本是否复制进块 | 不复制。块只存被采用的 attempt、结果图片、附言；文本留在账本 | 推荐，未确认 |
| D2 | tool 块的 `input` 以哪份为准 | 重放实际执行的输入（重定向或修复之后）；账本保留每个 attempt 的执行输入 | **已定** |
| D3 | 「轮」的边界 | 推荐以一次运行为界（§3.6）：插话不开新轮。用途说明后再定 | **待定** |
| D4 | `aborted` 块是否重放 | text 重放，reasoning 不重放 | 推荐，未确认 |
| D5 | 前端传输怎样演进 | P1 先给数组元素附加块 id，P2 再改成规范化结构 | 推荐，未确认 |
| D6 | 是否做块级 FTS | 放在 P3，作为可选项、独立开关；先保留消息级 | 推荐，未确认 |
| D7 | 是否建 `narrator_turns` 表 | P2 视 UI 需求再定 | 推荐，未确认 |
| D8 | `content_json` 的终态 | 当前版本保留派生投影；确认稳定后再单独立项清理（§10.4） | **已定** |
| D9 | 作者与属主是否拆分 | 拆分，新增 `author_narrator_id` | 推荐，未确认 |
| D10 | assistant 编辑的作用域 | 只改被点的那个 `output` 块（§5.5） | **已定** |
| D11 | broken 调用在历史中的形态 | tool 块 `failed`、不可见，另加一条 `context` 说明，与实时路径一致 | **已定** |
| D12 | turn nudge 放在哪里 | 单独一条 `context` 块（subtype `turn_nudge`），排在该步工具结果之后；不再拼进工具结果。这会改变发给模型的内容，列入 §12.6 | **已定** |
| D13 | 引用锚点是否写进模型可见正文 | 保持写在正文里；锚点改为块 id（`b:<blockId>`），COW 后靠 lineage 解析 | **已定** |
| D14 | sender 标记里的标题与用户名在改名后的处理 | compact 之间不改写已发送的前缀，用一条提醒代替；compact 后按最新数据完整重建（§7.7） | **已定** |

## 附录 A：现有块 type 到 kind 的映射

标「待核」的条目，在 P0 实现 `decodeLegacyContent` 时用真实数据确认。

| 现有 type | 常见 role | kind / subtype | 合并方式 | 模型是否可见（现状） | 说明 |
|---|---|---|---|---|---|
| text | user（人写） | `input` 的 `text` part | 与同一行的附件合成一块 | 是 | `model_text` 冻结 `content_text` |
| text | user（origin 为 system） | `context` | — | 是 | 自动续跑、批准反馈、审查检查 |
| text | sys | `context` | — | 是 | plan mode 退出、浏览器 tracing 通知等 |
| text | assistant | `output` 的 `text` part | 一个 item 一块 | 是 | |
| text | disp | `notice` / publication_result | — | 否 | 带 `publicationResult` 字段 |
| text（与注入或 review 配对的那一个） | sys / user | 并入 `context` 块的 `model_text` | 与配对块合成一块 | 是 | |
| image | user | `input` 的 `image` part | 并入 `input` | Anthropic、Responses 原生；其他 provider 为附件提示，重建时丢失 | |
| image | assistant（插件） | `output` 的 `image` part | — | 待核 | |
| text_file | user | `input` 的 `file` part | 并入 `input` | 附件提示 | |
| file_reference | user | `input` 的 `file_ref` part | 并入 `input` | 展开为引用文本 | |
| attachment、document | user | `input` 的 part | 并入 `input` | 待核 | 调研中列出，但未找到写入点 |
| reasoning、旧的 thinking | assistant | `reasoning` | 一个 item 一块 | 按携带策略 | `translatedText` 移入 `state` |
| redacted_thinking | assistant | `reasoning`（redacted） | — | 仅 strict 且同源时 | `data` 移入 `provider_meta` |
| tool_use | assistant | `tool` | 与被采用的账本 attempt 合成一块 | 是，需账本到达终态 | `thoughtSignature` 移入 `provider_meta.call` |
| tool_result | assistant（/bash） | 并入 `tool` | — | 否（只给显示用） | 显示数据改由账本提供 |
| web_search | assistant | `tool`，`executor = "provider"` | — | Responses 原生；Anthropic、Completions 为文本摘要；Gemini 跳过 | |
| image_generation | assistant | `tool`，`executor = "provider"` | 生成的图片为结果 part | 仅 Responses | `result` 只在落盘失败时存在 |
| bash_command | user | `input` | — | 是（经 `content_text` 回退） | `model_text` 为命令原文 |
| tool_loaded、tool_unloaded | user | `context` | — | 是（经 `content_text` 回退） | |
| system_injection 及旧拼写（sidecar、side_car、injection、system-injection） | sys / user | `context` / system_injection | 旧形态与前一个 text 合并 | 是（modelText） | |
| spec_continuation、spec_blocked_continuation、knowledge_hint、container_ready、browser_session_lost、merge_summary | sys | `context` / 同名 | — | 是（modelText） | `merge_summary.isLatest` 移入 `state` |
| subagent_messages、background_agents_completed | 注入的伴随块 | 并入所属 `context` 块的 `body` | 与注入合成一块 | 否 | |
| review_feedback | user（前面带一个 text） | `context` / review_feedback | 与前面的 text 合成一块 | 前面的 text 可见 | `applied` 移入 `state` |
| ask_in_passing | system | `notice` / ask_in_passing | — | 否 | status 移入 `state` |
| subagent_recovery、cwd_recovery | disp | `notice` / 同名 | — | 否 | status 移入 `state` |
| spec_goal_added、spec_fork_carryover、spec_context_cleared | disp | `notice` / 同名 | — | 否 | spec_goal_added 的信封 `cause_block_id` 指向 `/goal` 输入 |
| info | disp | `notice` / info | — | 否 | |
| error | system | `notice` / error | — | 否 | |
| compact（含 subtype plan，以及清空上下文） | system | `marker` / compact、plan、clear_context | — | 否（摘要走 system prompt） | status、attempts 移入 `phase` / `state` |
| segment_compact | user | `marker` / segment_compact | — | 是（现在经 `content_text`） | 改为 `model_text`，origin 为 system |
| file_history_checkpoint | disp | `marker` / file_history_checkpoint | — | 否 | |

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

以下第 18–37 条来自块粒度的第二轮调研（编辑与展示、provider 组装、落库与生命周期三个维度）。标注规则同上。

18. **删除 user 气泡只删 blockIndex 0**（代码推断，未运行验证）。气泡的删除和多选删除都指向 index 0（`vlist-block-target.ts:117-120`；`vlist-selection.ts:216`），而 user 块的持久化顺序是 `[image…, text_file…, file_reference…, text]`（`narrator-session.ts:3195-3221`），所以带附件的消息被删掉的是第一个附件。
19. **reasoning run 的删除与回滚。**
    - 行菜单按 blockIndices 循环调用删除，但 `handleDeleteBlock` 每次都覆盖上一个待删目标，最终只删 run 的最后一块（`use-message-revert-confirm.ts:63-68`；`VListRowInteractionControls.tsx:354-366`；代码推断）。
    - 回滚到 run 用的是起点下标，会截掉 run 的后半段（`narrator-session.ts:6207-6211`；是否有意待核）。
20. **assistant 编辑。**
    - 把所有 text 块合并进第一个，工具之后的文字被搬到工具之前；保留下来的块 id 与 revision 不变；citations 偏移对不上新文本（`narrator-session.ts:6904-6935`；`shared/citations.ts:517`；citations 一点为推测）。
    - 恢复用的是首次编辑时的整数组快照，会丢失之后的删块、翻译、卡片状态，并可能复活已删的工具调用（`narrator-session.ts:7032-7094`；推测）。
21. **内部执行进入模型历史**（推测）。
    - Eval 的内部 Read 只写账本行，toolUseId 以 `internal_read_` 开头（`narrator-persistence.ts:3199-3263`）。
    - 模型历史查询与构造器都不过滤它们，向后兼容路径会把它们补成 tool_use 加结果（`anthropic-provider.ts:3251-3271`；`openai-provider.ts:4033-4042`）。
22. **review 结论行可被拆开。** `[text, review_feedback]` 不在原生块与上下文卡的白名单里，块删除 API 能单独删掉模型文本或卡片（`shared/native-injection.ts:22-30,208-215`）。
23. **编辑菜单不检查 role。** 任何 markdown 行都显示「编辑」，系统或上下文消息里的 text 行点编辑后会被服务端拒绝（`vlist-edit-target.ts:56-59`；`narrator-session.ts:6973-6975`；推测）。
24. **删除并回退文件要求 tool_use 与账本行 1:1**，重跑过的工具会因 EVIDENCE_INCOMPLETE 失败（`revert-planner-local-access.ts:533-553`；推测）。
25. **引用锚点。**
    - `@@m:<id>` 写在模型可见的正文里（`shared/composer-document.ts:226-236`），消息经 COW 换 id 后会失效。
    - L1/L2 下工具折进 activity，`t:` 锚点找不到对应的行（`quote-selection.ts:94-103`；`quote-jump.ts:116-118`）。
    - 两点均为推测。
26. **text 拼接符不一致。**
    - 编辑器用 `\n\n`，user 气泡用 `\n`，composer 校验用 `""`，恢复后的 content_text 用 `\n\n`（`message-edit-text.ts:28`；`segment-adapter.ts:1772-1775`；`server/lib/composer-document.ts:95-145`）。
    - Responses 重建 user 文本用 `""`，其他构造器用 `\n`（`openai-provider.ts:3613-3686`）。
    - Completions 的 assistant 文本实时用 `""`，重建用 `\n`（`openai-provider.ts:1173-1176,4168`）。
27. **历史图片在重建中丢失。** Chat Completions、Gemini、Interactions、插件的重建路径只用 `content_text` 或丢弃只有 imageId 的块（`openai-provider.ts:4276`；`gemini-provider.ts:770-776`；`gemini-interactions-provider.ts:775-781`；`remote-provider-adapter.ts:1311-1325`）。工具结果中的图片在所有 provider 的重建路径都丢失，因为它们只存在于内存（`loop.ts:7278-7286,7454-7460`）。
28. **Gemini。**
    - functionResponse 不带 id，按名称加顺序配对，同名的并行调用可能配错（`gemini-provider.ts:758-769`；推测）。
    - 普通 text part 的签名在解析时被丢弃（`gemini-provider.ts:917-985`）。
29. **Responses。**
    - 多段 summary 落库时被压成一段（`openai-provider.ts:2288-2303`）。
    - function_call 的 item id 与 message 的 phase 都不落库。
    - image_generation 重建时在主线程 `readFileSync`，并插入一条只在重建路径出现的 developer 指令（`openai-provider.ts:3989-4011`）。
30. **Codex WS 增量续写几乎总是退化**（推测）。续写要求逐项 JSON 前缀相同，而本地重新序列化的 item 缺少 `status`、`annotations`、`id`、`phase`（`codex-websocket.ts:259-292,741-773`）。现有测试只用简化过的 item 验证（`codex-websocket.test.ts:72-85`）。
31. **NUG 的 OpenAI 系通道在 `buildHistory` 中的 strict 判定恒为 false**，与第 2 条同源（`nug-provider.ts:270,383-397`）。
32. **插件 provider**（推测）。
    - 不看账本状态，发出全部 tool_use，未完成的调用没有结果（`remote-provider-adapter.ts:1181-1202`）。
    - 不过滤 `parentToolUseId`。
    - 保留 role=system 的旧行。
33. **当前轮的拼接顺序与 DB 顺序不同。** 尾部 sys 文本放在用户输入之前，知识注入拼在之后（`agent-runtime/history.ts:238-243`；`orchestrator.ts:1976-2007`），到下一个 pass 重建时，分段和顺序都会变。
34. **Gemini 保留键在跨 provider 时泄漏**（推测）。账本 `input_json` 里的 `__geminiThoughtSignature` 在 Anthropic、OpenAI 重放时不剥离，会进入工具输入（`anthropic-provider.ts:3228-3238`；`openai-provider.ts:4028,4232`）。
35. **工具 id 改名有两套算法。** 实时用 `reserveUniqueToolUseIds`，重建用 `uniquify`；同一 id 多次出现时，两边可能改出不同的名字（`tool-use-id-dedup.ts:258-337,409-428`；推测）。
36. **`/bash`。**
    - user 行经 `content_text` 对模型可见，但上下文统计按 0 字符计（`context-characters.ts:131-133`）。
    - assistant 行里的 tool_result 块只给显示用，删除时不与 tool_use 配对，可能留下孤立的块（`narrator-messages.ts:647-655`；推测）。
37. **子代理收到的消息。** 删除那个无 modelText 的注入，会连带删掉 text，却留下图片和文件（`shared/native-injection.ts:168-171`；推测）。

## 附录 C：块粒度的推导

本附录记录 §3 的切分是怎样得出的。证据来自三份只读调研（编辑与展示、provider 组装、落库与生命周期），以及业界做法。

### C.1 四个维度各自需要的单位

| 维度 | 用户输入 | assistant 可见文本 | 推理 | 工具 | 注入与卡片 |
|---|---|---|---|---|---|
| 落库 | 整条一次写入（`narrator-persistence.ts:1238-1316`） | 按 provider item 流式产生、各自完成（`output-content.ts`） | 同左；凭据在 item 结束时到达 | 调用在流式中写入；结果可能在数小时后、跨重启才写入；被采用的结果会变 | 整条一次写入 |
| 编辑 | 整条：编辑器里可以逐个增删附件，提交时整条重建 | 入口是单块，现在的语义却是整条合并（附录 B 第 20 条） | 不可编辑；翻译按块 | 不可编辑；重跑、作答作用于 attempt | 整条删除；状态原地修改 |
| 展示 | 一个气泡，附件按占位符嵌入正文 | 每块一行 | 相邻块组成 run，再拆成步骤 | 一张卡：调用 + 被采用的输出 + 流式状态 + 权限 + 子代理 | 一个逻辑注入一张卡 |
| 组装 | 一个 user 轮的内容；块内按 part 映射为文本、图片、附件提示 | 每个 provider item 一个线上单元，凭据挂在 item 上 | 同左；跨 provider 时可被单独丢弃或降级 | 调用进 assistant 轮，结果进下一个 user 轮；必须成对 | `model_text` 进 user 轮或 system |

四个维度在下面几处一致：
- 用户输入是一个单元；
- assistant 的每个输出 item 是一个单元；
- 工具调用连同其结果是一个单元；
- 一次注入是一个单元。

比单元更细的结构（附件、推理分段、item 内的 content part）只有组装和展示需要，而且只作为内容使用，没有任何独立的写操作。比单元更粗的结构（步、轮、run、activity）由信封和写入时盖章的字段表达。

### C.2 候选方案

| 方案 | 落库 | 编辑 | 展示 | 组装 | 结论 |
|---|---|---|---|---|---|
| A. 现状：消息 = JSON 数组 | 每个 checkpoint 重写整列，并触发四组触发器 | 用下标作身份，删、改、回滚各自修补 | 流式交接、配对、轮次全靠推导 | 每个构造器各自解析；实时与重建有 19 处差异 | 要换 |
| B. 线上 part 粒度（本文初稿）：text、image、attachment、tool_use、tool_result 各一行 | 按 part 写，粒度最细 | 用户输入被拆成多块，又得另造一个「整条」身份；tool_use 与 tool_result 分两行，配对要额外维护 | 气泡要从多块重组，工具卡要拼两行 | 贴近 Anthropic；Responses、Gemini 要重新分组；配对靠约束保证 | 不选。初稿还有一个漏洞：tool_result 在信封封口后才追加，可能改到已被 fork 共享的信封 |
| C. 按角色合并：user_input、assistant_output（一步一块）、tool | 一步的全部文字合成一块，流式又回到整段重写 | 用户输入、工具没问题；assistant 只能整步编辑 | 用户输入、工具没问题；assistant 的每个 item 还要再做子寻址 | 凭据与携带策略得沉回块内数组；文字与工具交错时无法表达 | 不选，但其中「用户输入一块、工具一块」被采纳 |
| D. 单元粒度 + part（推荐） | 每个 provider item 一行；工具调用与结果一行，另有账本 | 每个操作都有对应的块；无效结构（只有图片的轮、没有结果的调用）不可表达 | 视觉单元与块一一对应；run、activity 由盖章字段分组 | 两段投影；凭据挂在所属块上；配对由结构保证 | 采用 |
| E. 事件溯源：只追加事件 + 物化视图 | 天然适合流式与审计 | 编辑是事件，历史完整；但 COW 与 fork 语义变复杂 | 仍需物化成 D 的形态 | 同 D（建在物化视图上） | 不选。比 D 多一层，现有需求用不上；编辑历史可以用块 revision 表单独实现（P3） |

### C.3 业界做法

- **Vercel AI SDK（`UIMessage`）**：与本设计的 D 基本同构。
  - 消息由 part 组成。text 与 reasoning 是不同的 part，各带流式状态和 `providerMetadata`。
  - 一次工具调用是**一个** part，状态依次为 `input-streaming`、`input-available`、`approval-requested`、`approval-responded`，最后是 `output-available`、`output-error` 或 `output-denied` 之一。
  - 调用侧和结果侧各有一份 provider 元数据；`providerExecuted` 标记由 provider 执行的工具；`step-start` part 标记步边界。
  - 存储与展示用这套结构，发给模型之前再转换成各家格式。
- **TanStack AI**：工具调用与结果是两个 part，但结果会再复制一份挂到调用 part 上。这说明展示层需要的仍是合在一起的形态。
- **OpenAI Responses**：`function_call` 与 `function_call_output` 是两个 item，因为它是只追加的日志，两者由不同的一方追加。本设计的存储不是只追加日志（信封可变，靠 COW 保护），这个理由不适用；线上格式由第二段投影负责。
- **工具结果中的图片**：
  - Anthropic 的 tool_result 可以带图；
  - Responses 自 2025 年 9 月起，允许 `function_call_output` 的 output 是含 `input_image` 的数组；
  - Gemini 3 支持多模态函数结果，旧模型会返回 400；
  - Chat Completions 不支持。

  因此工具结果在存储中应该是「文本 + 图片 part」，放到哪里由第二段按协议能力决定。
- **NarraFork 自己的 External API v1**：已经按「消息 → 文本、推理步骤、工具条目（调用与结果合一）、子代理」的结构输出（`docs/OPEN_API.md` §7.6）。

来源：
- [AI SDK `ui-messages.ts`（ai@7.0.40）](https://app.unpkg.com/ai@7.0.40/files/src/ui/ui-messages.ts)
- [AI SDK UIMessage 参考](https://ai-sdk.dev/v5/docs/reference/ai-sdk-core/ui-message)
- [TanStack AI Tool Architecture](https://tanstack.com/ai/latest/docs/tools/tool-architecture)
- [OpenAI 社区：Images and files as function call outputs](https://community.openai.com/t/images-and-files-as-function-call-outputs/1360081)
- [Gemini 3 Pro 模型页（Vertex AI）](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/3-pro?hl=hi)
