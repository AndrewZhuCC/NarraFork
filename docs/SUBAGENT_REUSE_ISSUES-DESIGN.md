# 子代理复用：Send / Await / Stop / ContextAsk 问题分析

来源：CatBurger 会话 S12～S13 的问题报告（主叙述者反复复用同一个后台 general 子代理，`Send` 下发修正、`Await` 取结果）。
本文对照 main（`221e9ad`）代码逐条核实，给出根因判断与修复方向，供讨论。

结论速览：

| # | 现象 | 判断 | 可信度 |
|---|------|------|--------|
| 1 | Send 后紧接 Await 拿到上一轮报告 | 同一回合内 Send 与 Await 被并行执行，Await 先读到旧的 completed 状态 | 高（代码可直接推出） |
| 3 | Send 后 Stop 说 "not running" | Send 续跑把后台子代理转成前台运行，Stop 只认后台 abort 注册表 | 高（与时序无关，必现） |
| 2 | Send 到运行中子代理，消息没被处理 | 子代理 Send 的排队路径缺少"运行刚结束"竞态的补唤醒；上一轮以错误结束时也不会自动唤醒 | 中（两条可能路径，需日志确认） |
| 4 | ContextAsk 分块结论矛盾 | 设计如此：它只读已持久化的转录，排队未消费的消息根本不在转录里，摘要模型分块各自猜 | 高 |

---

## 问题1：Await 返回上一轮结果

### 根因

`shared/tool-parallel-groups.ts` 把 `Send` 和 `Await` 都列在 `PARALLEL_SAFE_TOOL_NAMES` 里。模型在同一条 assistant 消息里先后发出 `Send(X)`、`Await(X)` 时，两者落在同一个并行组、同时开始执行。

- `Send` 唤醒已结束子代理的路径是 `enqueueInboxAgent` → `resumeSubagent` → `startContinuedSubagent`，要经过几次 DB 写和 admission 锁之后才执行 `narratorService.updateStatus(subagentId, "working")`（`server/services/subagent-runner.ts` 约 2590 行）。
- 与此同时 `Await` 走 `awaitAgentResultDetailed`（`server/services/agent-communication.ts`）：
  - 后台子代理：`awaitBackgroundAgentTask` 看到 task 行已 completed、narrator 仍是 `isBackground`（Send 还没来得及清掉），就调用 `waitForCompletion`，对已完成的任务立刻返回旧 output。
  - 非后台子代理：status 还是 `idle`，直接 `readSubagentTerminal(target)`，用的是旧的 `logicalRunId`，读到上一轮的终态结果。

"每次 Send 后紧接 Await 都有概率触发"正好对应模型有时把两个调用放在同一回合、有时分两回合。分两回合时 Send 已经同步把状态置为 working，Await 会正常等新一轮。

现有代码里已经有同类问题的先例：`Agent` 之后的 `Await/Send` 用 `isAgentDependentToolExecutionGroup` 强制分组（"AGENT_BARRIER"），但 `Send → Await` 没有这道屏障。

### 修复方向

1. **执行屏障（最小改动，推荐先做）**：在 `groupToolExecutions` 里让"前一组含 `Send`、本项是 `Await`"也形成屏障，与 Agent 屏障同一机制；`sharesParallelGroup` / 恢复路径共用同一谓词，前端分组显示也自然一致。
2. **结果带轮次标识（防御层）**：`Await` 返回值里附上 `logicalRunId`（或递增的 run 序号）以及触发该轮的 `deliveryMessageId`；`Send` 的返回文本也写出新轮次的标识。这样即使以后出现别的竞态，调用方也能识别新旧。
3. 可选：`Await` 发现目标 idle 但信箱里还有已接受、未消费的 agent 消息时，视为"即将开始新一轮"而继续等待，而不是返回旧终态。这一条语义更强，但要小心 `lastStopReason=error` 这类永远不会被自动唤醒的情况（见问题2），否则会一直等到超时。

## 问题3：Stop 与真实状态不一致

### 根因

`Send` 续跑一个后台子代理时，`resumeSubagent` 不传 `preserveBackground`，于是 `startContinuedSubagent` 把它改成前台：`isBackground=false`、清空 `backgroundStatus`，也**不会**把 abort controller 注册进 `getBackgroundAbortControllers()`（只有 `preserveBackground` 时才注册）。

`Stop({type:"agent"})` 最终调用 `cancelBackgroundTask`（`subagent-runner.ts` 1155 行），第一步就是查这个注册表，查不到就返回 false → "is not running"。所以只要子代理是经 Send 续跑的，Stop 必然失败，跟时序无关。

同时 `background_tasks` 行仍停在上一轮的 completed，TeamStatus 若按后台任务表展示，也会显示为已完成。这就是报告里"Stop/TeamStatus/Await 不共享状态源"的具体来源。

### 修复方向（需要讨论选型）

- **A. 续跑保持后台语义**：父代理 Send 唤醒一个原本是后台的子代理时传 `preserveBackground: true`，让 task 行重新进入 running、注册 abort controller。Await / Stop / TeamStatus 都继续走后台任务这一条状态源。改动集中，但要确认 preserveBackground 路径（目前主要给重启/更新恢复用）在"正常 Send 续跑"场景下的通知与结论写回行为。
- **B. Stop 兼容前台续跑**：`Stop` 在后台注册表查不到时，再看 `activeResumeRuns` / 执行 owner，能找到就通过续跑的 abort controller 取消。改动小，但 TeamStatus 的显示问题仍要单独处理。

倾向 A：它从源头消除"同一个子代理一会儿是后台、一会儿是前台"的二义性。

## 问题2：Send 到运行中的子代理，消息没有被执行

报告里出现了两种返回文本，对应 `sendSubagentMessageDetailedWithRun` 的两条分支：

- `message queued`：读到目标 `working/waiting`，调用 `pushSubagentBufferedMessage` 入信箱，等目标在安全边界消费。
- `subagent started asynchronously`：读到目标已 idle，入信箱后同步 `resumeSubagent` 唤醒新一轮。

### 可能根因（两条，需要日志确认是哪条）

1. **运行收尾竞态，缺少补唤醒。** 用户直接给子代理发消息走的是 `bufferSubagentUserMessage`，它在 `withNarratorStartAdmission` 内入队，并检查"入队期间原 owner 是否已离开"，离开了就 `wakeInboxIfEligible` 补唤醒。父代理 Send 的排队分支直接调 `pushSubagentBufferedMessage`，**没有** admission，也没有这个检查。如果 Send 读到 `working` 时子代理恰好已经过了编排器最后一次信箱检查（`orchestrator.ts` 约 2785 行），消息就留在信箱里，直到下一次有人唤醒它。下一次 Send 唤醒时，信箱头是这条旧消息，于是出现"重发一次才执行""可能执行两次"。
2. **上一轮以错误结束时不自动唤醒。** 编排器在 `runState.hadError` 时跳过信箱消费；`wakeInboxIfEligible` 在 `lastStopReason === "error"` 时对非 user_input 消息直接返回 false。子代理（deepseek-flash）如果本轮末尾遇到过 provider 错误，排队的 agent 消息就不会被处理，而 Send 返回的仍是 "message queued"。

"started asynchronously 却没执行"那一次，更可能是第1条的变体：子代理刚 idle，`resumeSubagent` 从信箱头消费的是之前滞留的那条消息，新消息排在后面。

### 修复方向

- 父代理 Send 的排队分支改为与 `bufferSubagentUserMessage` 相同的 admission + `needsWake` 补唤醒逻辑（或直接复用它）。
- `Send` 返回文本里区分三种状态，并带上 `deliveryMessageId`：已进入当前运行（排队等安全边界）、已唤醒新一轮、已保留但目标不会自动运行（例如上一轮错误结束，需要显式重试）。
- 子代理消费某条信箱消息时，在其转录/结果里标出对应的 `deliveryMessageId`，便于问题4的结构化查询。

## 问题4：ContextAsk 判断"是否收到消息"不可靠

`ContextAsk` 的定义就是让摘要模型读目标的**已持久化上下文**回答问题，长上下文会分块。排队但未消费的消息不在转录里；已消费的消息又可能落在某一个分块里。分块各自作答，自然会出现"没收到"和"收到但没执行"并存。这不是摘要模型的 bug，而是用错了工具。

### 修复方向

- 投递状态是确定性事实，应由结构化接口回答：信箱行本身有 `state`（queued / 已消费等）和 `deliveryMessageId`。建议在 `TeamStatus` 增加按 `deliveryMessageId` 查询的动作（或在其 status 输出里列出每个子代理的未消费信箱条目），返回 queued / consumed（附消费它的 run）/ cancelled / failed。
- `ContextAsk` 描述里补一句：投递状态请用 TeamStatus 查询，不要问摘要模型。

## 推荐的实施顺序

1. 问题1 执行屏障 + 问题3 方案A：根因确定、改动面可控，先做并配回归测试。
2. 问题2：先给 Send 排队分支补 admission + 补唤醒，同时让 Send 返回三态和 `deliveryMessageId`。
3. 问题4：TeamStatus 增加投递状态查询，ContextAsk 描述补充说明。
4. 文档：在子代理复用的推荐用法里写明"Send 与 Await 分两回合，或比对 Await 返回的轮次标识"（修复上线前的临时约定）。
