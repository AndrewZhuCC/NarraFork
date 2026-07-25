# virtual 模式流式消息 flowtoken 式动画

## 目标

在「高级动画」开关（`narrafork_advanced_anim`，默认开）打开时，为 virtual 模式（vlist / `PretextExactMessageList`）的**流式 tail**加上与 classic 模式（`MarkdownContent` 的 `AnimatedMarkdownText`）一致的逐字/逐词淡入（blur-in）效果。我们自己实现，不引入 flowtoken 依赖。

## 背景事实（已核实）

- classic 模式已自实现该效果：`AnimatedMarkdownText` 把新追加的 grapheme 包进 `<span class="animatedWord">`，CSS `@keyframes markdownWordIn`（opacity/translateY/blur）。追加边界靠 `commonPrefixLength(previousText, text)` 计算，只有边界之后的片段带动画类。
- virtual 模式是**零 DOM 测量**架构：`InlineBlockView`（`render/RenderMarkdown.tsx`）用 pretext 把每行还原成一组绝对定位的 `<span>` fragment（`whiteSpace:"pre"`, `display:"inline-block"`），fragment 文本来自 `materializeRichInlineLineRange`。
- 流式 tail 只在 `isActive` 时渲染：`useExactStreamingTail` → `buildStreamingMsg`（id 恒为 `__streaming__`）→ `buildPretextDocumentLayout` → `streamingItems`，在 `data-pretext-exact-streaming-tail` 容器里用 `renderElement(kind, measured, extra)` 渲染，**每帧重算**。assistant 文本走 `kind==="markdown"` → `RenderMarkdown`。
- 已持久化的历史消息走 `ExactRow` → 同样 `renderElement`，但**不是** streaming tail，不能带动画。
- `data-advanced-anim="true"` 由 `AppRootLayout` 依据 `narrafork_advanced_anim` 写到 `<html>`。

## 两条铁律约束

1. **不影响开关关闭时的 classic 路径**：只改 `vlist/` 内文件 + `AppearanceSection` 无关。vlist 只能被动态 import。→ 本方案只改 vlist/ 内文件，满足 `vlist-isolation.guard.test.ts`。
2. **零 DOM 测量**：动画不能改变任何块的高度/宽度或触发重排。→ 动画只用 `opacity` + `transform` + `filter`（合成层属性，不影响布局盒），fragment 的 `display:"inline-block"` / `whiteSpace:"pre"` / `font` / 尺寸全部不变。measure 层完全不动，`RenderMarkdown` 属于 render 层（不在 `zero-dom-measure.guard` 扫描范围）。

## 设计

### 核心难点：把「文本追加边界」映射到 fragment

pretext 每行的 fragment 携带 `itemIndex` + `start/end`（LayoutCursor：segmentIndex/graphemeIndex），但**没有**该 fragment 在整块 flow 里的全局字符 offset。因此无法直接照抄 classic 的 `commonPrefixLength`（那是基于纯字符串 offset 的）。

采用**按 fragment 文本内容累计 offset**的方案，在 `InlineBlockView` 内部对已还原的 `lines[].fragments[]` 顺序累计字符长度，得到每个 fragment 的 `[fragStart, fragEnd)`（相对于该 inline block 的可见文本拼接）。再用「上一帧该 block 的可见文本长度」作为动画边界：`fragEnd > prevLen` 的 fragment 视为新增，附加动画类。

- block 级的 prev 长度用 `useRef` 存，key 由 block 内容决定。由于流式 tail 每帧 `RenderMarkdown` 重新挂载（`streamingItems` memo 依赖 `streamingMsg` 变化），**不能依赖组件实例存活**。→ 改为用一个**模块级 WeakMap/Map 缓存**：以稳定的 `animKey`（见下）为键存 `{ text, len }`，跨帧比较。
- 为进一步贴近 classic 的「逐 grapheme」颗粒度：新增 fragment 若只是「旧 fragment 尾部长出几个字」，把该 fragment 再按 grapheme 切成「旧前缀（无动画）+ 新后缀（动画）」两个 span。这样正在敲的词也能逐字淡入，而不是整段闪。

### animKey（跨帧稳定标识）

流式 tail 的 item `spec.key` 对 assistant markdown 是 `__streaming__-b{bi}`（稳定）。但一个 markdown block 会拆成多个 inline block（段落/行）。需要 `(itemKey, blockIndex)` 唯一定位一个 inline block。

`RenderMarkdown` 目前不知道 itemKey。方案：`renderElement("markdown", ...)` 增加可选 `extra.animateStreaming`（boolean）与 `extra.animKeyBase`（string，来自 `item.spec.key`）。`RenderMarkdown` 把 `animKeyBase + ":" + blockIndex` 作为每个 `InlineBlockView` 的 animKey。

只有流式 tail 渲染路径会传 `animateStreaming=true`；`ExactRow`（历史消息）和默认路径不传 → 历史消息零影响。

### 缓存生命周期

- 模块级 `Map<string, { text: string; len: number }>`。
- 流式结束（`__streaming__` 消失）后这些 key 不再出现，但 Map 会泄漏。→ 用 `animKeyBase` 前缀 `__streaming__` 的特性：在 `PretextExactMessageList` 流式 tail 为空时清理（或在 anim 模块提供 `resetStreamingAnim(prefix)`，tail 长度归零时调用）。更简单稳妥：Map 容量上限 + LRU 式裁剪（超过 N 条删最旧）。倾向后者，避免跨组件耦合。

### 动画边界的单调性

- 与 classic 一致：只有 `text.startsWith(prevText)` 才认为是纯追加，用 `prevLen` 当边界；否则（重排/reset）`commonPrefixLength`。
- reset（切换 narrator / stream reset）时 `useExactStreamingTail` 会清空 blocks，tail 消失，animKey 不再命中；重新出现时 prev 不存在 → 全部当新增（首帧全体淡入一次，可接受，classic 首帧同样 `previousText===null` 不加动画——为对齐 classic，首次出现记为 `prevLen=len` 不加动画，避免整屏闪）。

## 变更文件（全部在 vlist/ 内 + 无关的开关项不动）

1. **新增 `vlist/render/stream-token-anim.ts`**（纯逻辑，可单测，无 DOM/React）
   - `graphemesOf(text)`：Intl.Segmenter grapheme 切分（带 code-point 回退），复用 classic 思路。
   - `commonPrefixLen(a, b)`。
   - `computeFragmentAnim(fragments, prevLen)`：输入本行 fragment 的 `{text}` 顺序，返回每个 fragment 的 `{ preText, animText }` 切分（preText 无动画，animText 有动画），基于跨行累计的全局 offset 与 `prevLen`。
   - `StreamAnimStore`：`Map` + 上限裁剪；`update(animKey, fullText): { boundary: number }`（返回本帧动画边界 offset，内部维护 prev）。
   - 导出常量 `STREAM_ANIM_MAX_KEYS`。

2. **改 `vlist/render/RenderMarkdown.tsx`**
   - `RenderMarkdownProps` 增加可选 `animateStreaming?: boolean`、`animKeyBase?: string`。
   - `RenderMarkdown` 把 blockIndex 传给 `InlineBlockView`，并透传 `animateStreaming`/`animKeyBase`。
   - `InlineBlockView`：当 `animateStreaming` 时，
     - 先拼出本 block 全部可见文本（顺序遍历 lines→fragments 累加 `text`），调用 `StreamAnimStore.update(animKey, fullText)` 得 `boundary`。
     - 渲染 fragment 时用累计 offset 判断：`fragEnd <= boundary` 全静态；`fragStart >= boundary` 整段动画；跨界的按 grapheme 切成静态前缀 + 动画后缀两个 `<span>`。
     - 动画 span 追加 className `vlist-anim-token`（附加，不替换现有 `frag.className`），其余样式（font/margin/whiteSpace/inline-block）完全不变。
   - 非 `animateStreaming`（默认/历史）保持现有渲染，一行 fragment 一个 span，零改动。

3. **改 `vlist/vlist-markdown.css`**
   - 新增 `.vlist-anim-token { animation: vlist-token-in 0.32s ease-out both; }` + `@keyframes vlist-token-in`（opacity 0→1 / translateY(0.15em)→0 / blur(2px)→0），镜像 classic `markdownWordIn`。
   - `@media (prefers-reduced-motion: reduce) { .vlist-anim-token { animation: none; } }`。
   - 关键：动画属性只含 transform/opacity/filter，`display:inline-block` 已由行内 style 提供，不改变盒模型 → 高度稳定。

4. **改 `vlist/render-registry.tsx`**
   - `renderElement` 的 `markdown` 分支：读取 `extra.animateStreaming` / `extra.animKeyBase` 并传给 `RenderMarkdown`。
   - `resolveRenderExtra` 不改（extra 由调用方在 streaming 分支显式注入，避免影响历史路径）。

5. **改 `vlist/PretextExactMessageList.tsx`**
   - 流式 tail 渲染块（`streamingItems.map`，约 1132 行）：计算 `const animateStreaming = isActive && document.documentElement.getAttribute("data-advanced-anim") === "true"`（与 classic 判定一致，`typeof document !== "undefined"` 守卫）。
   - 对每个 streaming item：`const extra = resolveRenderExtra(item.spec); if (animateStreaming && item.spec.kind === "markdown") { extra.animateStreaming = true; extra.animKeyBase = item.spec.key; }` 再 `renderElement(item.spec.kind, item.measured, extra)`。
   - 历史 `ExactRow` 路径完全不动。

## 验证

- `bunx tsgo --noEmit`
- `bunx @biomejs/biome check --write <改动文件>`
- `bun test frontend/components/narrator/vlist/stream-token-anim.test.ts`（新增：grapheme 切分、prefix 边界、fragment 跨界切分、store 上限裁剪、首帧不动画）
- `bun test frontend/components/narrator/vlist/render-registry.test.tsx`（回归：markdown 仍路由到 RenderMarkdown，新 extra 不破坏 dispatch）
- `bun test frontend/components/narrator/vlist/vlist-isolation.guard.test.ts` + `zero-dom-measure.guard.test.ts`（两条铁律不破）
- 人工：virtual 模式开+高级动画开，观察流式逐字淡入；高级动画关时无动画；历史消息无动画；OLED/reduced-motion 正常。

## 明确不做

- 不动 measure 层、不改高度模型、不引入 ResizeObserver。
- 不给 reasoning/tool/user 等其它元件加动画（本次只做 assistant streaming markdown tail，和 classic 的作用范围对齐）。
- 不引入 flowtoken 依赖。
