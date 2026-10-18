# 子代理复用：Send / Await / Stop / ContextAsk 问题分析

来源：CatBurger 会话 S12～S13 的问题报告（主叙述者反复复用同一个后台 general 子代理，`Send` 下发修正、`Await` 取结果）。
本文对照 main（`221e9ad`）代码逐条核实根因。修复原则：只修根因，复用已有机制，不加防御层和新接口。

| # | 现象 | 根因 | 修复 |
|---|------|------|------|
| 1 | Send 后紧接 Await 拿到上一轮报告 | 同回合的 Send 与 Await 被并行执行 | Send→Await 加执行屏障 |
| 3 | Send 后 Stop 说 "not running" | Send 续跑被转成前台运行，状态散落在两套来源 | Send 续跑统一走后台任务 |
| 2 | 发给运行中子代理的消息没被执行 | 父代理 Send 的排队分支缺少收尾竞态的补唤醒 | 复用用户发消息的那条入队路径 |
| 4 | ContextAsk 分块结论矛盾 | 它只读已持久化转录，排队中的消息不在里面 | 不改机制，只改工具描述 |

---

## 问题1：Await 返回上一轮结果

`shared/tool-parallel-groups.ts` 把 `Send` 和 `Await` 都列为并行安全。模型在同一条 assistant 消息里发出 `Send(X)`、`Await(X)` 时，两者同时开始：

- `Send` 唤醒已结束的子代理要经过 `enqueueInboxAgent` → `resumeSubagent` → `startContinuedSubagent`，几次 DB 写之后才把状态置为 working。
- `Await` 此时读到的还是 idle / completed 和旧的 `logicalRunId`，于是直接返回上一轮的终态结果。

分两回合调用时 Send 已经同步置为 working，Await 会正常等待新一轮。这正对应报告里"有概率触发"。

**修复**：仓库里已经有完全同类的机制，`Agent` 之后的 `Await/Send` 通过 `isAgentDependentToolExecutionGroup` 另起一组。把这条屏障推广为"前一组含 `Agent` 或 `Send`、本组含 `Await`"即可。实时执行、恢复路径和前端分组显示共用这一个谓词，改一处就全部生效。

## 问题3：Stop 与真实状态不一致

`Send` 续跑一个后台子代理时，`startContinuedSubagent` 会把它转成前台运行（`isBackground=false`，不注册后台 abort controller）。而 `Stop({type:"agent"})` 调用的 `cancelBackgroundTask` 只查后台注册表，查不到就返回 "not running"。所以只要子代理是经 Send 续跑的，Stop 必然失败，跟时序无关。

**修复：Send 续跑统一作为后台任务运行。** Send 本来就是异步的，父代理并不阻塞等待，续跑在语义上就是一个后台任务。现在它一会儿是后台、一会儿是前台，Stop、Await、TeamStatus 各读各的状态，这才是问题3的根源。

仓库里已经有完全相同的流程：`narrator-subagent-recovery.ts` 的"恢复失败子代理"就是先把一个 idle 子代理转成后台任务，再续跑。

1. `backgroundTaskService.createAgentTask`：建立或重启 background_tasks 行（已存在时走幂等的重启分支）。
2. 把 narrator 标记为 `isBackground=true`、`backgroundStatus="running"`，并加上 `background` trait。
3. `resumeSubagent({ preserveBackground: true, skipConclusionDelivery: true, ... })`。

把第1、2步提取成一个共享函数（例如 `promoteSubagentToBackground`），恢复流程和 Send 续跑都调用它；Send 续跑在第3步传同样的两个参数。这样做之后：

- **Stop**：后台 abort controller 已注册，`cancelBackgroundTask` 直接生效，不需要额外分支。
- **Await / TeamStatus**：都读 background_tasks 这一条状态源，续跑期间显示 running。
- **结果送达**：走后台完成通知（`finalizeBackgroundCompletion` 负责通知并唤醒父代理）。父代理 Send 之后可以直接结束回合等唤醒，这也符合 CLAUDE.md 里"非必要不 Await"的规则。
- **一处行为变化**：原 Agent 工具卡片上的结果不再被续跑结果覆盖，保留首轮结果；后续每一轮的结果作为独立的完成通知送达。新旧结果因此分得更清楚，问题1里"把旧报告当新结果"的误判也更难发生。

原本就是前台启动的子代理，被 Send 续跑时也同样转成后台任务。

## 问题2：发给运行中子代理的消息没有被执行

用户直接给子代理发消息时走 `bufferSubagentUserMessage`（`subagent-executor.ts`）：它在 `withNarratorStartAdmission` 内入队，并检查入队期间原运行是否已经结束，结束了就 `wakeInboxIfEligible` 补唤醒。

父代理 Send 的排队分支（`agent-communication.ts`，目标为 working/waiting 时）直接调用底层的 `pushSubagentBufferedMessage`，没有这层处理。Send 读到 working 时，如果子代理恰好已经过了编排器最后一次信箱检查，消息就留在信箱里，直到下一次被唤醒才执行。这能解释"重发一次才执行"，也能解释重发后可能执行两次：下次唤醒时，信箱头是滞留的旧消息，新消息排在它后面。

**修复**：这个分支改为调用 `bufferSubagentUserMessage`，不再直接调 `pushSubagentBufferedMessage`。传 `requestSoftStop: false` 保持现有"不打断，等安全边界"的语义；`doInterrupt` 继续按现有逻辑在入队后处理。这样父代理和用户两条入队路径的行为一致，不需要另写补唤醒。

另一个可能的诱因是：上一轮以 provider 错误结束时，系统按设计不会自动唤醒。这是有意的行为，本次不改。

## 问题4：ContextAsk 判断"是否收到消息"不可靠

`ContextAsk` 让摘要模型读目标的**已持久化转录**，长上下文会分块。排队但还没消费的消息不在转录里，分块各自作答，自然会出现矛盾。这不是缺陷，而是用错了工具；问题2 修好之后，这类"消息到底收到没有"的疑问本身也会少很多。

**修复**：只在 `ContextAsk` 的描述里补一句，说明它看不到尚未投递进转录的排队消息，不能用来确认投递。暂不新增 TeamStatus 查询接口；如果问题2 修复后仍有需要，再考虑在 `list_agents` 每行显示未消费消息数。

## 实施

一个 PR 完成问题1～3 的修复，并附上问题4 的描述修改，每个修复各配一个回归测试：

1. 分组测试：`Send(X)` 后紧跟 `Await(X)` 被分成两组。
2. Send 续跑测试：续跑后 background_tasks 行为 running，Stop 能取消，完成后父代理收到完成通知。
3. Send 测试：子代理在 Send 入队期间结束运行时，消息会被补唤醒处理。
