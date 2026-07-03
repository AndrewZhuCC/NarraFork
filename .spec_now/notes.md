# Notes

本目录是一次自举实验：先用普通文件系统 `.spec_now` 模拟未来 DB-backed `spec://` 虚拟目录。

当前值得保留的简化决定：

1. `tasks.json` 必须小，不做数据库导出。
2. protected task 是 task 级承诺，不是复杂 goal 对象。
3. protected requirements 是顶层只读监督区，不和 tasks 混在一起。
4. taskReflection 是 goal reflection 的泛化和改名。
5. 模型主要通过 Read/Edit/Write/Grep 操作 spec；系统负责解析、校验、sidecar 和 continuation。
