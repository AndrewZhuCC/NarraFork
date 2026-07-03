# Task Runtime Design

## 目标

`spec://tasks.json` 替代旧 todo，同时吸收旧 goal 的自动 continuation 与完成反思能力。

## 最小公开状态

模型只看到：

- `tasks[].text`
- `tasks[].status`
- `tasks[].protected`

系统内部维护：

- 稳定 task id
- text hash
- protected lock hash
- 创建者与来源
- 首次出现 revision
- 最近出现 revision
- fork base
- reflection 状态
- 删除/完成审计

## 状态推导

- 当前任务：第一个 `status = "doing"` 的任务。
- 下一任务：没有 doing 时，第一个 `status = "todo"` 的任务。
- 阻塞：当前任务为 `blocked`。
- 阶段完成：没有 doing/todo，或所有剩余任务为 done。

## taskReflection 触发

对 protected task 的以下变更触发 taskReflection：

- `text` 被修改。
- 条目被删除。
- `status` 被改为 `done`。
- 条目从当前任务集中消失。
- 系统检测到它被普通任务替代，且原 protected task 被绕过。

反思判断：

- 是否有足够证据证明任务完成。
- 是否只是阶段性完成，仍需保留后续任务。
- 是否客观条件导致任务不再适用。
- 是否需要用户确认才能废弃或改写。

## sidecar 最小提醒

正常提醒：

```xml
<tasks_reminder>
当前任务：...
如果完成，请更新 spec://tasks.json 将其标记为 done。
如果无法继续，请标记 blocked 并说明原因。
protected task 完成或删除需要 taskReflection。
</tasks_reminder>
```

未完成停止时：

```xml
<task_continuation>
你停止输出了，但 tasks.json 仍有 doing/todo 任务。
请继续推进，或更新任务状态。
</task_continuation>
```

## 清理策略

- 普通 done task 可以被模型清理。
- 清理时应在 `index.md`、`notes.md` 或相关设计文件中留下阶段性摘要，不写进 `tasks.json`。
- protected done task 不应被静默清理；需要 taskReflection 或系统归档。
