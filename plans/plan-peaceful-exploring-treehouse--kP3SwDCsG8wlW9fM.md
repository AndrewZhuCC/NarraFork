# 工具 Shimmer 三态区分：流式输出 / 运行中 / 被上游阻塞

## 问题

同一轮里出现多个工具调用时，卡片/折叠行的 shimmer 光效区分不了这三种情况：

| 真实状态 | 现状 | 用户看到的 |
|---------|------|-----------|
| 参数还在流式输入 | neutral 循环扫光 | 中性扫光 |
| 真正执行中（`tool_executing` 已发） | blue 循环扫光 | 蓝色扫光 |
| **参数已解析完，但前面工具还在跑/等权限，自己尚未开始** | 落在 `status=initializing`，被 `resolveToolShimmerPhase` 归入 `streaming` | **同样是中性扫光** |

第三种被静默并入第一种，根因在 [shared/tool-shimmer.ts](shared/tool-shimmer.ts)：

- `initializing` 写在 `NON_EXECUTING_IN_FLIGHT_STATUSES` 里，与 `streaming` 同映射为 neutral。
- 设计注释只区分了「参数解析 ≠ 执行」，**没有**再区分「解析完在排队」和「参数还在到达」。
- `pending`（等人类审批）被刻意静音——那是对的；但「被上面工具占住、还没轮到自己」既不是静音也不是 streaming。

数据层其实已经有足够线索，只是前端没用：

- DB status：`initializing | pending | running | success | fail`（无 queued 枚举）
- wire：`tool_use_chunk`（参数流）→ `tool_call`/`tool_started`（参数完整）→ `tool_executing`（真正开跑）
- 同一条 assistant 消息里工具有顺序；`groupToolExecutions` 决定谁先跑、谁排队

## 目标

在 **不改数据库 status 枚举** 的前提下，让 shimmer 能一眼分出：

1. **streaming** — 参数仍在到达（中性循环扫光，保持现状）
2. **running** — 已 `tool_executing`，正在执行（蓝色循环扫光，保持现状）
3. **queued** — 参数已齐，但因上方未完结工具/组而阻塞未开跑（**新相位**，视觉必须与扫光可分）
4. （已有）**reflecting** 橙 / **pending 等人** 静音 / **done·fail** 单次反向闪 — 优先级不变

## 推荐视觉语言（queued）

调色板已挤满（status-registry 注释写明 teal/cyan/violet/pink 均与现有状态色过近），**不要靠新色相区分**。与项目「颜色分组、形状/动效承载身份」一致：

- **queued = 非扫光**。扫光暗示「正在动」；排队恰恰是「还没动」。
- 做法：左侧一条 **固定、不位移的暗条**（card 用 `::after` 左缘窄带；trace 行用文字左侧短渐变或仅降透明度 + 极慢 opacity pulse）。
- 色相用 **slate**（与 `model_unavailable` / `quota_exhausted` 同一「机器在等、用户无事可做」语义），不是 yellow（yellow 在本系统已绑「等你行动」）。
- `prefers-reduced-motion`：去掉 pulse，保留静止暗条（不能像 trace 那样恢复成与 streaming 相同的 neutral 文字扫光）。
- 无障碍文案：`traceRowState.queued` → en `waiting for earlier tools` / zh-CN `等待前面的工具完成`。

备选（若评审嫌暗条太弱）：极慢（3s+）的 slate 半程微扫，幅度远小于 streaming/running，且两端停在框外——仍建议首选「非扫光」。

## 检测算法（纯函数，不改 schema）

在 `@shared/tool-shimmer` 增加：

```ts
export type ToolUpstreamPeer = {
  status?: string | null;
  isStreaming?: boolean;
  hasPendingPermission?: boolean;
  reflectionStatus?: string | null;
};

/** true ⇔ 本调用参数已齐，但同消息内更早的调用仍占着执行回合 */
export function isToolQueuedBehindUpstream(input: {
  status?: string | null;
  isStreaming?: boolean;
  hasPendingPermission?: boolean;
  reflectionStatus?: string | null;
  /** 本调用在同条 assistant 消息工具序列中的位置之前的调用 */
  earlierTools: readonly ToolUpstreamPeer[];
}): boolean
```

判定顺序（与现有 precedence chain 衔接）：

1. 本调用有 reflection `running` / `hasPendingPermission` / `status===pending` → **false**（交给现有 reflecting / 静音）
2. `isStreaming===true` 或 `status==="streaming"` → **false**（是 streaming，不是 queued）
3. `status==="running"` → **false**
4. `status` 非 `initializing`（或空/终态）→ **false**
5. `earlierTools` 中存在 **仍占回合** 的调用：
   - `running`、`pending`、`isStreaming===true`、`reflectionStatus==="running"`、或 `initializing`（更早的 initializing 本身也可能在等更早的，形成传递阻塞）
   - → **true**（queued）
6. 否则 → **false**（本调用虽是 initializing，但前面已无活口，属于「即将开跑/权限周期未写 pending」的短暂窗口，保持 neutral streaming 或暂无扫光均可；**推荐保持 neutral**，避免闪一下 queued）

并入 `resolveToolShimmerPhase`：

```ts
// 新增 input 字段
earlierTools?: readonly ToolUpstreamPeer[];

// 在 reflection / pending silence 之后、streaming 判定附近：
if (isToolQueuedBehindUpstream({ status, isStreaming, hasPendingPermission, reflectionStatus, earlierTools }))
  return "queued";
```

新增 phase：

```ts
export type ToolShimmerPhase = "streaming" | "reflecting" | "running" | "queued";
```

`CARD_SHIMMER_CLASS` / `TRACE_SHIMMER_CLASS` 增加 `queued` 映射。

**分组语义（可选增强，第一版可不做）：** 若能拿到完整 `toolUses`，可用 `groupToolExecutions`（[server/lib/agent/tool-execution-groups.ts](server/lib/agent/tool-execution-groups.ts)）把「同组并行的 initializing」标成非 queued，只有**更晚组**才是 queued。前端 adapter 通常只有已落库/已广播的行，未必有完整并行组边界；简化版「更早仍有 in-flight」在串行工具与 Agent barrier 场景已正确，在「同组多个 Read 都 initializing」时会短暂全部标 queued——可接受，组一开跑会立刻变 blue。若要把该逻辑放 shared，可把 `groupToolExecutions` 抽到 `@shared/tool-execution-groups`（纯函数、无 server 依赖），前后端共用；**本计划默认不强制搬迁**。

## 实施步骤

### 1. 共享决策层（核心）

文件：

- [shared/tool-shimmer.ts](shared/tool-shimmer.ts)
- [shared/__tests__/tool-shimmer.test.ts](shared/__tests__/tool-shimmer.test.ts)

改动：

- `ToolShimmerPhase` 增加 `"queued"`
- 新增 `isToolQueuedBehindUpstream` + `resolveToolShimmerPhase` 接受 `earlierTools`
- `CARD_SHIMMER_CLASS` / `TRACE_SHIMMER_CLASS` 增加 queued 键
- 更新文件头五态表 → 六态（或「三循环 + 反射 + 两闪」）
- 更新 `IN_FLIGHT` 相关注释：`initializing` 在有上游阻塞时映射 queued，否则仍 streaming
- 分区测试：`IN_FLIGHT_TOOL_ROW_STATUSES` 每个 status 在「无 earlier」与「有阻塞 earlier」两种输入下都有明确 phase（不得 silent null，reflection/pending 除外）

测试用例至少覆盖：

- `initializing` + earlier `running` → `queued`
- `initializing` + earlier `pending` → `queued`
- `initializing` + earlier `initializing` → `queued`（传递）
- `initializing` + 无 earlier / earlier 全终态 → `streaming`
- `isStreaming: true` + earlier `running` → `streaming`（参数还在来，优先于 queued）
- `status: running` + earlier `running` → `running`（并行同组，不是 queued）
- `pending` / `hasPendingPermission` 仍 → `null`（静音不变）
- `reflectionStatus: running` 仍 → `reflecting`（即使有 earlier）

### 2. 全局样式

文件：

- [frontend/styles/card-shimmer.css](frontend/styles/card-shimmer.css)
- [frontend/styles/trace-shimmer.css](frontend/styles/trace-shimmer.css)

改动：

- 增加 `nf-card-shimmer--queued` / `nf-trace-shimmer--queued`
- **Card**：`::after` 左缘约 10–14% 宽 slate 渐变，`opacity` 慢脉冲（约 2.4s），**无 translateX**
- **Trace**：不与 streaming 共用 `nf-trace-shimmer-loop`；可用 `background-image` 左侧重、右侧 dimmed 的静态渐变 + 极慢 opacity，或仅降低 `opacity` 的 pulse；**必须保持 height-neutral**（只动 background/opacity/text-fill，不动盒模型）
- 文件头状态表补 queued；说明「非扫光 = 未开跑」的读法
- `prefers-reduced-motion`：queued 停 pulse，保留静止 slate 视觉，**不得**回退成 neutral 文字透明扫光（trace 的 reduce 分支要单独处理 queued：恢复 `currentColor` 或保持静态 slate 填充，不能和 stream/run 一样）

### 3. 把 `earlierTools` 送进渲染面

涉及（当前实际引用 shimmer 的路径）：

| 文件 | 职责 |
|------|------|
| [shared/pretext-layout/segment-adapter.ts](shared/pretext-layout/segment-adapter.ts) | 从同条消息的 tool 序列计算每行的 `earlierTools` 摘要（status/isStreaming/…），写入 measured 行或 adapter 标记 |
| [frontend/components/narrator/vlist/render/RenderToolCall.tsx](frontend/components/narrator/vlist/render/RenderToolCall.tsx) | `resolveToolShimmerPhase({ ..., earlierTools })` |
| [frontend/components/narrator/vlist/render/RenderToolRun.tsx](frontend/components/narrator/vlist/render/RenderToolRun.tsx) | 同上；折叠行 fallback `row.shimmer` 逻辑在有 queued 证据时优先 queued |
| [frontend/components/narrator/vlist/useVListLabels.ts](frontend/components/narrator/vlist/useVListLabels.ts) | `shimmerState.queued` 注入 |

Adapter 注意：

- 序列构建必须 **height-neutral**：只加数据字段/类名，不改 measure 几何
- `earlierTools` 可裁剪为每行所需的状态摘要数组，避免把完整 tc 塞进 measured payload 导致缓存键膨胀（measure-cache 若按 payload hash，需确认新字段是否参与 key——**应参与**，否则滚动复用旧类名）
- 现有 `isActiveToolItem` 仍把 initializing 算 active（LOD 不折叠）——保持，queued 仍是 live

### 4. i18n

文件：

- `frontend/locales/en/narrator.json` → `traceRowState.queued`
- `frontend/locales/zh-CN/narrator.json` → `traceRowState.queued`
- 同步 [useVListLabels.ts](frontend/components/narrator/vlist/useVListLabels.ts) 的 `shimmerState` 映射

建议：

- en: `"waiting for earlier tools"`
- zh-CN: `"等待前面的工具完成"`

### 5. 渲染测试

文件：

- [frontend/components/narrator/vlist/render/RenderToolRun.shimmer.test.tsx](frontend/components/narrator/vlist/render/RenderToolRun.shimmer.test.tsx)
- 如有 card 侧静态 class 断言，同样补 queued

覆盖：

- status=initializing + earlier running → DOM class 含 `nf-trace-shimmer--queued`
- 无 earlier 的 initializing → 仍是 `--stream`
- aria-label/title 使用注入的 `shimmerState.queued`
- reduced-motion 下 queued 不落入 `--stream` 动画（可通过 class 仍为 queued、CSS 契约测试 pin 类名与 keyframe 名）

### 6. 文档/注释一致性

- 修正 [shared/tool-shimmer.ts](shared/tool-shimmer.ts)、两份 CSS、以及测试文件头里「五态」表述
- 明确写清：`pending`≠queued；queued≠需要用户操作；等人的仍是静音

## 明确不做（Non-goals）

- **不改** `narrator_tool_calls.status` 枚举、不生成迁移、不写 recovery 新状态
- **不改** 侧栏 RecentTabs 的 narrator 级 working/queued 点（那是消息队列 `queued` substatus，与工具级 queued 同词不同物；侧栏已有 hollow/filled + 黄/蓝区分）
- **不改** ruler 模式
- **不** 为 queued 增加红色/黄色「要你处理」语义
- **不** 在 external API projection 里把 initializing 从 running 计数拆出（除非后续单独立项）

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 并行组内多个 initializing 被短暂标成 queued | 可接受；组一 `tool_executing` 即变 blue。后续可用共享 `groupToolExecutions` 收紧 |
| measure-cache 键未含 earlier 摘要 → 滚动后 class 错绑 | 扩展 cache key / payload 字段时带 earlier 摘要或 `queued` 布尔 |
| trace reduce-motion 把 queued 降级成 streaming 外观 | CSS 单独分支；测试 pin |
| 源码文本守卫测试引用旧路径/旧类表 | 全仓搜 `CARD_SHIMMER_CLASS` / `traceRowState` / `nf-trace-shimmer` 字符串 |
| 四处 render 面漂移（历史问题） | 决策仍只在 `@shared/tool-shimmer`；CSS 类名表从 shared 导出 |

## 验收

1. `bun test shared/__tests__/tool-shimmer.test.ts frontend/components/narrator/vlist/render/RenderToolRun.shimmer.test.tsx --isolate`
2. `bunx @biomejs/biome check` 相关文件 + `bunx tsgo --noEmit`
3. 人工场景：一轮内 `Bash`（串行）+ 后续 `Read`/`Write` — Bash 蓝扫、后续 queued 暗条；Bash 结束后后续变蓝或 neutral→蓝
4. 一轮内模型仍在写第二个工具参数时 — 第二个保持 streaming 中性，不因第一个在跑而变成 queued
5. 权限 pending 卡片仍完全无扫光
6. 折叠行无障碍 title 在 queued 时为中文「等待前面的工具完成」

## 实施顺序建议

1. `tool-shimmer.ts` + 单元测试（纯逻辑先绿）
2. 两份 CSS 类
3. adapter → RenderToolCall / RenderToolRun 接线
4. i18n + useVListLabels
5. 渲染测试 + biome/tsgo + isolate 相关测试
