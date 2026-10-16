# gen_ui 重设计：问题分析与方案草案

> 状态：讨论稿（分支 `claude/gen-ui-redesign-sidkzo`）。第 1–4 步（改名、描述与报错、markup 状态绑定、阻塞表单）已实施，第 5 步待定。
> 材料：一次真实会话导出（deepseek-v4.1-flash 使用 IntelligentUI 的全过程）+ main 上的现有实现。

## 0. 现状速览

| 动作 | 形态 | 显示 | 动态逻辑 | 值回传 |
|---|---|---|---|---|
| `render` | JSX 风格 markup（纯数据） | 行内、边流边画 | 无（只有 Mantine 组件自带的非受控状态） | 只有写死字面量的 `sendPrompt("…")` |
| `create` + `show` | 工作区 `App.tsx`，服务端编译 | 卡片 + 点击「打开预览」→ Modal | 任意 React | 无（`FilePreview` 不处理任何 `ui-action`） |
| `kit_status` / `kit_build` | 项目组件包 | — | — | 只有 kit 自己的代码能 post，宿主弹确认 |

关键代码：
- 工具定义与描述：`server/lib/agent/tools/intelligent-ui.ts`
- markup 解析：`shared/intelligent-ui-markup.ts`（7 个手势属性、2 个动作）
- 帧内渲染器：`frontend/components/intelligent-ui/preview-shell.ts`（`toValue` 把动作绑成 `() => act(value.$action, value.value)`，点击时不读任何状态）
- 行内宿主：`InlineRender.tsx`（`sendPrompt` 直接 `api.sendNarratorMessage`，项目 kit 时弹确认）
- App 宿主：`IntelligentUICard.tsx` 的 `PrivateCard` + `FilePreview.tsx`（只在点开后挂载；只监听 `preview-left`/`preview-present`）
- 服务端预览构建：`server/services/intelligent-ui/preview.ts`（全局并发 2、15s 超时）

---

## 问题 1：不看源码就用不顺

### 会话里实际踩到的坑

| 轮次 | 现象 | 根因 |
|---|---|---|
| 首轮 | 工具「看得见但不能用」，模型去翻 `tools/index.ts` 才弄明白 | 可选工具未加载时的拒绝信息没说怎么加载 |
| render 演示 | 模型担心组件名写错，读了 `kit-runtime.ts` + `compiler.ts` 才敢写 | 描述只说「组件包的组件」，没说内置就是整个 `@mantine/core`；一个未知标签导致**整张卡片什么都不画** |
| 每次 render | 模型以为「每次渲染宿主都会再弹一次组件包确认」，为了不打扰用户**放弃修正笔误** | 宿主其实只提示一次（`kit-runtime.ts` 的 `suggest()`），但每个 `render` 结果末尾都重复「The user has been asked whether to build…」（`describeRequest`），模型据此误判 |
| 「拖滑条改圆角」 | 模型说「上面就是交互应用」，其实用户只看到一个按钮卡片，被用户追问后才自我纠正 | 工具结果 `Preview: …/App.tsx` 没告诉模型「用户需点击才能看到」 |
| 表单 | 模型推断用户能「用 `value=`」，又读了三个文件才确认「值传不回来」 | 描述没写清能力边界：没有 onChange、值不回传、App 无宿主 API |
| 全程 | 5 个动作 + kit 概念 + 文件目录约定，模型需要先 `kit_status` 再 `render` | 心智负担大，且「先 kit_status」的规则强制多一轮 |

结论：**这个工具的正确用法目前只能从源码里读出来。** 没有仓库的用户（以及他们用的较弱模型）只会更多地出错重试。

### 方案

1. **描述按「能做什么 / 不能做什么 / 用户看到什么」重写**，并写短：
   - 明说内置组件 = 全部 `@mantine/core`，列 10–15 个最常用的带 props 示例；
   - 一张能力表（与上面的现状表同构），直接告诉模型选哪条路；
   - 明说受控属性（`value=`）的坑，或者干脆让解析器把它当 `defaultValue`（见问题 2 的绑定方案后这个坑自然消失）。
2. **错误要能教会模型**：
   - 未知标签**不再整张拒绝**：照常渲染，未知处画占位框，结果里返回「`<Foo>` 不存在，最接近的是 `<FocusTrap>`…」（编辑距离建议），而不是倒出 200 个组件名；
   - 可选工具未加载时的拒绝信息直接写「请用户 `/load gen_ui` 或在 设置 → 例程 设为常驻」。
3. **工具结果写清用户看到的东西**：`show` 返回「用户看到的是一个预览卡片，需点击打开」（若采纳问题 2 的自动挂载，则改成「已在对话中显示」）。
4. **kit 提示的结果文字只说一次**：宿主本来就只弹一次，但结果文本要跟着只在首次出现时说明（之后不再重复），并明说「再次 render 不会再打扰用户」；`kit_status` 改为可选，不再要求「首次 render 前必须调用」。
5. **动作收敛**（与问题 2 一起定）：对模型暴露 `render`（markup，主路径）、`app`（代码应用）、`kit`（status/build 合并，`{action:"kit", build:true}`）三个，`create`/`show` 合并为一个 `app` 动作（不存在就建，存在就显示）。

已实施（第 2 步）：描述重写（第 1 条）；未知标签照常渲染为占位框并给出近似名，全部未知时才报错（第 2 条，未加载工具的拒绝信息未改：未加载时模型根本拿不到该工具，拒绝路径不存在）；`render` / `show` / `create` 的结果写明用户看到什么、值不会回传（第 3 条）；kit 提示只在首次弹出时告知模型并说明不会重复（第 4 条）。

---

## 问题 2：流式 ≠ 动态 ≠ 行内，且值传不回 agent

三个缺陷是一个根问题：**markup 是纯数据（安全、可流式），但没有任何「状态」概念；而 App 有状态，却被隔离在点击后才出现的 Modal 里，且与宿主零通道。**

### 2.1 给 markup 加「声明式状态绑定」（推荐，核心改动）

不引入代码执行，只在数据层加三样东西：

```jsx
<Stack>
  <Slider bind="radius" defaultValue={12} min={0} max={64} />
  <TextInput bind="label" defaultValue="卡片" />
  <Card radius={$radius} withBorder>
    <Text>{$label}：border-radius = {$radius}px</Text>
  </Card>
  <Button onClick={submit()}>确定</Button>
</Stack>
```

- **`bind="x"`**（实现时由 `name` 改为 `bind`：Mantine 的 Avatar、Radio 等本身就有 `name` 属性，会冲突）：带 `bind` 的输入控件自动变成受控组件，值存进帧内一个 `form` 状态表（帧内渲染器负责给它接上 `value` + `onChange`，模型不写 handler）。
- **`$x` 引用**：属性值或文本里出现 `$x` 即读状态表；只允许**裸标识符**（可选一层成员 `$x.y`），不支持运算、调用。解析器把它解析成 `{ $ref: "x" }` 节点，帧内查表替换。仍然是数据，无 eval。
- **动作可携带值**：
  - `sendPrompt("把圆角设为 {radius}px")`：字符串里的 `{name}` 在点击时按状态表插值；
  - `submit()`：把全部命名字段打包回传（见 2.3）。

这样「拖滑条改圆角」这类联动可以用 `render` 直接做，**同时满足流式 + 动态 + 行内**。表达能力故意有限（无条件、无循环），够做表单、调参、预览类 UI；需要真逻辑的才走 App。

已实施（第 3 步）：`bind` / `$x`（属性、style 字面量、子节点）/ `sendPrompt` 中 `{x}` 插值 / `submit("可选消息")`。`submit` 以“消息 + 每行 `name: value`”作为用户消息发出，同时把结构化 `fields` 交给宿主（第 4 步阻塞表单会用它作为工具结果）。读了却没有任何控件 bind 的 `$x`，渲染结果会提示模型补上。评审后补充：树到达时先给所有 bind 预置初值（`$x` 可写在控件之前）；`TagsInput`/`MultiSelect`/`Checkbox.Group` 初值为 `[]`；同名单选框存被选中项的 `value`；项目 kit 的 Radix 风格控件同时接 `onValueChange`/`onCheckedChange`；`submit` 按字段平均截断以适配宿主 2000 字上限。

可选增强（第二步再定）：`showIf={$advanced}` 布尔显隐、`$x` 对 `Select`/`Checkbox` 的类型约定、`defaultValue` 缺省时的初始值规则。

安全面：引用只读帧内状态表，不能读 DOM/全局；插值后的文本依旧走手势校验 + 2000 字截断；项目 kit 下依旧宿主确认。

### 2.2 App 模式：能行内显示

- **最新一次 `app`/`show` 自动行内挂载**（像 render 那样有固定高度 + 放大按钮），历史消息里的仍保持「点击打开」——这样不会因为翻历史把并发 2 的构建额度打满，也不会自动执行过期代码。
- 只读表面（列表行、公开分享）保持现状不执行。
- 构建：命中「工作区文件哈希未变」时复用上次产物（当前 `useIntelligentUI` 明确无缓存），减少点开/重进的构建开销。
- App 也拿到宿主桥：帧内注入 `globalThis.genUI = { sendPrompt, submit }`（或 `import { submit } from "gen-ui"`），走与 markup 相同的 `ui-action` 消息；由于 App 是模型写的任意代码，**一律宿主确认**（与项目 kit 同策略）。

### 2.3 值回传给 agent：两种语义

| 方式 | 适用 | 机制 |
|---|---|---|
| **阻塞表单**（推荐默认） | agent 需要用户填完才能继续（配置向导、参数选择） | `render` 增加 `wait: true`：工具调用像 `AskUserQuestion` 一样挂起，用户点 `submit()` 后，**工具结果**就是 `{"radius": 24, "label": "卡片"}`；取消/超时返回明确的取消结果。可复用 `narrator-question-service` 的挂起/恢复与通知（human-attention） |
| **非阻塞提交** | 卡片留在对话里，用户随时操作 | 不带 `wait` 时 `submit()` 发一条用户消息，正文是人类可读的摘要 + 结构化附件（`gen_ui_submission` 元数据，含 toolUseId 与字段值），模型下一轮读到 |

两者都满足「值是用户手势产生的、宿主可见的」。

已实施（第 4 步）：没有复用权限弹窗那条路（`AskUserQuestion` 的同步模式靠权限门挂起，会带上自动代答、权限模式等语义），而是在工具内等待：`render` 带 `wait: true` 时，校验通过后在内存中登记等待（`server/services/gen-ui/form-wait.ts`），用户点 `submit()` 时卡片调 `POST /api/narrators/:id/gen-ui/form/:toolUseId`，工具结果为 JSON 字段值；卡片标题显示“等待你填写”，旁边有“关闭表单”。30 分钟超时、用户中断、服务重启都会结束等待，之后卡片里的提交退回为普通聊天消息。没有 `submit()` 控件或子代理（无法提问的运行时）中使用 `wait` 时直接报错，不挂起。非阻塞提交仍按第 3 步发聊天消息，暂未加 `gen_ui_submission` 元数据。阻塞表单比「伪造一条用户消息」更干净：值进工具结果、不污染对话、模型不会误以为用户说了话。

---

## 问题 3：改名为 gen_ui

### 范围（约 78 个文件引用）

- 工具名 `IntelligentUI` → 新名（模型可见）
- 内置例程 id `intelligent_ui` → `gen_ui`（`server/lib/builtin-routines.ts`）
- 工作区目录 `.narrafork/intelligent_ui` → `.narrafork/gen_ui`
- 代码目录/文件 `intelligent-ui` → `gen-ui`、i18n 命名空间键 `intelligentUi.*`、元数据键 `intelligentUi` / `intelligentUiInline` / `intelligentUiKit`

### 已定：工具名 `GenUI`，不保留旧名

与 `AskUserQuestion`、`ShareFile` 等保持 PascalCase；例程 id、工作区目录与代码路径用 `gen_ui` / `gen-ui`。

还没有发布过包含 IntelligentUI 的版本，所以**不做任何旧名兼容**（用户确认）：工具名、例程 id、元数据键、工作区目录、公开摘要码（`GEN_UI_PRIVATE`）、组件包签名密钥文件（`gen-ui-kit-signing.key`）全部只用新名。开发环境里用旧版本留下的 `.narrafork/intelligent_ui` 和已构建组件包需要重新生成。

---

## 建议的实施顺序

1. **改名**（机械改动，先做，后续改动都在新名字上）
2. **描述重写 + 错误信息 + kit 提示只问一次**（问题 1，改动小、收益立刻可见）
3. **markup 状态绑定：`name` / `$x` / 插值 `sendPrompt` / `submit()`**（问题 2 核心）
4. **阻塞表单 `wait: true`**
5. **App 最新一条行内自动挂载 + 构建缓存 + 宿主桥**

每步单独可合并、可回归。
