# Runtime Hidden State

## 原则

公开 spec 文件应面向模型和用户，保持短小、稳定、容易编辑。凡是系统可以运行时推导或内部维护的字段，都不应写进 `tasks.json`。

## 不暴露给模型的状态

- task id
- task 创建时间
- task 更新时间
- 创建者
- 来源消息
- 来源 toolUseId
- 文本 hash
- protected lock hash
- fork base revision
- namespace id
- file revision id
- taskReflection request id
- taskReflection 决策结果
- token/time 统计
- evidence 索引

这些数据放 DB hidden metadata，供系统校验、审计、sidecar、continuation 和 UI 使用。

## 为什么不暴露

- 减少模型写坏格式的机会。
- 避免任务文件膨胀成数据库导出。
- 降低上下文噪声。
- 让简单任务保持低摩擦。
- 保留系统对 protected task 的强约束能力。
