# NarraFork 0.6.0 本地升级、迁移与打包交接指南

> 整理日期：2026-08-19  
> 适用场景：从本机 0.5.24 升级到 0.6.0 测试包，使用 macOS arm64、launchd supervisor 和现有 `~/.narrafork/narrafork.db`。

## 1. 本次事故结论

本次升级实际遇到了两个独立问题，不能混为一谈。

### 1.1 第一层：0.6.0 新表没有进入旧数据库

0.6.0 代码已经开始查询以下表：

- `narrator_grants`
- `acl_grants`
- `acl_events`
- `chat_rooms`
- `chat_room_members`
- `chat_messages`
- `chat_attachments`

旧数据库没有这些表时，错误日志会出现：

```text
SQLiteError: no such table: chat_rooms
SQLiteError: no such table: acl_grants
```

测试包必须嵌入包含这些表的迁移，并在启动时执行。

当前测试分支中对应文件是：

```text
drizzle/0001_manual_0_6_0_acl_chat.sql
drizzle/meta/_journal.json
server/generated/embedded-migrations-data.ts
```

注意：`server/generated/embedded-migrations-data.ts` 是打包时生成的，不应靠手工维护。

项目正式规则仍然是：

```text
修改 server/db/schema.ts
→ bun run db:generate
→ bun run db:migrate
```

这次使用的是分支上已经存在的手工迁移补救文件。后续不要继续随意手改 `drizzle/`，更不能删除用户数据库来规避迁移问题。

### 1.2 第二层：数据库中的空字符串被当作 JSON 解析

新表成功创建后，日志仍然出现：

```text
SyntaxError: JSON Parse error: Unexpected EOF
```

只读扫描所有 Drizzle `{ mode: "json" }` 列后，定位到两条坏数据：

```text
projects.traits = ''
user_preferences.traits = ''
```

Drizzle 读取这两列时会执行等价于：

```ts
JSON.parse("")
```

因此抛出 `Unexpected EOF`。

修复位于：

```text
server/db/index.ts
```

启动时，在任何业务查询读取这些 JSON 列之前，执行幂等归一化：

```sql
UPDATE projects
SET traits = '[]'
WHERE traits IS NULL OR traits = '' OR json_valid(traits) = 0;

UPDATE user_preferences
SET traits = '[]'
WHERE traits IS NULL OR traits = '' OR json_valid(traits) = 0;
```

原本已有 `narrators.traits` 和 `narrators.substatus` 的同类修复；本次只是补齐项目层和用户层。

对应修复 commit：

```text
b1369b90 fix: normalize invalid trait JSON during startup
```

已推送到：

```text
remote: qnap
URL: ssh://git@git.qnap.anzhi.app:222/zhuanzhi/narrafork.git
branch: chapter/selfuse-NZl4H0
```

## 2. 正确的本地打包方式

### 2.1 不要使用的命令

本机测试包不要直接运行：

```bash
bun run build:macos-arm64
# 或
bun scripts/build-cross-platform.ts --platform=darwin-arm64
```

当前私有工作树缺少：

```text
scripts/lib/binary-metadata
```

因此完整跨平台发布脚本会在 Phase 2 后处理阶段失败：

```text
Cannot find module './lib/binary-metadata'
```

这不是应用代码或迁移失败，而是正式发布后处理依赖缺失。

### 2.2 本地测试包的正确命令

使用专门的本地构建脚本：

```bash
bun scripts/build-local.ts
```

它执行：

1. Vite 前端构建
2. 准备 `@parcel/watcher` 各平台原生包
3. 生成嵌入式前端资源
4. 从 `drizzle/meta/_journal.json` 收集 SQL，生成嵌入迁移数据
5. 生成 build info 与 changelog
6. 使用 Bun compile 构建 macOS arm64 单文件二进制
7. 跳过签名、SHA、zstd patch 和 `latest.yml` 等正式发布后处理

成功产物：

```text
dist/narrafork-0.6.0-macos-arm64
```

### 2.3 一个容易误判的 build commit 细节

`build-local.ts` 的 banner commit 来自：

```bash
git rev-parse --short HEAD
```

它不会反映未提交改动。

因此，如果在修复尚未 commit 时打包：

- 二进制会包含工作区中的修复代码；
- 但启动 banner 仍显示打包时的旧 `HEAD`。

本次第一次修复包就是这种情况：实际包含 JSON 修复，但 banner 显示 `c1c793c1`。

为了可复现，推荐顺序是：

```text
完成代码 → commit → build-local.ts → 安装
```

不要只根据 banner commit 判断某个脏工作区修复有没有进入二进制。

## 3. 正确的本机安装方式

构建成功后：

```bash
cp "dist/narrafork-0.6.0-macos-arm64" \
  "$HOME/.local/bin/narrafork-0.6.0"

chmod +x "$HOME/.local/bin/narrafork-0.6.0"

ln -sfn \
  "$HOME/.local/bin/narrafork-0.6.0" \
  "$HOME/.local/bin/narrafork"
```

核对：

```bash
ls -l \
  "$HOME/.local/bin/narrafork" \
  "$HOME/.local/bin/narrafork-0.6.0"
```

## 4. launchd supervisor 的关键陷阱

本机 LaunchAgent 启动的不是 `narrafork` 本身，而是：

```text
~/.local/bin/narrafork-launchd-supervisor
```

supervisor 选择二进制的优先级是：

1. `~/.narrafork/updates/placed-update.json` 中的 `newBinaryPath`
2. `~/.local/bin/narrafork` symlink

所以，只更新 symlink 不够。

如果 `placed-update.json` 仍指向 0.5.24，下一次 supervisor 启动仍会回到 0.5.24。

升级 0.6.0 时，交接文件需要类似：

```json
{
  "version": "0.6.0",
  "fromVersion": "0.5.24",
  "fileName": "narrafork-0.6.0",
  "newBinaryPath": "/Users/zhuanzhi/.local/bin/narrafork-0.6.0",
  "updatePath": "/Users/zhuanzhi/.local/bin/narrafork-0.6.0",
  "placed": true,
  "placedAt": "2026-08-19T11:50:00.000Z"
}
```

如果用户手动回退，这个文件也会被改回旧版本。这属于预期操作，不要把它误诊为 supervisor 自己回退。

## 5. 启动与验收

本机服务标签：

```text
gui/501/dev.narrafork.server
```

用户可执行：

```bash
launchctl kickstart -k gui/501/dev.narrafork.server
```

注意项目铁律：不要停止或接管承载当前 agent loop 的 `bun run start:dev` 进程。这里描述的是用户明确控制的 launchd 生产式本机服务。

### 5.1 正常启动日志应出现

日志文件：

```text
~/.narrafork/launchd-supervisor.log
~/.narrafork/launchd-supervisor.err.log
```

本次成功验收的关键日志：

```text
[narrafork-supervisor] starting /Users/zhuanzhi/.local/bin/narrafork-0.6.0
Database migrated from embedded migration data
Normalized invalid project traits JSON to empty array, count=1
Normalized invalid user traits JSON to empty array, count=1
NarraFork v0.6.0
```

### 5.2 不要被旧 err 日志误导

日志是追加写入的。搜索整个 `err.log` 一定还能看到旧版本留下的：

```text
no such table
JSON Parse error
```

必须按本次启动的绝对时间过滤，或先记录行数再读取新增部分。

例如：

```bash
wc -l \
  "$HOME/.narrafork/launchd-supervisor.log" \
  "$HOME/.narrafork/launchd-supervisor.err.log"
```

然后只查看 kickstart 后新增的区段。

本次 0.6.0 于 `2026-08-19 11:51:55 +08:00` 启动后，错误日志中没有再出现：

```text
JSON Parse error
no such table
Unhandled error
```

### 5.3 数据库只读复查

确认新表：

```bash
sqlite3 "$HOME/.narrafork/narrafork.db" \
"SELECT name
 FROM sqlite_master
 WHERE type='table'
   AND name IN (
     'chat_rooms',
     'chat_messages',
     'chat_room_members',
     'chat_attachments',
     'narrator_grants',
     'acl_grants',
     'acl_events'
   )
 ORDER BY name;"
```

确认 traits JSON 已修复：

```bash
sqlite3 "$HOME/.narrafork/narrafork.db" \
"SELECT 'projects', COUNT(*)
 FROM projects
 WHERE traits IS NOT NULL AND json_valid(traits)=0
 UNION ALL
 SELECT 'user_preferences', COUNT(*)
 FROM user_preferences
 WHERE traits IS NOT NULL AND json_valid(traits)=0;"
```

验收结果必须是：

```text
projects|0
user_preferences|0
```

## 6. 如何系统扫描 Drizzle JSON 列

当堆栈出现：

```text
JSON.parse
map
execute
```

而不是明显的路由 body 解析上下文时，应优先怀疑 Drizzle JSON 列中的坏数据。

可以只读扫描 schema 中所有 `SQLiteTextJson` 列：

```bash
bun -e '
import { Database } from "bun:sqlite";
import * as schema from "./server/db/schema.ts";
import { getTableColumns, getTableName } from "drizzle-orm";

const db = new Database(`${process.env.HOME}/.narrafork/narrafork.db`, {
  readonly: true,
});
const seen = new Set();

for (const value of Object.values(schema)) {
  try {
    const table = getTableName(value);
    if (seen.has(table)) continue;
    seen.add(table);

    for (const [property, column] of Object.entries(getTableColumns(value))) {
      if (column.constructor?.name !== "SQLiteTextJson") continue;
      const name = column.name;
      const rows = db
        .query(
          `SELECT rowid, quote(${JSON.stringify(name)}) AS value
           FROM ${JSON.stringify(table)}
           WHERE ${JSON.stringify(name)} IS NOT NULL
             AND json_valid(${JSON.stringify(name)}) = 0
           LIMIT 20`,
        )
        .all();
      if (rows.length) {
        console.log(JSON.stringify({ table, column: name, property, rows }));
      }
    }
  } catch {}
}
'
```

这条命令是只读的，不会修改数据库。

## 7. 常见错误判断

### 错误 1：看到 JSON Parse 就认为不是迁移问题

首次启动时，缺表错误和 JSON 错误可能同时存在。应分别解决：

```text
先确认 schema 对象存在
再确认 JSON 数据有效
```

### 错误 2：日志写了 migrated 就认为数据库完全健康

`Database migrated from embedded migration data` 只说明迁移执行完成，不代表历史数据符合新列的运行时编码要求。

### 错误 3：只改 symlink

supervisor 会优先读取 `placed-update.json`，所以 symlink 正确仍可能启动旧版。

### 错误 4：用正式跨平台构建脚本打本地测试包

当前环境缺 `binary-metadata` 私有模块。使用 `build-local.ts`。

### 错误 5：为解决迁移直接删除数据库

绝对不要删除：

```text
~/.narrafork/narrafork.db*
drizzle/
```

数据库包含用户数据。优先使用幂等迁移、列修复和数据归一化。

### 错误 6：把无关的 TypeScript 错误归因于本次修复

本次执行 `bunx tsgo --noEmit` 时观察到一个既有错误：

```text
server/services/narrator-compact.ts:913
DbMessage.narratorId 的 string | undefined 与 string 不兼容
```

它与 traits JSON 修复无关。应单独处理，不应阻止确认本次数据库根因。

## 8. 回退原则

如需回退到 0.5.24：

1. 明确把 `placed-update.json` 指回 0.5.24；
2. 确认旧二进制存在且可执行；
3. 再由用户 kickstart；
4. 不删除数据库。

本次新增表是加法式 schema，0.5.24 不读取这些表；traits 从非法空字符串归一化为合法 `[]` 也向后兼容。

## 9. 最终验收清单

- [ ] `package.json` 版本是 `0.6.0`
- [ ] 新表迁移已进入 journal 和嵌入迁移数据
- [ ] 使用 `bun scripts/build-local.ts` 构建成功
- [ ] 产物为 `dist/narrafork-0.6.0-macos-arm64`
- [ ] 二进制复制到 `~/.local/bin/narrafork-0.6.0`
- [ ] `~/.local/bin/narrafork` symlink 指向 0.6.0
- [ ] `placed-update.json` 指向 0.6.0
- [ ] supervisor 日志确认实际启动 0.6.0
- [ ] 启动日志确认 migration 执行
- [ ] `projects.traits` 非法 JSON 数量为 0
- [ ] `user_preferences.traits` 非法 JSON 数量为 0
- [ ] 本次启动时间之后无 `no such table`
- [ ] 本次启动时间之后无 `JSON Parse error`
- [ ] 本次启动时间之后无相关 `Unhandled error`
