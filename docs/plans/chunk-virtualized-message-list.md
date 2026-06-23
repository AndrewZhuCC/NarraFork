# 叙述者消息列表 Chunk 虚拟化重构方案

## 背景与目标

当前消息列表的"滚动加载更旧"不可靠，根因是两层割裂的加载机制叠加：
- 数据分页层（TanStack `useInfiniteQuery`，首页 20 / 后续 50，newest-first 多页 + around 窗口）
- 渲染窗口层（`MESSAGE_RENDER_WINDOW`，元素数 > 260 时只渲染 180 元素的滑动窗口）

两层各有"还有更多/加载更旧"触发器并互相抑制，且 `shift={isFetchingNextPage}` 的滚动补偿门控时序错误（fetch 结束与元素增多不在同一帧），导致加载更旧时视口跳动、窗口整窗跳转丢失阅读位置。

**目标**：用"Minecraft 式 chunk 网格 + 距离预加载"统一为单一模型。chunk 按最早消息起固定切分，每条消息固定归属一个 chunk，随滚动动态挂载视口附近 ±3 个 chunk、卸载远处 chunk。位置稳定性依赖浏览器原生 scroll anchoring（用户已实测多浏览器可行，前提是加载/卸载不发生在视口顶端/底端——±3 缓冲保证此前提）。

## 已确定的设计决策（不再留白）

1. **chunkSize = 20**，按**顶层消息**计数（`parentToolUseId IS NULL`；子代理/工具结果作为 children 不计入）。
2. **render-chunk 与数据页解耦**：后端只按 seq 提供连续顶层消息，前端在已加载窗口上统一分段后按安全边界打包 chunk。
3. **chunk.id = 该 chunk 首条消息的 messageId**。messageId 永不变（compact 只改 `seq` 不改 id，fork 重新编号但属另一叙述者），故 chunk 身份稳定。
4. **脏判定指纹 = `hash(firstSeq, lastSeq, count, lastMessageId)`**。覆盖插入/删除/重排（这些必然改变区间或边界 id）。**正文编辑**不改变结构，走现有 `editedAt` + `message_updated` WS 事件单独处理。
5. **预加载距离 = ±3 个 chunk**（中心 chunk 两侧各 3，共 7 个 chunk ≈ 140 条顶层消息常驻 DOM，与现状 180 元素量级相当）。
6. **不需要数据库迁移**：已确认 `narrator_messages` 无逐行 `updatedAt`，但有 `editedAt`（`schema.ts:398`）；chunk 为动态视图不入表；指纹与 manifest 全部基于现有列计算。本方案不修改 `server/db/schema.ts`，不运行 `db:generate`。

## 跨 chunk tool-run 处理（核心正确性）

`segmentMessages`（`message-segments.ts:327-352`）会把**连续的 tool 原子跨消息**合并成一个 `tool-run` 段。合并仅发生于"消息 P 末原子是 tool 且消息 Q 首原子是 tool"。其它情况（Q 是 user/system，或 Q 以文本/推理开头）都是安全断点；每个对话回合通常以 user 消息收尾，安全断点至少每回合出现一次。

> 既有缺陷：当前 `flatElements` 逐数据页 `renderTreeMessagesWithKeys(page.messages)`（`NarratorPanel.tsx:4674`），跨页 tool-run 已被切断。新方案顺带修复。

**处理规则**：
- 前端把已加载的连续 chunk 拼成按 seq 升序的单一数组，对**整个已加载窗口**跑一次 `segmentMessages`（~140 条，开销极小），窗口内跨消息 tool-run 正确合并。
- 将段序列按"目标 20 条顶层消息、**只在安全段边界切**"打包成 render-chunk。任何 tool-run 不跨 render-chunk。
- **元素数硬上限兜底**：若某回合出现海量 tool-only 消息（长自主运行、无 user 介入）使安全边界撑出超大 chunk，设硬上限（2× 目标元素数）；超限时允许在 tool-run 内切一次，接受该处视觉断开。极端兜底，正常不触发。

---

## 后端改动（契约级）

文件：`server/services/narrator-messages.ts`（新增方法）、`server/routes/narrators.ts`（新增路由）、`server/websocket/narrator-ws-types.ts` + `narrator-ws.ts`（新增事件）。**不改 schema。**

### 端点 1：`GET /narrators/:id/chunk-manifest?since=<version>`

轻量指纹清单，只读窄表，不读大 JSON。

```ts
// narratorService.getChunkManifest(narratorId, sinceVersion?)
// → { unchanged: true } 当 since === 当前 messageVersion（最常见短路）
// → {
//     messageVersion: number,
//     total: number,                 // 顶层消息总数
//     chunks: Array<{
//       id: string,                  // = firstMessageId
//       firstSeq: number,
//       lastSeq: number,
//       count: number,               // ≤ 20
//       hash: string                 // hash(firstSeq,lastSeq,count,lastMessageId)
//     }>
//   }
```

实现要点（守 CLAUDE.md 主线程红线）：
- 单条查询：`SELECT r.seq, r.message_id FROM narrator_message_refs r INNER JOIN narrator_messages m ON r.message_id = m.id WHERE r.narrator_id = ? AND m.parent_tool_use_id IS NULL AND r.segment_compact_id IS NULL ORDER BY r.seq`（走 `idx_narrator_refs_seq`，仅窄列，无大字段，无 `COUNT(*)`）。
- 内存中每 20 条切一组算指纹。数千条 = 一两百 chunk，JSON 仅几 KB。
- `since` 短路：先读 `narrators.messageVersion`，相等直接返回 `{ unchanged: true }`，零 chunk 计算。

> 注：manifest 的 chunk 边界用"纯 20 条等分"（不含安全边界逻辑），仅用于**总高度估算与对齐**；真正的渲染 chunk 边界由前端分段后决定。两者解耦：manifest chunk 用于 spacer 高度记账和脏标记，render-chunk 用于实际 DOM。详见前端"双层 chunk"说明。

### 端点 2：`GET /narrators/:id/chunks?fromSeq=<n>&direction=older|newer&count=<k>`

内容接口，复用现有树构建。

```ts
// narratorService.getChunksByRange(narratorId, fromSeq, direction, count)
// → {
//     messages: NarratorMsg[],   // 连续顶层消息（含 children 树），每条带 seq，按 seq 升序
//     minSeq: number, maxSeq: number,
//     hasOlder: boolean, hasNewer: boolean,
//     messageVersion: number
//   }
```

实现要点：
- 取 `direction` 方向上约 `count × 20` 条顶层消息（`count` 默认 7）。`older`：`seq < fromSeq` 倒序取后反转；`newer`：`seq > fromSeq` 正序；`fromSeq` 省略表示取最新尾部。
- 复用 `getMessagesCursor` 已有的子树加载与 `buildMessageTree` → `enrichToolUseBlocks` → `truncateToolIO` → `filterExitPlanBeforePlanCompact` 管线（抽出公共函数，避免复制）。
- 用 `LIMIT n+1` 判断 `hasOlder/hasNewer`，不 `COUNT(*)`。
- 返回扁平 `messages`（前端自行分段打包），不在后端分组。统一替代现有"20/50/around"三套口径，前端不再需要 `getRenderableMessageOrder` 修 seq 回归。

### WS 事件：`chunks_dirty`

`narrator-ws-types.ts` 新增：
```ts
| { type: "chunks_dirty"; narratorId: string; messageVersion: number;
    dirtySeqRanges: Array<{ fromSeq: number; toSeq: number }>;
    reason: "insert" | "delete" | "compact" | "edit_structural" }
```

广播时机与脏范围来源（变更操作本身已知 seq，无需全表扫）：
- 中段插入（`insertSystemMessage` 带 `beforeMessageId`，`narrator-persistence.ts:284`）：脏范围 = `[targetRef.seq, MAX]`（其后整体 +1 后移）。
- 删除及其后（`deleteMessage`，`narrator-messages.ts:1486` 删除 `seq >= targetRef.seq`）：脏范围 = `[targetRef.seq, oldMAX]`。
- compact（`narrator-service.ts:1388` 等）：脏范围 = compact 边界 seq 起至 MAX。
- 普通**尾部追加**：**不发** `chunks_dirty`，继续走现有 `message` 事件，前端插入尾部 chunk。
- 正文编辑（`message_updated`，`routes/narrators.ts:2322`）：结构不变，**不发** `chunks_dirty`；前端按 id 原地替换该消息所在 chunk 的内容并重测高度。

前端收到后：拉 `chunk-manifest?since=本地version` 做 diff（或直接用 `dirtySeqRanges` 映射到已加载 render-chunk），**只对受影响且已挂载的 chunk** 调 `getChunksByRange` 重取，其余 chunk 缓存与 DOM 不动。无法增量时回退 `full_reload`。

---

## 前端改动（模块级）

### 双层 chunk 概念澄清

- **manifest-chunk**（后端纯 20 等分）：用于"我知道总共有哪些 chunk、各自估算高度、哪些脏了"——驱动 spacer 高度记账与对齐。
- **render-chunk**（前端分段后按安全边界打包）：实际挂载/卸载的 DOM 单元，保证 tool-run 不被切断。

二者通过 seq 区间互相映射。manifest-chunk 提供稳定的"全局坐标系"，render-chunk 提供"安全的 DOM 边界"。

### 新增 hook：`useNarratorChunks(narratorId, { aroundMessageId? })`

替代 `useNarratorMessages`。职责：
- 初始：`getChunkManifest` 拿全量 manifest（总数/各 chunk 指纹/估算高度种子）；`getChunksByRange` 取中心 ±3（尾部或 around 目标处）。
- 维护 `loadedChunks: Map<chunkId, { messages, measuredHeight? }>` 与 `manifest`。
- 暴露：`renderChunks`（已分段打包的可渲染 chunk）、`mountedRange`、`loadOlder/loadNewer`、`hasOlder/hasNewer`、`jumpToSeq(seq)`、`reconcileDirty(ranges)`。
- 内部对已加载连续窗口跑 `segmentMessages` 并打包 render-chunk（含硬上限兜底）。

### Chunk 窗口状态机

- 状态：`centerChunkIndex`、`mountedChunkIds`（中心 ±3）。
- 触发：用 `IntersectionObserver` 观察每个已挂载 render-chunk 容器与上下 spacer 的哨兵。进入预加载圈（距中心 ≤3）→ 确保该 chunk 已取数并挂载；离开（距中心 >3）→ 卸载 DOM（数据保留在 `loadedChunks` 缓存，LRU 上限控制内存）。
- 替代现有手搓的 `loadOlderArmedRef`/`loadOlderNearTopRef`/scroll 意图判断（`NarratorPanel.tsx:5320-5398`、`5400-5483`），改用 IO 哨兵（更稳，且正是 broad-infinite-list 被 disable 掉的能力）。

### 三段式高度结构（spacer）

```
[顶部 spacer: Σ 上方未挂载 manifest-chunk 的高度]
[挂载区: 中心 ±3 render-chunk 真实渲染]
[底部 spacer: Σ 下方未挂载 manifest-chunk 的高度]
```
- `chunkHeights: Map<chunkId, number>`：挂载时 `ResizeObserver` 测真实高度存入；卸载后复用（旧消息高度冻结，误差≈0）。
- 未测过的 chunk 用估算（按 count × 角色经验值分档）。
- spacer 高度 = 对应侧所有未挂载 chunk 高度（实测优先，回退估算）累加。
- **位置补偿交给浏览器 anchoring**：挂载/卸载发生在视口 ±3 之外，anchor 节点在视口内，上方高度变化由浏览器自动抵消。前端不写 `scrollTop` 补偿。需确保滚动容器祖先链无 `overflow-anchor: none`（现 broad-infinite-list 在 1px 哨兵上设了，替换后移除）。

### 自定义滚动条组件 `<ChunkScrollbar>`

职责严格限定：
- **仅显示 + 跳转输入**，不参与滚动位置补偿。
- 位置/长度由 **chunk index 模型**驱动（"第 N / 共 M 个 chunk"），而非像素 `scrollTop/scrollHeight`，从而在"估算→实测"高度修正时不抖动。
- 拖拽/点击轨道 → 换算目标 chunk → `jumpToSeq`（类似 around 跳转：设新中心、取该带 chunk、`scrollIntoView` 目标）。
- 复用现有 `ScrollbarUserMarkers`（`NarratorPanel.tsx:7729`）的用户消息标记，迁移到按 chunk/seq 定位。

### 与现有集成点的适配（逐一）

| 集成点 | 现状 | 适配 |
|---|---|---|
| 跟随底部流式 `startFollowing` | RAF 跟随 `scrollBottom`（`NarratorPanel.tsx:2958`） | 保留。底部 chunk 永远挂载（中心=最新时），流式增高在视口内，anchoring 不介入，跟随逻辑不变。`isTailRenderWindowRef` 改为"中心 chunk 是否为最后一个 chunk"。 |
| around 深链 / `highlightMessageId` | `aroundOptions` → around 查询（`NarratorPanel.tsx:2035`） | `useNarratorChunks` 接受 `aroundMessageId`，初始取目标 chunk 的 ±3，`jumpToSeq` 定位 + 高亮。`scrollToFullIndex`（`5120`）改为 `jumpToSeq` 语义。 |
| 搜索跳转 + 用户标记 | `scrollToMessageTarget`/`targetIndexMap`（`5624`/`5068`） | 先试 DOM 命中（已挂载则直接 `scrollIntoView`）；未挂载则 `jumpToSeq` 到目标 chunk 再滚动。`targetIndexMap` 改为 messageId→seq 映射。 |
| compact/prune 分隔 | `segmentMessages` 产出 `prune-divider`，compact 标记消息 | 分段在窗口级统一进行，分隔符自然落在对应 render-chunk 内，无需特殊处理。compact 触发 `chunks_dirty(reason: compact)` 增量重取。 |
| 乐观发送 | `firstPage.messages` 插入 optimistic（`useNarratorPanelWS.ts:1614`） | 改为插入"尾部 render-chunk"；服务端 `message` 事件回来按 contentText 匹配替换。尾部 chunk 始终挂载，逻辑等价。 |
| 缓存淘汰 `MAX_LIVE_MESSAGES=200` | `evictOldestPages` 按页淘汰（`message-tree-utils.ts:919`） | 改为 `loadedChunks` 的 LRU（按 chunk 数上限，如保留最近 30 个 chunk 的数据），卸载只去 DOM 不丢数据；超 LRU 才丢数据，需要时重取。 |
| WS reconnect catch_up/full_reload | `onFullReload`→invalidate（`useNarratorPanelWS.ts:2981`）、catch_up 增量 | reconnect 时先 `chunk-manifest?since=version`：`unchanged` 则无操作；否则按 diff 增量修脏 chunk，替代全量 invalidate。`full_reload` 仍保留为兜底（重置 manifest + 重取中心带）。 |
| 尾部追加 `message` 事件 | 插 `firstPage`（`useNarratorPanelWS.ts:1535`） | 插入尾部 render-chunk + 更新 manifest 末项指纹/高度；`isAtBottom` 时跟随。 |

---

## 分阶段实施与验证门

### Phase 0：最小垂直切片（验证不确定性，feature flag 后并存）

- 后端：实现 `getChunkManifest` + `getChunksByRange` 两端点。
- 前端：在 feature flag 下，新增一个**裸 chunk 列表**（三段式 spacer + IO 挂载/卸载 + **原生滚动条**，不含自定义滚动条、不含 chunks_dirty 增量）。与现有 `NarratorPanel` 列表通过 flag 切换并存。
- **必须实测的两项**：
  1. `getChunkManifest` 在数千条消息叙述者下的查询耗时（确认窄表扫描达预期、不阻塞主线程）。
  2. ±3 挂载 7 个 render-chunk 在**最坏情况（含大子代理树的 Agent 调用）**的 DOM 节点量、挂载耗时、以及**浏览器 anchor 实测**——含 **iOS Safari 真机**（这是手头资料"WebKit 不支持 overflow-anchor"与用户实测"多浏览器可行"的唯一冲突点，必须真机确认；若 iOS 真不稳，则该平台需 JS 补偿兜底方案，在此门决策）。
- **验证门**：上述达标（manifest 毫秒级、DOM 量可接受、目标浏览器 anchor 不跳）才进 Phase 1。

### Phase 1：自定义滚动条 + 完整窗口状态机

- 实现 `<ChunkScrollbar>`、`jumpToSeq`、用户标记迁移、分段打包（含安全边界 + 硬上限兜底）。
- 接入跟随底部、around 深链、搜索跳转、乐观发送、LRU。

### Phase 2：增量对齐

- 后端 `chunks_dirty` 事件 + 各变更点脏范围广播。
- 前端 `reconcileDirty` + reconnect 走 manifest diff，移除全量 invalidate（保留 `full_reload` 兜底）。

### Phase 3：清理

- feature flag 默认开启、灰度观察后移除旧渲染窗口（`MESSAGE_RENDER_WINDOW`、`shift` 补偿、手搓 scroll 监听、`getRenderableMessageOrder` 异常修正）与 `BroadMessageList` 依赖（如不再使用）。

## 风险与回滚

- **feature flag 并存**：核心脆弱区，全程保留新旧两套列表，可即时切回旧实现。flag 建议放 `userPreferences`（与 `autoLoadOlderMessages` 同处）或本地存储，便于按用户灰度。
- **anchor 平台差异**：iOS 在 Phase 0 门验证；若不达标，该平台启用最小 JS 锚点补偿（仅上方插入帧），不影响桌面路径。
- **seq 重排事件**（compact/fork）：本就触发结构版本变化，按 `chunks_dirty`/`full_reload` 处理，不破坏 chunk 身份（id 用 messageId）。
- **大子代理 chunk**：Phase 0 实测 DOM 量；必要时对超重 chunk 内部再做懒展开（子代理卡片折叠态）。

## 验证手段

- 静态：`bunx @biomejs/biome check .`、`bunx tsgo --noEmit`（白名单命令）。
- 单测：分段打包函数（安全边界、硬上限）、manifest 指纹 diff、seq 区间→chunk 映射，加 `*.test.ts`。
- 手测场景清单：
  1. 长会话（数千条）向上连续滚动到顶，无跳动、无空洞。
  2. 快速 fling 上滚/下滚，缓冲是否够（±3）、是否触空白。
  3. 底部流式跟随 + 中途上滚脱离 + 点"回到底部"。
  4. around 深链打开到中段消息并高亮。
  5. 搜索跳转到未挂载的远处消息。
  6. 中段删除/compact 触发增量重取，未涉及 chunk 不动。
  7. WS 断线重连后 manifest 短路或增量修复。
  8. 乐观发送→服务端确认替换。
  9. iOS Safari 真机滚动 + 加载。

---

# 修订记录与当前分阶段计划（2026-06，权威版本）

> 上方原始 Phase 0–3 部分为早期设计，部分已被后续决策取代。**本节为当前权威计划，subagent 必须以本节为准。**

## 已完成（不要重做）

- **Phase 0 切片**：后端 `getChunkManifest` + `getChunksByRange` 两端点；前端 feature flag（localStorage `narrafork_chunk_list`，设置页 Agent→调试区开关）；裸 chunk 列表骨架。
- **路线 1（manifest 驱动稀疏加载）**：`useNarratorChunks` 改为 manifest 全集 + `Map<chunkId, messages>` 稀疏加载 + `ensureLoaded`；`ChunkedMessageList` 用前缀和 spacer 覆盖全集、二分定位滚动跳转、center 用 chunkId 锚定抗 prepend 漂移、哨兵+scroll 兜底。**滚动/拖拽/加载已可靠。**
- **性能优化**：manifest 查询对 primary 叙述者跳过 JOIN（child refs 恒为 0），126ms→23ms；服务端按 `(narratorId, messageVersion)` 的 LRU(64) 缓存 manifest；payload 元组化 `[id,firstSeq,lastSeq,count]` 去掉 hash 字段，134KB→~50KB。off-thread 评估后降级（25ms 一次性，不上 worker）。

## 终局目标

chunk 模式**完全替代** Query 分页模式，最终删除 `useInfiniteQuery` 分页（`fetchNextPage`/`fetchPreviousPage`）和 `BroadMessageList` 路径。chunk 数据层（`useNarratorChunks`）成为消息的**唯一真相源**，自己接 WS，Query 缓存完全不参与消息存储。

## 核心架构决策（彻底派）

- chunk 数据层自管 WS：把 `useNarratorPanelWS` 的全部消息更新逻辑在 `Map<chunkId, TreeMessage[]>` 上重新实现。
- **尾部 chunk 常驻**（不卸载），作为流式/新消息/工具完成的落点。
- 每个 chunk 的 `messages[]` 按 seq 升序；跨 chunk 按 seq 有序。
- **结构性变更**（中段插入 compact/ask_in_passing、删除、segment-hide、compact_done）→ 视为 **structural dirty**：刷新 manifest（`?since=messageVersion`）+ 重取受影响/中心 chunk，而非朴素 in-place 改（会让 seq 与 manifest 失同步）。`full_reload` 为硬兜底。
- WS 消息更新规格见“WS message-data mutation spec”（已由 subagent 提取，下方“规格要点”浓缩；完整逻辑须对照 `useNarratorPanelWS.ts` 行号）。

## 规格要点（WS → chunk Map 的映射契约）

数据模型对应：旧 `pages[0].messages` 尾部 ≈ 新“尾部 chunk 的 messages 数组”。“insert by seq into pages[0]” → “insert by seq into 拥有该 seq 的 chunk；超出末 chunk lastSeq 的全新尾部 seq 则扩展尾部 chunk + manifest 末项 lastSeq/count”。

可**原样复用**（纯数组/树操作）：`insertTopLevelMessageBySeq`、`preserveLiveSideCars`、`appendSideCarsToLatestAssistant`、`preserveCompleteStreamedOutput`、`appendStreamingTextPreview`、`buildStreamingMsg`、`upsertStreamingToolBlock`，以及 message-tree-utils 的 DFS 形式 `mergeToolCallFieldsInTree`/`updateToolCallInTree`/`findMsgByToolUseIdInTree`/`insertChildIntoMessages`/`removeStreamingChildInMessages`/`upsertStreamingChildInMessages`。

需**适配**（`{pages}`+`MessageIndex` 绑定）：`insertChildIntoCache`/`removeSubagentStreamingChunk`/`upsertSubagentStreamingChunk`/`mergeFieldsByIndex`/`updateToolCallByIndex`/`mergeAtPath` — 最廉价适配：把目标 chunk 包成 `{pages:[{messages: chunkArr}]}` 调用再解包；或直接用其 DFS 兜底形式在 chunk 数组上跑。需建 `toolUseId → chunkId` 索引扫描已加载 chunk。

**丢弃**：`evictOldestPages`（改 chunk-LRU，只卸 DOM，超 LRU 才丢数据）；`getRenderableMessageOrder`/`hasRenderableMessageOrderAnomaly`（chunk 原生 seq 有序）。

保留的非消息副作用：未读计数（§2C 非贴底且 assistant 才 +1）、catch-up 后贴底自动滚、streamingVersion/topLevelChunksVersion 与 synthetic-strip 同批 flush、messageVersion 用 manifest/range 的版本播种 `narratorWSManager.updateMessageVersion`。

## 分阶段执行（每阶段独立 commit + flag 实测 + 可回退）

### 阶段 1（地基）：chunk 数据层尾部活跃 + WS 接入骨架 + 新消息 + 流式
- 尾部 chunk 常驻不卸载。
- WS 订阅 + catch-up：subscribe(fullSubscribe)、catch_up/sync_ok/full_reload、lastMessageId 锚点（=尾部 chunk 末消息深层末子，跳过 synthetic；around/jumped 态抑制）。
- `scheduleCacheUpdate` 的 chunk 版：批量 updater 折叠后 `setState(loaded)`（新 Map + 仅改动 chunk 的新数组），不写 query cache。
- 实现：新消息追加（§2C 决策树：synthetic-strip、id-dedupe via preserveLiveSideCars、乐观替换 3 规则、insertTopLevelMessageBySeq、unread、needsStreamingVersionBump 同批 flush）；user_message（§3 三匹配规则）；流式注入尾部 render-chunk（§6：streamingBlocksRef + topLevelStreamingChunkRef + topLevelStreamingChunks memo + buildStreamingMsg + STREAMING_CHUNKS_MSG_ID 落在尾 chunk）。
- **代码组织**：WS 逻辑拆独立文件（如 `useNarratorChunksWS.ts` 或 `narrator-chunks-ws.ts`），`useNarratorChunks` 调用之，保持可维护。
- **验证门**：活跃叙述者实时显示新消息 + 流式文本/推理输出；发送消息乐观显示并被服务端确认替换。

### 阶段 2（工具与子代理）：tool_completed / tool_use_chunk / 子代理树 / reflection
- 建 `toolUseId → chunkId` 索引；tool_completed（updateToolCallByIndex + mergeFieldsByIndex + preserveCompleteStreamedOutput）；tool_use_chunk（subagent→upsertSubagentStreamingChunk，top-level→topLevelStreamingChunkRef）；子代理消息（insertChildIntoCache/removeSubagentStreamingChunk）；各 reflection（danger/plan/goal/question）+ permission_request/resolved + tool_started/long_running/output/timeout + subagent_started/conclusion + background_task_*。
- **验证门**：工具卡实时更新状态/输出/时长，子代理消息树正确，权限审批 UI 正常。

### 阶段 3（结构变更）：编辑/删除/compact → chunks_dirty 失效重载
- `message_updated`（按 id 合并保留 children）；`messages_deleted`（结构 dirty）；`segment_compact_hide`；`compact_done`。
- 实现 manifest diff 增量：`getChunkManifest?since=version` → unchanged 短路 / 重取脏 chunk + 中心带；full_reload 硬兜底（重置 manifest + 重取尾部）。
- **验证门**：编辑/删除/压缩后 chunk 正确刷新，seq 不错乱，未涉及 chunk 不动。

### 阶段 4（交互回调 + 滚动 + 多选）
- 透传 ~20 回调（fork/compactBefore/clearContextBefore/manualSummarize/deleteBlock/rollback/editAndRegenerate/editAssistant/restoreAssistant/onViewSubagentSession/onAskInPassing/resolvePerm）+ showTokenUsage/highlightedId/expandedToolUseId/pruneBoundaryMessageId/pruneDividerLabel/lastUserMessageId 到 renderTreeMessages（注意 enableBlurIn 传 false 与旧路径一致）。
- 跟随底部（startFollowing/stopFollowing 等价）、isAtBottom、未读计数、滚到底按钮；深链 highlightMessageId（around 初始定位）、scrollToMessageTarget 等价、ScrollbarUserMarkers、搜索/todos 跳转。
- 多选/批量：暴露稳定 contentRef；批量操作改走 chunk 数据层；处理虚拟化下跨未挂载 chunk 的选择。
- **验证门**：逐项对齐旧列表交互。

### 阶段 5（收尾）：删除 Query 分页模式 + 旧 BroadMessageList 路径；flag 默认开启后移除。

## 验证手段（每阶段）
- 静态：`bunx @biomejs/biome check .`、`bunx tsgo --noEmit`（白名单，无需批准）。
- flag 开启手测对应阶段验证门场景。
- 关键纯函数加 `*.test.ts`（分段打包、seq→chunk 映射、manifest diff）。

## 给 subagent 的强制要求
- 动手前必须先 Read 本文件（`docs/plans/chunk-virtualized-message-list.md`），以“修订记录与当前分阶段计划”节为准。
- 严守 CLAUDE.md 主线程性能红线、禁改 `drizzle/`、禁删数据库、i18n 双语同步。
- 不要重做“已完成”清单中的工作。
