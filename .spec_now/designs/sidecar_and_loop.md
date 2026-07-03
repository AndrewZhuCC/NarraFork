# Sidecar and Continuation Loop

## 输入

sidecar compiler 不直接依赖用户原始输入，而是读取编译后的 spec 状态：

- 当前 doing task
- 下一个 todo task
- 是否存在 blocked
- 是否尝试修改 protected task
- 上一轮是否有实质推进
- 最近工具结果是否失败

## 输出原则

sidecar 应短小，只暴露当轮必要信息。

不应每轮重复：

- 完整用户原始需求
- 全部 protected requirements
- 全部设计文件内容
- 所有历史任务

## 正常 continuation

当存在 doing task 且未完成：

```xml
<tasks_reminder>
当前任务：...
请推进当前任务。如果完成，请把它标记为 done；如果无法继续，请标记 blocked。
</tasks_reminder>
```

## 自动切换

当没有 doing 但存在 todo：

- 系统可在运行时把第一个 todo 视为当前任务。
- 或自动 patch `tasks.json` 将其设为 doing。
- 然后继续 loop。

## 防偷懒

如果模型试图结束但仍有 doing/todo：

```xml
<task_continuation>
任务尚未完成。请继续推进，或更新 spec://tasks.json 的任务状态。
</task_continuation>
```

如果模型试图完成 protected task 但缺少证据：

```xml
<task_reflection_feedback>
protected task 不能直接完成。请补充证据、继续执行，或说明为什么需要废弃并等待确认。
</task_reflection_feedback>
```

## 停止条件

- 所有任务完成。
- 当前任务 blocked。
- taskReflection 要求用户确认。
- 连续多轮无实质推进，系统转为 blocked 或请求用户介入。
- 用户中断。
