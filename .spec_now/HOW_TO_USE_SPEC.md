# HOW TO USE THIS SPEC

这是未来 `spec://HOW_TO_USE_SPEC.md` 的本地自举版本。

## 最小规则

1. 先读 `.spec_now/tasks.json`，确认当前 `doing` 任务。
2. 推进当前任务；如果完成，把它改为 `done`。
3. 如果当前任务无法继续，把它改为 `blocked`，并在回复或相关设计文件中说明阻塞原因。
4. 如果没有 `doing`，系统应把第一个 `todo` 视为下一步任务。
5. 普通 `done` 任务可在阶段性完成后清理；阶段摘要写到 `index.md` 或普通说明文件，不写进 `tasks.json`。
6. `protected: true` 的任务代表用户意图或关键验收要求，不能被绕过。

## tasks.json 公开格式

```json
{
	"tasks": [
		{ "text": "任务内容", "status": "todo" },
		{ "text": "关键任务", "status": "doing", "protected": true }
	]
}
```

允许的状态：

- `todo`：待处理
- `doing`：当前进行中
- `done`：已完成
- `blocked`：无法继续，需要外部条件或用户输入

除 `text`、`status`、`protected` 外，不应向模型暴露更多运行时字段。ID、来源、时间、修订、反思状态、fork base 等都由系统在数据库中维护。

## protected task

- 模型可以创建 protected task。
- 创建后，protected task 的 `text` 不能被模型直接修改。
- protected task 不能被模型直接删除。
- protected task 从 `todo/doing` 改为 `done`，或被废弃/删除时，应触发 taskReflection。

## 文件组织

- `.spec_now/index.md`：总览和设计入口。
- `.spec_now/tasks.json`：唯一任务调度源。
- `.spec_now/protected_requirements`：模拟只读监督要求。
- `.spec_now/designs/*.md`：复杂设计细节，可按需创建。
