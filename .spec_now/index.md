# Living Work Spec: DB-backed `spec://` 虚拟目录

## 当前结论

NarraFork 的 plan、todo、goal 不应继续作为三套并列心智模型暴露给用户和模型。更好的方向是：

- 用数据库存储 Work Spec。
- 用 `spec://` 虚拟 URL 让模型通过现有 Read/Edit/Write/Grep 访问。
- 用 `spec://tasks.json` 承担任务调度和自动 continuation。
- 用 `spec://protected_requirements` 保存只读监督原则。
- 用 taskReflection 替代现有 goal completion reflection。
- fork narrator/chapter 时，spec 虚拟目录一起 fork，并采用写时复制。

## 本地自举映射

未来虚拟路径与当前本地文件的对应关系：

| 未来路径 | 当前本地路径 | 作用 |
| --- | --- | --- |
| `spec://index.md` | `.spec_now/index.md` | spec 总览 |
| `spec://tasks.json` | `.spec_now/tasks.json` | 最小任务队列 |
| `spec://protected_requirements` | `.spec_now/protected_requirements` | 只读监督要求 |
| `spec://HOW_TO_USE_SPEC.md` | `.spec_now/HOW_TO_USE_SPEC.md` | 内置使用说明 |
| `spec://designs/*.md` | `.spec_now/designs/*.md` | 模型自行组织的详细设计 |

## 公开给模型的最小任务格式

`tasks.json` 应保持非常小：

```json
{
	"tasks": [
		{ "text": "任务内容", "status": "doing", "protected": true }
	]
}
```

阶段摘要写到 `spec://index.md` 或普通说明文件，不放进 `tasks.json`。运行时可以推导的东西不写入文件，也不总是暴露给模型，例如：task id、创建时间、来源、toolUseId、fork base、反思状态、完成证据索引等。

## protected task 语义

普通 task 类似旧 todo：模型可以自由新增、改状态、清理。

protected task 类似旧 goal 的承诺能力：

- 模型可以创建 protected task。
- 创建后，内容锁定。
- 完成、删除、废弃、替换都需要 taskReflection。
- 系统应把 protected task 的内容 hash、来源、创建修订等存入 DB hidden metadata。

## 自动 continuation

系统每轮结束后编译 `tasks.json`：

1. 如果存在 `doing` 且未 `done/blocked`，继续围绕它 loop。
2. 如果没有 `doing` 但有 `todo`，选择第一个 `todo` 作为下一任务并继续。
3. 如果全部完成，进入完成检查或停止。
4. 如果当前任务 `blocked`，停止并向用户说明阻塞。
5. 如果模型停止输出但任务仍未完成，注入简短 sidecar，提醒继续或更新状态。

## 设计细节

- 任务运行时与 taskReflection：见 `.spec_now/designs/task_runtime.md`
- 虚拟文件系统与写时复制：见 `.spec_now/designs/virtual_spec_fs.md`
