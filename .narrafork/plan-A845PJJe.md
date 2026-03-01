# 项目数据库双写备份方案

## 概述

在主库（`~/.narrafork/narrafork.db`）正常运行的基础上，为每个项目维护一个独立的备份数据库文件，存放在项目 git 仓库内（`<gitPath>/.narrafork/project.db`）。业务读写仅操作主库，项目库纯粹作为备份/导出载体。导入项目时从项目库恢复数据到主库。

## 设计决策

### 项目库存放位置
`<project.gitPath>/.narrafork/project.db`

理由：
- 与 git 仓库同级，项目自包含
- `.narrafork/` 目录可加入 `.gitignore`（备份库不需要版本控制）
- 不与现有的 `.worktrees/` 目录冲突

### 项目库包含的表（不含全局表）

| 表 | 说明 |
|---|---|
| `projects` | 仅当前项目的一条记录 |
| `chapters` | 该项目的所有章节 |
| `chapter_edges` | 该项目的所有边 |
| `chapter_commits` | 该项目所有章节的 commit |
| `exploration_groups` | 该项目的探索组 |
| `narrators` | 该项目所有章节绑定的叙述者 |
| `narrator_messages` | 上述叙述者的所有消息 |
| `narrator_message_refs` | 上述叙述者的消息引用 |
| `narrator_tool_calls` | 上述叙述者的工具调用 |
| `narrator_patches` | 上述叙述者的快照追踪 |
| `merge_sessions` | 该项目的合并会话 |

**不包含的表：**
- `users` / `user_preferences` / `user_favorite_directories` — 全局用户数据
- `terminals` / `terminal_tabs` / `terminal_view_state` — 运行时状态，不需要备份
- `container_instances` / `port_allocations` — 运行时状态，不需要备份
- FTS5 虚拟表 — 恢复时从数据重建

### 项目库 Schema

项目库使用**独立的、简化的 schema**，不使用 Drizzle 迁移，而是直接用 raw SQL 建表。原因：
1. 项目库不需要外键约束（数据已经在主库中验证过）
2. 不需要 FTS5（不做搜索）
3. 不需要索引（不做复杂查询，只做全量导出/导入）
4. Schema 版本通过 `user_version` PRAGMA 管理，简单可靠

### 双写策略：事件驱动 + 异步批量

利用现有的 `event-bus` 事件系统，在事件触发时异步写入项目库。不在 service 层侵入式修改，而是注册事件监听器。

**为什么不用 SQLite 触发器或 ATTACH：**
- 主库和项目库 schema 不同（项目库无外键、无索引）
- 需要按 projectId 路由到不同的数据库文件
- 事件驱动更灵活，失败不影响主库

**为什么不在 service 层逐个方法添加双写：**
- 侵入性太强，每个 service 方法都要改
- 容易遗漏
- 事件总线已经覆盖了所有关键操作

## 实施步骤

### 第 1 步：项目库连接管理器

**新文件：`server/lib/project-db.ts`**

```typescript
// 职责：
// 1. 管理项目库的 SQLite 连接（懒加载、LRU 淘汰）
// 2. 初始化项目库 schema
// 3. 提供写入接口

class ProjectDbManager {
  private connections: Map<string, { db: Database; lastUsed: number }>;
  private maxConnections: number = 5; // LRU 上限

  // 获取或创建项目库连接
  getDb(projectId: string): Database | null;

  // 初始化项目库（建表）
  initProjectDb(gitPath: string): Database;

  // 关闭指定项目的连接
  close(projectId: string): void;

  // 关闭所有连接（进程退出时）
  closeAll(): void;
}
```

**Schema 版本管理：**
```sql
PRAGMA user_version = 1;  -- 递增版本号
```

**建表 SQL（无外键、无索引、无 FTS）：**
```sql
CREATE TABLE IF NOT EXISTS projects (...);
CREATE TABLE IF NOT EXISTS chapters (...);
CREATE TABLE IF NOT EXISTS chapter_edges (...);
CREATE TABLE IF NOT EXISTS chapter_commits (...);
CREATE TABLE IF NOT EXISTS exploration_groups (...);
CREATE TABLE IF NOT EXISTS narrators (...);
CREATE TABLE IF NOT EXISTS narrator_messages (...);
CREATE TABLE IF NOT EXISTS narrator_message_refs (...);
CREATE TABLE IF NOT EXISTS narrator_tool_calls (...);
CREATE TABLE IF NOT EXISTS narrator_patches (...);
CREATE TABLE IF NOT EXISTS merge_sessions (...);
```

字段定义与主库一致，但：
- 所有 `REFERENCES` 外键约束移除
- 所有 `INDEX` / `UNIQUE INDEX` 移除
- 不创建 FTS5 虚拟表和触发器
- 保留 `PRIMARY KEY`（用于 UPSERT）

### 第 2 步：项目库同步服务

**新文件：`server/services/project-db-sync.ts`**

```typescript
// 职责：
// 1. 监听 event-bus 事件，触发异步写入
// 2. 从主库查询完整数据，写入项目库
// 3. 提供全量同步方法（初始化或修复时使用）

class ProjectDbSyncService {
  // 注册所有事件监听器
  registerListeners(): void;

  // 全量同步：从主库导出项目的所有数据到项目库
  async fullSync(projectId: string): Promise<void>;

  // 增量同步方法（由事件触发）
  private syncChapter(chapterId: string): Promise<void>;
  private syncNarrator(narratorId: string): Promise<void>;
  private syncNarratorMessages(narratorId: string): Promise<void>;
  // ...
}
```

**事件 → 同步映射：**

| 事件 | 同步动作 |
|------|---------|
| `chapter:created` | UPSERT chapter + project 记录 |
| `chapter:forked` | UPSERT 新 chapter + chapter_edge |
| `chapter:merged` | UPDATE chapter 状态 + INSERT chapter_edge |
| `chapter:dormant` / `chapter:woken` / `chapter:abandoned` / `chapter:frozen` | UPDATE chapter 状态 |
| `chapter:split` | UPSERT prefix + continuation + new fork chapters |
| `chapter:role_changed` | UPDATE chapter role |
| `chapter:commits_updated` | 同步 chapter_commits |
| `narrator:message` | UPSERT 最新消息 + refs + tool_calls |
| `narrator:status_changed` | UPDATE narrator 状态 |
| `narrator:forked` | UPSERT 新 narrator + 复制 message_refs |
| `narrator:title_updated` | UPDATE narrator title |
| `narrator:auto_commit` / `narrator:force_commit` | 同步 chapter_commits |
| `exploration:created` / `exploration:decided` / `exploration:abandoned` | UPSERT exploration_group |
| `merge:started` / `merge:completed` / `merge:cancelled` | UPSERT merge_session |
| `dependency:created` / `dependency:removed` | UPSERT/DELETE chapter_edge |

**同步策略：**
- 每个同步操作都是"查主库 → 写项目库"的模式
- 使用 `INSERT OR REPLACE`（SQLite UPSERT）保证幂等
- 异步执行，不阻塞主库操作
- 失败只记日志，不影响业务
- 对于高频事件（如 `narrator:message`），使用 debounce/批量合并

### 第 3 步：全量同步实现

全量同步用于：
- 项目首次启用备份
- 项目库损坏后重建
- 手动触发修复

```typescript
async fullSync(projectId: string): Promise<void> {
  const projectDb = projectDbManager.getDb(projectId);
  if (!projectDb) return;

  // 1. 同步 project 记录
  // 2. 同步所有 chapters（WHERE projectId = ?）
  // 3. 同步所有 chapter_edges（WHERE projectId = ?）
  // 4. 同步所有 exploration_groups（WHERE projectId = ?）
  // 5. 通过 chapters 找到所有 narrators（JOIN chapters ON chapterId）
  // 6. 通过 narrators 找到所有 messages、refs、tool_calls、patches
  // 7. 通过 chapters 找到所有 chapter_commits
  // 8. 通过 chapters 找到所有 merge_sessions

  // 使用事务包裹整个写入过程（项目库的事务）
  projectDb.transaction(() => {
    // 批量 INSERT OR REPLACE
  });
}
```

**数据量考虑：**
- 消息表可能很大（数万条），全量同步时分批处理（每批 1000 条）
- 使用 prepared statements 提高性能

### 第 4 步：项目导入（恢复）服务

**新文件：`server/services/project-import.ts`**

```typescript
// 职责：
// 从项目库恢复数据到主库

class ProjectImportService {
  // 从 gitPath 导入项目
  async importFromGitPath(gitPath: string): Promise<ImportResult>;
}
```

**导入流程：**

1. **检测项目库** — 检查 `<gitPath>/.narrafork/project.db` 是否存在
2. **验证 schema 版本** — 检查 `PRAGMA user_version`，确认兼容性
3. **读取项目记录** — 从项目库读取 `projects` 表
4. **冲突检测** — 检查主库中是否已存在相同 ID 的项目
   - 如果存在且 gitPath 相同 → 跳过或合并
   - 如果存在但 gitPath 不同 → 报错（ID 冲突）
   - 如果不存在 → 正常导入
5. **导入数据** — 按依赖顺序写入主库：
   ```
   projects → exploration_groups → chapters → chapter_edges
   → narrators → narrator_messages → narrator_message_refs
   → narrator_tool_calls → narrator_patches → chapter_commits
   → merge_sessions
   ```
6. **重建 FTS 索引** — 触发 FTS rebuild（或依赖触发器自动同步）
7. **验证完整性** — 检查外键约束

**ID 冲突处理策略：**
- nanoid 碰撞概率极低，默认信任原始 ID
- 如果确实冲突（极端情况），生成新 ID 并维护映射表
- 导入时使用 `INSERT OR IGNORE`（已存在的记录跳过）

**独立叙述者处理：**
- 项目库中的 narrators 都通过 chapter 关联到项目
- `chapterId IS NULL` 的独立叙述者不在项目库中（它们是全局的）
- 导入时不需要处理独立叙述者

### 第 5 步：API 端点

**新路由：`server/routes/project-db.ts`**（挂载到 `/api/projects/:id/backup`）

```
POST /api/projects/:id/backup/sync     — 触发全量同步
GET  /api/projects/:id/backup/status    — 查看备份状态（最后同步时间、记录数）
POST /api/projects/:id/backup/import    — 从项目库导入（恢复）
```

**项目创建时的集成：**
- 在 `POST /api/projects` 路由中，创建项目后自动初始化项目库
- 在 `createRootChapter` 完成后触发首次全量同步

### 第 6 步：.gitignore 管理

项目创建时，自动在 `<gitPath>/.gitignore` 中添加：
```
.narrafork/
```

如果 `.gitignore` 已存在且已包含该规则，则跳过。

### 第 7 步：进程生命周期

- **启动时**：不主动打开项目库连接（懒加载）
- **运行时**：事件触发时按需打开连接，LRU 淘汰闲置连接
- **退出时**：`closeAll()` 关闭所有项目库连接，执行 WAL checkpoint

## 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `server/lib/project-db.ts` | 新建 | 项目库连接管理器 + schema 初始化 |
| `server/services/project-db-sync.ts` | 新建 | 事件驱动的双写同步服务 |
| `server/services/project-import.ts` | 新建 | 项目导入（恢复）服务 |
| `server/routes/project-db.ts` | 新建 | 备份相关 API 端点 |
| `server/routes/projects.ts` | 修改 | 创建项目时初始化项目库 + .gitignore |
| `server/index.ts` | 修改 | 注册同步服务的事件监听器 + 退出时清理 |
| `server/lib/validators.ts` | 修改 | 添加导入相关的 Zod schema |

## 关键设计细节

### 消息共享问题

Fork 时 `narrator_messages` 是共享的（多个 narrator 通过 `narrator_message_refs` 引用同一条消息）。项目库中需要正确处理：

- **写入时**：消息按 `narratorId` 查询，同一条消息可能被多次写入（不同 narrator 触发）。使用 `INSERT OR REPLACE` 保证幂等。
- **导入时**：消息的 `narratorId` 字段指向创建者，refs 表维护引用关系。按依赖顺序导入即可。

### 高频事件的 Debounce

`narrator:message` 事件在 AI 对话中非常频繁（每条消息触发一次）。策略：

```typescript
// 按 narratorId 分组 debounce，500ms 内的多次触发合并为一次同步
private messageDebounceMap = new Map<string, Timer>();

onNarratorMessage(narratorId: string) {
  const existing = this.messageDebounceMap.get(narratorId);
  if (existing) clearTimeout(existing);
  this.messageDebounceMap.set(narratorId, setTimeout(() => {
    this.syncNarratorLatestMessages(narratorId);
    this.messageDebounceMap.delete(narratorId);
  }, 500));
}
```

### 错误处理

- 项目库写入失败 → 记录 warn 日志，不影响主库
- 项目库文件损坏 → 删除重建，触发全量同步
- gitPath 不存在（项目目录被删除）→ 跳过同步，记录 warn
- 导入时主库写入失败 → 事务回滚，返回错误

### Schema 版本升级

当项目库 schema 需要变更时：
1. 递增 `PRAGMA user_version`
2. 在 `initProjectDb` 中检测版本，执行迁移 SQL
3. 或者直接删除重建（项目库是备份，可以从主库全量重建）

推荐后者——简单可靠，全量同步的成本可接受。

## 不在本方案范围内

- 前端 UI（备份状态展示、手动触发同步按钮）— 后续迭代
- 自动定时全量同步 — 事件驱动的增量同步已足够
- 跨机器同步（通过 git push 项目库）— 需要额外设计
- 独立叙述者的备份 — 它们不属于任何项目
