# Migration from Current Goal/Todo

## 当前机制映射

| 当前机制 | 新机制 |
| --- | --- |
| `narrators.todosJson` | `spec://tasks.json` 中的普通 task |
| `narrator_goals` | `spec://tasks.json` 中的 protected task |
| `TaskCreate` | 兼容层：编辑 `spec://tasks.json` |
| `GetGoals` | 兼容层：读取编译后的 protected task 摘要 |
| `UpdateGoal` | 兼容层：请求完成当前 protected task，触发 taskReflection |
| goal continuation | task continuation |
| goal completion reflection | taskReflection |

## 迁移原则

第一阶段不必删除旧工具：

- `TaskCreate` 仍可存在，但内部写入 `tasks.json`。
- `AddGoal` 可创建 protected task。
- `UpdateGoal` 可触发 taskReflection。
- 前端逐步从 goal/todo 两块 UI 收敛到一个任务面板。

## 兼容行为

如果旧会话只有 todos，没有 goals：

- 生成 simple `tasks.json`。
- todo 项作为普通 task。

如果旧会话有 active goal：

- active goal 转为 protected doing task。
- pending goals 转为 protected todo task。
- 当前 todos 转为普通 task；若需要阶段摘要，写入 `index.md` 或普通说明文件，而不是 `tasks.json`。

## 风险

- 模型可能滥用 protected，系统需要限制数量或通过 sidecar 指导。
- 旧 goal 的排序和用量统计需要映射到 hidden metadata。
- protected task 删除不能真删，应先归档或记录 reflection 决策。
