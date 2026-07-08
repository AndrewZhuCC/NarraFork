# Virtual Spec Filesystem Design

## 目标

把 Work Spec 存储在数据库中，通过 `spec://` 虚拟 URL 暴露给现有 Read/Edit/Write/Grep 工具。模型不需要学习大量新工具，只把 spec 当作文件系统使用。

## 特殊路径

- `spec://index.md`：总览入口，可编辑但可被结构校验。
- `spec://tasks.json`：最小任务队列，可编辑但受 protected task 规则约束。
- `spec://behavior_fence`：行为护栏，对 agent 只读；用户可在 UI 编辑，编辑通过 sidecar 提醒 agent。
- `spec://designs/*`：模型自行创建的设计文件。

## 数据库语义

核心概念：

- namespace：一个 narrator/chapter 看到的一棵 spec 虚拟目录。
- file revision：某个虚拟文件的一次不可变内容版本。
- overlay：namespace 对某个 path 的新增、覆盖或删除。
- builtin file：系统内置只读文件。

读取流程：

1. 查询当前 namespace overlay。
2. 若不存在，查询 fork base snapshot。
3. 若不存在，查询 builtin file。
4. 仍不存在则返回 not found。

写入流程：

1. resolve 当前内容。
2. 运行权限和特殊文件校验。
3. 创建新的 file revision。
4. 在当前 namespace 写 overlay。
5. 编译 spec 状态并广播更新。

## fork 写时复制

fork narrator/chapter 时：

- 不复制所有文件内容。
- 创建 child namespace。
- child namespace 指向 parent 在 fork 时刻的 spec snapshot。
- 子会话首次修改某个 path 时才创建自己的 revision 和 overlay。

要求：

- 子会话不会看到父会话 fork 后的新修改，除非显式同步。
- 父会话不会受子会话修改影响。
- 后续可设计 spec merge/rebase，但不是第一阶段必须项。

## 与现有工具集成

- Read：识别 `spec://`，从 spec provider 读取。
- Edit：识别 `spec://`，执行虚拟文件 diff + 校验 + revision 写入。
- Write：用于创建普通 spec 文件；特殊只读文件拒绝。
- Grep：在当前 namespace resolve 后搜索虚拟文件内容。
- Glob：可选支持 `spec://**/*.md`，非第一阶段必需。
