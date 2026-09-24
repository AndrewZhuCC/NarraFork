# OpenAI `reasoning_text` 400：既有处理确认与剩余缺口修复

## 结论（先回答你的记忆）

**是的，这个问题做过专门修复。**

- 提交：`f0fe4eaa` — `fix(agent): 网关签发 encrypted reasoning 时 relay 模型也原样回传`
- 已合入当前 `HEAD`（`9b9934d7` / v0.7.7）
- 场景：Console Go 类 Codex 兼容网关会对 **relay 模型**（如 deepseek）也签发 `encrypted_content`，并要求后续请求必须回传 reasoning item，否则 400：

  > `The 'reasoning_text' in the thinking mode must be passed back to the API.`

- 旧行为：按模型名判定 relay（`strict=false`）→ 把历史 reasoning **降级为 assistant 文本**、丢掉 `encrypted_content` → 网关校验失败。
- 修复行为：只要 reasoning 块带 `encrypted_content`（且签名匹配当前网关），**一律按 `type:"reasoning"` 原样回传**；仅在确无凭据时才降级明文。

测试已在下列用例中固化该错误文案与行为：

- [server/lib/agent/__tests__/openai-provider-history.test.ts](server/lib/agent/__tests__/openai-provider-history.test.ts) — `buildHistory` / `pushAssistantTurn` 在 relay + gateway-minted encrypted 场景下回传 reasoning item
- 实现：[server/lib/agent/openai-provider.ts](server/lib/agent/openai-provider.ts) 中 `buildResponsesPreludeItems`、`buildResponsesAssistantItemsFromStoredContent`

相关但不同的策略模块：

- [shared/agent-protocol/reasoning-source.ts](shared/agent-protocol/reasoning-source.ts) — `signatureSourcesCompatible`：缺 `signatureSource` 或与当前源不一致 → 不回放（防跨服务器签名校验失败）
- [shared/reasoning-credentials.ts](shared/reasoning-credentials.ts) — `hasCredentialBoundReasoning`：gpt/o 系/claude 判为 credential-strict，其余为 relay

---

## 你现在仍看到同一 400 的可能原因

主路径（Responses 格式 `buildHistory` + `pushAssistantTurn`）在 HEAD 上已处理；若错误仍在，优先排查下列**残留缺口**（按可能性排序）：

| # | 缺口 | 机制 | 代码位置 |
|---|------|------|----------|
| 1 | **`convertHistoryToResponsesApi` 未同步 f0fe4eaa 策略** | `strict=false` 时即使 `_reasoningBlocks` 带 `encrypted_content`，也只 `relayReasoningText` 降级为 assistant 文本，**从不**回传 reasoning item。正常 Responses 历史已是 `type:"reasoning"` 会 pass-through；但若中间历史是 Chat Completions 形状（带 `_reasoningBlocks`）再被 convert，仍会丢 | [openai-provider.ts](server/lib/agent/openai-provider.ts) `convertHistoryToResponsesApi`（约 L4160–4311）；Codex WS 请求构建同用此函数 |
| 2 | **signatureSource 不匹配/缺失** | 旧消息无 `signatureSource`，或切换 provider/NUG channel 后源不一致 → strict 丢弃 / relay 降级文本。对「必须回传 reasoning item」的网关仍 400 | `signatureSourcesCompatible` + 历史回放分支 |
| 3 | **上游未下发 `encrypted_content`** | 只流 `reasoning_text.delta` 而 `output_item.done` 无 credential → 存库后无法作为 reasoning item 回放 | 流解析 `output_item.done` → `reasoningEncryptedContent` |
| 4 | **apiMode 配置为 completions** | 走 Chat Completions，用 `reasoning_content` 字段；若网关实际要求 Responses 式 reasoning item，会报同一错 | `buildHistory` 的 completions 分支 |
| 5 | **运行进程代码偏旧** | HEAD 有修复，但线上 `start:dev`/发布二进制若早于 f0fe4eaa 则行为仍旧 | 版本/进程核对 |
| 6 | **远程插件路径** | Go/remote-provider-adapter 的 reasoning 回放策略与 TS OpenAI provider 不完全一致 | `remote-provider-adapter.ts` |

**诊断（实施前第一步，需要你提供的信息）：**

1. 出错会话用的 **provider / model / apiMode**（设置页或供应商配置）。
2. 若有 **request dump**（用法历史 → 下载 raw dump），看失败请求 `input`/`messages` 中上一轮 assistant 的 reasoning 是否存在、是否带 `encrypted_content`。
3. 出错是 **第二轮追问**、**工具调用续跑**，还是 **新会话首轮**？
4. 当前运行 NarraFork 的版本号（设置 → About）。

---

## 实施计划

### 阶段 A — 诊断定位（只读）

1. 根据你补充的 provider/model/request dump，判定命中上表哪一条缺口。
2. 若 request dump 显示 `input` 里缺 `type:"reasoning"` / `encrypted_content` → 代码回放缺口。
3. 若 dump 里已有 reasoning item 仍 400 → 网关形态问题（字段名/summary 形状），需对照网关期望调整序列化。
4. 若运行版本 < f0fe4eaa → 先升级，不在旧进程上叠补丁（遵守铁律：不擅自重启 NarraFork 进程）。

### 阶段 B — 代码修复（仅当确认命中缺口 1 或同类策略分叉）

**目标：** 所有 Responses 请求组装路径的 reasoning 回放策略与 `buildResponsesAssistantItemsFromStoredContent` 对齐。

1. **改 `convertHistoryToResponsesApi`**
   - 抽出与 `buildResponses*` 相同的决策函数（建议 `resolveReasoningReplayAction(block, options)`）：
     - 有 `encrypted_content` **且**（in-memory 场景无需 source 检查 / 历史场景 `signatureSourcesCompatible`）→ push `type:"reasoning"` item（含 `id`、`summary`、`encrypted_content`）。
     - strict 且不可回放 → drop。
     - relay 且不可回放 → 降级 assistant 文本。
   - 同步修正函数头注释（当前仍写「relay 模型永不发送 encrypted_content」，与 f0fe4eaa 后的主路径策略矛盾）。
   - 覆盖 `assistant`（有/无 tool_calls）两个分支。
2. **Codex WS / generate 等调用点**无需改接口，只需确认 `currentSource`/`strict` 传参正确。
3. **可选加固（缺口 2，需谨慎）：** 对「有 encrypted 但 `signatureSource` 缺失」的历史，在 relay/Codex 兼容网关上是否放宽为「按 itemId+encrypted 原样回传」。默认**不做**放宽（跨源回放可能换来另一种硬失败）；仅在诊断证明网关要求回传且源实际同源时，考虑按 provider 默认 prefix 回填推断，或仅对同 provider 历史放宽。

### 阶段 C — 测试

1. 扩展 [openai-provider-history.test.ts](server/lib/agent/__tests__/openai-provider-history.test.ts)：
   - `convertHistoryToResponsesApi` + relay 模型 + `_reasoningBlocks` 带 encrypted → 必须含 `type:"reasoning"` + `encrypted_content`。
   - 同函数 + strict + signatureSource 不匹配 → 不含 encrypted 回放。
   - 与既有 `pushAssistantTurn`/`buildHistory` 用例交叉，确保三处策略一致。
2. 运行：`bun test server/lib/agent/__tests__/openai-provider-history.test.ts --isolate`
3. `bunx tsgo --noEmit` + `bunx @biomejs/biome check .`

### 阶段 D — 非代码收尾

1. 在会话/知识记录中简要记下：该 400 的根因、已修路径、残留路径与如何用 request dump 自检。
2. **不**重启正在运行的 NarraFork 进程；修复生效由你自行重载。

---

## 明确不做的事

- 不为了「验证」去 kill/restart `bun run start:dev` 进程。
- 不删除数据库或 request-dump 目录。
- 不在未看到 dump/配置前盲目放宽跨源 encrypted 回放。
- 不改 Ruler 相关代码。

---

## 需要你确认的信息（阻塞阶段 A 精确定位）

1. provider 名称 + 模型 id + apiMode（completions / responses / codex）。
2. 是否有对应失败请求的 request dump 可下载（路径或 usage history 条目）。
3. 版本号（是否 ≥ 含 `f0fe4eaa` 的版本）。

若你暂时只想确认「有没有修过」：**有**，主路径已修；若错误正在复现，按上面阶段 A 提供 dump/配置后继续阶段 B。
