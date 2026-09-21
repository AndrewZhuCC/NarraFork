# 通知中心 Phase 1 — 子代理协作规格

> **工作目录（必读）：** 所有编码、测试、静态检查都在 worktree 内执行：
>
> ```text
> /home/fulcrum/projects/narrafork-notification-center
> ```
>
> 分支：`feat/notification-center`  
> 基点：`8b3a7d3f`（`main` HEAD，含 git 提交图 / queued 工具相位等）  
> **禁止**把产物写回 `/home/fulcrum/projects/narrafork`（主工作区有未提交改动）。  
> **禁止**停止/重启任何 `bun run start:dev` / NarraFork 运行中进程。
>
> 本文是子代理之间共享的唯一规格源。不要依赖 `spec://` 虚拟目录（子代理不可见）。  
> 状态变更时更新本文 §0「协作看板」，不要另起私有真相源。

---

## 0. 协作看板（主代理/子代理共同维护）

| 任务包 | 负责人 | 状态 | 产出文件 | 验收 |
|--------|--------|------|----------|------|
| A Schema + Shared 契约 | A-subagent + 主代理 | done | `server/db/schema.ts`（`notifications` + 3 索引）、`shared/notification-center.ts`、增量迁移 **`drizzle/0181_mean_gabe_jones.sql`**（已从主仓拷贝 `drizzle/` 基线后 `db:generate`；错误全量 0000 已移除） | fresh `NARRAFORK_HOME` migrate ✓（0181 仅 CREATE notifications）；A 文件 tsgo/biome ✓；**生产库本阶段不迁移**（主仓脏 schema 可能仍超前；合并期再 apply）；测试一律 `NARRAFORK_HOME`/isolate |
| B Service + API | B-subagent | done | `server/services/notification-center-service.ts`（import `@shared/notification-center` 再导出）、`server/routes/notifications.ts`（保留 test-dingtalk/feishu + 新增 4 API）、`server/lib/validators/notifications.ts` + barrel、`server/services/__tests__/notification-center-service.test.ts` | `bun test --isolate server/services/__tests__/notification-center-service.test.ts` **15 pass / 0 fail**；owned 文件 biome ✓；tsgo 本包文件 0 错（仓内仍有无关 routeTree/ws-bridge 报错）。API：`GET /api/notifications`、`GET /api/notifications/unread-count`、`POST /api/notifications/read`、`POST /api/notifications/:id/delete`（JWT，他人资源 404）。`app.ts` 已挂载 `/api/notifications`，未改。导出给 C：`recordNotifications`；给 D：`onNotificationCenterChanged`。markRead 空 body=全量已读；`ids:[]` 为 no-op |
| C Fan-out 接线 | C-subagent | done | `server/services/notification-fanout.ts`；chat hook：`chat-notify.ts` `onMessageCreated`（约 L115–126）；permission hook：`initNotificationFanout` → `narrator:permission_request`（`main.ts` L1528）；`__tests__/notification-fanout.test.ts` | `bun test --isolate …notification-fanout.test.ts` **12 pass**；`chat-notify.test.ts` **9 pass**；biome 4 文件通过；C 文件 tsgo 0 错。契约：`@shared/notification-center` + B `recordNotifications`（测试经 `setRecordNotifications` mock） |
| D WS + 前端 Drawer | D-subagent | done | `frontend/components/notifications/`（Bell/Drawer/ListItem/types/useNotificationCenter + index）、`frontend/lib/api/notifications.ts`（import `@shared/notification-center`）、`AppRootLayout.tsx` Header 右侧铃铛（OutputStatsBadge 与 feedback 之间）、event-bus + narrator-ws-types + narrator-ws：`notification_center_changed`（user-scoped，50ms/用户合并）+ `onNotificationCenterChanged` → eventBus 桥 | `bun test --isolate frontend/components/notifications server/lib/__tests__/notification-center-ws-bridge.test.ts` **27 pass / 0 fail**；D 文件 biome ✓；tsgo D 相关文件 0 错（仓内仍有无关 routeTree.gen 缺失等）。i18n 用包 E `nav` ns。query keys：`["notifications","unread-count"]` / `["notifications","list",{kind,status}]`。WS 事件名 `notification_center_changed`（常量：frontend types + narrator-ws-types 字符串对齐，不进 shared） |
| E 设置最小策略 + i18n | E-subagent | done | `frontend/locales/{en,zh-CN}/nav.json`（通知中心键）；§6.3 最终键契约；无需改 `i18n.ts` | `bun server/scripts/check-i18n.ts` 0 error + en/zh 键集合一致 |
| F 集成验收 + 静态检查 | F-subagent | done | `server/services/__tests__/notification-center-integration.test.ts`（C 遗留真实路径）；§0/§8.3 勾选状态 | **静态：** 通知中心 owned 文件 `bunx biome check` 全过；`bunx tsgo --noEmit` 对通知中心 **0 错**（全仓失败为环境/基线：缺 `frontend/routeTree.gen.ts` 连带 route 路径类型、缺 `@server/generated/embedded-licenses`、`server/routes/narrators.ts` whitelist validator 导出名、`vscode-extension` 缺 vscode 类型；全仓 biome 159 errors 来自 docs/task-call-challenges 等与通知中心无关内容）。**isolate 测试：** notification-center-service **15** + notification-fanout **12** + chat-notify **9** + ws-bridge **2** + frontend/components/notifications **25** = **63 pass**；`tests/frontend/i18n-resources.test.ts` **1 fail**（locale-sensitive formatting 守卫命中 GitChangesTab / NarratorPublicShareManager / PublicSharedNarratorPage，**基线无关**，非 A–E 文件）。**集成：** `bun test --isolate …notification-center-integration.test.ts` **4 pass / 0 fail**（preload 隔离 `NARRAFORK_HOME` + 迁移 182 含 0181；真实 `recordNotifications` 无 mock）。**冒烟：** Bell 挂 AppRootLayout L724；fanout 默认 resolve B 的 `recordNotificationsImpl`；record/markRead/delete → `notifyChanged` → narrator-ws 桥 eventBus `notification_center_changed`；`app.route("/api/notifications", notificationRoutes)` L387；i18n en/zh 23 键与 Drawer/Bell 一致；unread 用 `LIMIT cap+1`，fan-out 成员/接收者有 cap，list 不读 contentJson。**F 修复：** 无 A–E 逻辑修复；新增集成测试并修正断言以匹配 B 的 clamp（fan-out 加 `…` 后 max+1 字符串会被 B 裁回 max、不保留省略号——见遗留风险） |

**环境：** worktree 已 `bun install`（node_modules 就绪）。

### 评审后修复与已知策略（2026-09-21）

| 项 | 结论 |
|----|------|
| **M1 resolved 点击** | **已修（方案 A）**：权限已决时 `displayStatus=resolved` + `sourceAlive=true`（narrator 仍可读）；前端仅 `gone` 为惰性；resolved 点击会 markRead 并跳转 `/narrators/:id`；列表显示「已处理」徽章 |
| **M2 权限受众** | **P1 策略（有意保守，非回归）**：权限通知接收者 = owner ∪ 显式 ACL grantees ∪ recent-tabs，再 `canRead` 过滤；`visibility=everyone/project` **不**扩全体项目成员（under-report 只延迟通知，over-report 是泄露）。P2 再评估项目成员投影 |
| m1 preview 省略号 | **已修**：fan-out 与 service 截断均为 `≤max` 且保留 `…` |
| m2 Load more 键 | **已修**：使用 `nav:notificationLoadMore`；另增 `nav:notificationResolved` |
| m3 重复 insert 通知 | **已修**：`ON CONFLICT DO NOTHING` 未实际插入时不 `notifyChanged` |
| M3 有界 N+1 / M4 权限集成 / M5 保留 job | 遗留，P2；不阻塞合并 |

状态枚举：`todo` → `doing` → `done` / `blocked`（blocked 必须写清缺什么）。

**顺序约束：**

1. A 完成前，B/C 不得改 schema；B 的 service API 签名定稿前，C/D 不得硬编码内部实现。
2. C/D 可在 B 合并前用 **契约 mock**（按 §4 类型）写测试。
3. F 在 A–E 主路径合入后跑全量。

**并行边界（文件所有权）：** 同一文件禁止两名子代理同时写；跨界只允许通过本文契约 + 主代理调度。

---

## 1. 产品目标（Phase 1）

类手机通知中心：用户在一个入口看到「需要知道的事」，可筛选、标已读、跳回源。

| 决策项 | 锁定值 |
|--------|--------|
| 入口 | Header **铃铛 + Drawer**（非独立路由） |
| 权限条目 | Phase 1 **只跳转**到叙述者会话，不内嵌审批 |
| @提及 | Phase 1 **不实现**；约定见 §1.1（后续接入用） |
| 类型范围 | 仅 `chat_message` + `permission_request` |
| 通知 vs 待办 | 通知=事件史（unread/read）；待办状态在源系统；标已读 ≠ 已处理 |
| 展示关联 | 交互可用 username；**落库只存 userId**，防改名断链 |

### 1.1 提及数据约定（Phase 2 预留，勿在 P1 实现解析）

- 输入/补全：`@username`（折叠匹配）。
- 持久：只存 `userId` / `mentioneeUserId`，不把裸 `@` 字符串当外键。
- 展示名在渲染时解析；用户改名不影响通知关联。
- 与叙述者 `@handle` 命名空间隔离；解析可复用 [shared/narrator-handle.ts](../../shared/narrator-handle.ts) 的折叠/边界思路，但用户与 narrator 分命名空间。

### 1.2 明确不做（Phase 1）

- 内嵌权限审批 / 批量批准
- 权限通知扩到 project/everyone 全体可读成员（P1 受众见 §0 M2）
- `@username` 提及解析与 `mention` 类型落库
- `session_error` / 统一 Retry
- 消息级 `/messages?seq=` 深链（仅 room 级）
- 改钉钉/飞书语义、替代 Human Attention 专业收件箱
- 在 eventBus/WS 广播通知**正文**
- 从 HEAD 之外的脏主工作区合并未提交改动

---

## 2. 架构

```text
源系统（状态权威）                    通知中心（事件史 + 未读）
─────────────────                    ─────────────────────────
chat DM 未读水位 lastReadSeq   →→→   chat_message 通知行
Human Attention / pending perm →→→   permission_request 通知行
（P2）mention 解析             →→→   mention
（P3）session error            →→→   session_error
                                    ↓
                            Header 铃铛徽章 + Drawer
                                    ↓
                            深链：/messages?room=  |  /narrators/:id
```

- **通知表是投影的持久化，不是源状态副本机。** `resolved/gone` 列表时按源派生，库内持久态只有 `unread|read`。
- **审批/回答仍走原 API**（Human Attention、narrator permissions）。中心不新增审批端点。
- **聊天标通知已读不推进 `chat_room_members.lastReadSeq`**；只有打开房间 markRead 才推进未读水位。

---

## 3. 数据模型（任务包 A）

### 3.1 表 `notifications`

改 [server/db/schema.ts](../../server/db/schema.ts)，然后：

```bash
bun run db:generate
bun run db:migrate
```

**禁止手动编辑 `drizzle/` 下任何文件。**

字段（Drizzle 概念模型）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | text PK | `generateId()`（21 chars，`@server/lib/id`） |
| `userId` | text FK users.id | 接收者；`onDelete: cascade` |
| `kind` | text/json | `"chat_message" \| "permission_request"` |
| `projectId` | text null | 尽力冗余，便于过滤；可 null |
| `chapterId` | text null | 同上 |
| `narratorId` | text null | 权限类必填；聊天可 null |
| `title` | text | 写入时生成，有界 |
| `preview` | text | 写入时截断，默认 `""` |
| `linkJson` | json | 见 §3.2，列表不重算 |
| `sourceKey` | text | 去重键（见 §3.3） |
| `status` | text | 持久仅 `unread` \| `read`，默认 `unread` |
| `createdAt` | timestamp_ms | 必填 |
| `readAt` | timestamp_ms null | 标已读时间 |

索引：

- **unique** `(userId, kind, sourceKey)`
- `(userId, createdAt)` 列表游标
- `(userId, status, createdAt)` 未读

### 3.2 `link` JSON

```ts
export type NotificationLink =
  | { type: "chat_room"; roomId: string }
  | { type: "narrator"; narratorId: string };
```

Phase 1 **只允许**这两种。不要预留无法路由的假字段。

### 3.3 去重键 `sourceKey`

| kind | sourceKey |
|------|-----------|
| `chat_message` | `chat_message` 的消息 id（`chat_messages.id`） |
| `permission_request` | 权限 `toolCallId`（`narrator_tool_calls.id`） |

同一用户对同一 `(kind, sourceKey)` 只 insert 一次（unique 冲突则忽略）。  
**重连、WS 重订阅、IM 重发不得制造第二行。**

自动重试/连接恢复类事件 Phase 1 不插权限通知；只在**新的** permission offer（tool call 进入 pending）时插入。

### 3.4 Shared 契约

新建 [shared/notification-center.ts](../../shared/notification-center.ts)：

```ts
export type NotificationKind = "chat_message" | "permission_request";
export type NotificationPersistentStatus = "unread" | "read";
/** 展示时派生，不落库 */
export type NotificationDisplayStatus = "unread" | "read" | "resolved" | "gone";

export interface NotificationLink {
  type: "chat_room" | "narrator";
  roomId?: string;
  narratorId?: string;
}

export interface NotificationListItem {
  id: string;
  kind: NotificationKind;
  projectId: string | null;
  chapterId: string | null;
  narratorId: string | null;
  title: string;
  preview: string;
  link: NotificationLink;
  sourceKey: string;
  status: NotificationPersistentStatus;
  displayStatus: NotificationDisplayStatus;
  createdAt: number;
  readAt: number | null;
  /** 权限类：源是否仍可打开；Phase 1 仅跳转时用于灰态 */
  sourceAlive?: boolean;
}

export interface NotificationListPage {
  items: NotificationListItem[];
  nextCursor: string | null;
}

export interface NotificationUnreadCounts {
  total: number; // 有界：>阈值可显示为 cap+，见实现
  chat_message: number;
  permission_request: number;
  /** true 表示因 cap 导致 total 为下界 */
  lowerBound?: boolean;
}

export const NOTIFICATION_PREVIEW_MAX_LENGTH = 120;
export const NOTIFICATION_LIST_DEFAULT_LIMIT = 30;
export const NOTIFICATION_LIST_MAX_LIMIT = 50;
export const NOTIFICATION_UNREAD_COUNT_CAP = 99;
export const NOTIFICATION_FANOUT_MAX_RECIPIENTS = 200;
```

---

## 4. Service 与 API（任务包 B）

### 4.1 Service：`server/services/notification-center-service.ts`

建议导出（签名可微调，但语义不可破）：

```ts
export interface RecordNotificationInput {
  userId: string;
  kind: NotificationKind;
  sourceKey: string;
  title: string;
  preview: string;
  link: NotificationLink;
  projectId?: string | null;
  chapterId?: string | null;
  narratorId?: string | null;
  createdAt?: number;
}

/** 有界 fan-out；重复插入忽略；失败只 log，不抛回源事务 */
export async function recordNotifications(inputs: RecordNotificationInput[]): Promise<void>;

export async function listNotifications(params: {
  userId: string;
  kind?: NotificationKind;
  /** unread-only 或 all */
  status?: "unread" | "all";
  cursor?: string | null;
  limit?: number;
}): Promise<NotificationListPage>;

export async function getUnreadCounts(userId: string): Promise<NotificationUnreadCounts>;

export async function markNotificationsRead(params: {
  userId: string;
  ids?: string[];
  before?: number;
  kind?: NotificationKind;
}): Promise<{ updated: number }>;

export async function deleteNotification(userId: string, id: string): Promise<void>;
```

实现约束：

1. `preview` / `title` 在**写入时**截断到约定上限；service 再 clamp 一次。
2. `list` 只 select 通知表列；**禁止** join 消息 `contentJson` / plan 正文。
3. `displayStatus`：
   - 库内 read/unread；
   - `permission_request`：对当前页有界查询源 tool call 是否仍 pending/可见；不是则 `resolved` 或 `gone`；
   - `chat_message`：房间对用户仍可读则至少 `read/unread`，房间无权访问 → `gone`。
4. 未读计数：每 kind 在索引上 `LIMIT cap+1` 判断是否触顶，**禁止**无界 `COUNT(*)` 全表。
5. `markRead`：只更新该 `userId` 行；单调（已 read 不改 `readAt`）。
6. 保留策略：常量保留最近 500 条或 30 天（先常量，不做 UI）；**清理不得放在普通请求路径**（可 TODO 后台 job）。

### 4.2 HTTP API

扩展现有 [server/routes/notifications.ts](../../server/routes/notifications.ts)（目前仅钉钉/飞书 test webhook）：

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/api/notifications` | JWT | query: `kind?`, `status?=unread\|all`, `cursor?`, `limit?` |
| GET | `/api/notifications/unread-count` | JWT | 返回 `NotificationUnreadCounts` |
| POST | `/api/notifications/read` | JWT | body: `{ ids?: string[], before?: number, kind?: NotificationKind }` |
| POST | `/api/notifications/:id/delete` | JWT | 仅本人 |

- 校验放 `server/lib/validators/`（按资源拆分，遵循现有范式）。
- 错误：`AppError` 子类；非本人资源用 404（避免确认存在）。
- **列表响应不得包含聊天正文以外的长字段**；preview 已在写入时截断。

### 4.3 Zod 要点

- `limit`：1..50
- `status`：`unread` | `all`
- `kind`：枚举与 shared 一致
- `before`：非负 timestamp；`ids`：数组上限建议 200

---

## 5. Fan-out（任务包 C）

统一入口：`notification-center-service.recordNotifications`。

| 源 | 挂载点（实现时以当前 worktree 为准） | 条件 | kind / link |
|----|--------------------------------------|------|-------------|
| DM 新消息 | `server/services/chat-notify.ts` / `chat:message_created` 消费侧 | 成员、非发送者、未 mute、对该房 `seq > lastReadSeq`（或当前快照未读）、可读 | `chat_message` / `{type:"chat_room", roomId}` |
| 权限 pending | 权限 offer 路径或 `narrator:attention` reason=`waiting_permission` 的服务端收件侧 | 对 narrator **可读**用户；排除已决定的 toolCall；sourceKey=`toolCallId` | `permission_request` / `{type:"narrator", narratorId}` |

硬规则：

1. **eventBus 事件不携带正文**；preview 生成必须发生在**已鉴权服务路径**或收件人维度读取，不得把他人消息正文广播出去。
2. DM preview：写入时对**该接收者可见的**消息正文截断 ≤120 字符；软删/无权限 → 空 preview + 标题仍可用。
3. 权限 preview/title：用工具名/叙述者标题等摘要；**不要**塞完整 tool input（可能含密钥/大 payload）。
4. fan-out 失败：log，**不回滚**已提交的聊天/权限写入。
5. 单次 fan-out 人数 `≤ NOTIFICATION_FANOUT_MAX_RECIPIENTS`；超出按业务规则截断（如最近活跃），并 log。
6. 与 IM webhook **正交**：站内 always 尝试入通知（可读过滤后）；钉钉/飞书仍按 `userPreferences.notifyOnWaiting` 等，不因关 IM 而丢站内史。
7. 权限通知**不要**在 WS 重订阅 snapshot 上重复 insert（依赖 unique + 仅 offer 时 hook）。

标题文案（en + zh-CN 各一份，最终键名由任务包 E 落 locales）：

| kind | 标题示例（zh） | preview 示例 |
|------|----------------|--------------|
| chat_message | 发送者显示名或「新消息」 | 消息正文截断 |
| permission_request | `{narratorTitle}` 或「权限请求」 | `{toolName}: {短摘要}` |

---

## 6. WS 与前端（任务包 D）

### 6.1 WS

- 事件名：`notification_center_changed`
- payload：有界，例如 `{}` 或 `{ kinds: NotificationKind[] }`，**无标题/无正文/无列表**
- 发出时机：`recordNotifications` / `markRead` / `delete` 成功后
- 前端：收到后标 stale + 更新徽章；**禁止**每条通知全量 refetch 造成 SQLite N+1；列表可 invalidate 一次或 patch 未读数后按需拉第一页

### 6.2 UI

**入口：** [frontend/components/AppRootLayout.tsx](../../frontend/components/AppRootLayout.tsx) `AppShell.Header` 右侧 `Group`（约 L721–735），搜索旁：

- `ActionIcon` 铃铛（`@tabler/icons-react` 的 `IconBell` 或等价）
- 徽章：未读 `total`，超过 cap 显示 `99+`
- 打开 Drawer

**Drawer：** 建议新组件目录，例如：

```text
frontend/components/notifications/
  NotificationBell.tsx
  NotificationCenterDrawer.tsx
  NotificationListItem.tsx
  useNotificationCenter.ts
```

行为：

1. 筛选 SegmentedControl：`全部` | `待处理` | `消息` | `权限`
   - 全部：`status=all`
   - 待处理：`kind=permission_request` + 源仍 alive（或前端按 `displayStatus`）
   - 消息 / 权限：按 kind
2. 时间倒序；显示 title、preview、相对时间、kind 图标
3. 操作：
   - 点击行 → 标已读 + **navigate**
     - `chat_room` → `/messages?room={roomId}`
     - `narrator` → `/narrators/{narratorId}`（params 按 TanStack Router 现状）
   - 顶部「全部标已读」→ `POST /api/notifications/read`（可带 `before=now`）
4. `displayStatus === "gone"`：灰态「已失效」，不可当待办跳转或跳转后由目标页兜底
5. Phase 1 **不**渲染 Approve/Deny 按钮
6. 移动端 Drawer 全屏行为与现有 Mantine Drawer 惯例一致
7. 打开 Drawer 时若窗口 focus，**不要**再重复弹 PWA/toast

**Hooks：** `@tanstack/react-query`，query key 约定：

- `["notifications", "list", { kind, status }]`
- `["notifications", "unread-count"]`

**导航徽章：** 侧边栏 `/messages` 的 `chatUnread` **保留**；通知中心铃铛是聚合入口，不删除专业入口。

### 6.3 i18n（任务包 E — **最终契约，D/C 必须按此引用**）

- **namespace：`nav`**（复用现有 ns，**不**新建、**不**改 `frontend/lib/i18n.ts`）。
  - 理由：铃铛 + Drawer 属 App Shell 铬层；`nav` 已在 `APP_SHELL_NAMESPACES` 中对全部登录路由预加载，Drawer 打开时无 Suspense 缺口。
  - 文件：[frontend/locales/en/nav.json](../../frontend/locales/en/nav.json)、[frontend/locales/zh-CN/nav.json](../../frontend/locales/zh-CN/nav.json)
- **包 D 引用方式（禁止改 D 的 tsx 由 E 代写）：**

  ```tsx
  const { t } = useTranslation("nav");
  t("notificationCenter")
  t("notificationFilterAll")
  // …完整路径见下表
  ```

- **默认标题策略（C 写入 + D 展示回落）：**
  - fan-out 能解析到发送者/叙述者标题时，`title` 直接写该显示名；否则 title 可为空或写英文 fallback。
  - 前端 `title` 为空时回落：`chat_message` → `nav:notificationChatTitle`（或有 sender 时 `nav:notificationChatTitleNamed`）；`permission_request` → `nav:notificationPermissionTitle`（或有 narrator 时 `nav:notificationPermissionTitleNamed`）。
  - 权限 preview 模板：`nav:notificationPermissionPreview`（`{{tool}}` + `{{summary}}`，**禁止**塞完整 tool input）。
- **Phase 1 默认策略（无设置页）：** 站内通知中心对「可读接收者」**始终**记录 `chat_message` + `permission_request`；与 IM/PWA `userPreferences.notify*` **正交**（§5 硬规则 6）。P2 预留偏好键名（勿在 P1 落库/UI）：`notificationCenterEnabled`、`notificationCenterChatEnabled`、`notificationCenterPermissionEnabled`（默认均为 true；关闭只停站内中心 fan-out，不停 IM）。
- **最终键路径一览（D 必须按 `nav:<key>` 引用）：**

| 完整键路径 | en | zh-CN | 用途 |
|------------|----|-------|------|
| `nav:notificationCenter` | Notifications | 通知 | Drawer 标题 / 入口 label |
| `nav:notificationCenterTooltip` | Open notifications | 打开通知中心 | 铃铛 tooltip |
| `nav:notificationUnreadBadge` | `{{count}} unread notifications` | `{{count}} 条未读通知` | 徽章无障碍 label（count < 99） |
| `nav:notificationUnreadBadgeCap` | 99+ unread notifications | 99+ 条未读通知 | 触顶徽章 a11y |
| `nav:notificationLoading` | Loading notifications… | 正在加载通知… | 列表加载中 |
| `nav:notificationEmpty` | No notifications yet | 暂无通知 | 空状态（无筛选或全部） |
| `nav:notificationEmptyFiltered` | No notifications in this filter | 当前筛选下暂无通知 | 空状态（当前筛选） |
| `nav:notificationLoadError` | Could not load notifications | 通知加载失败 | 列表失败 |
| `nav:notificationRetry` | Retry | 重试 | 列表失败重试 |
| `nav:notificationMarkAllRead` | Mark all as read | 全部标为已读 | 顶部操作 |
| `nav:notificationMarkAllReadFailed` | Could not mark all as read | 全部标已读失败 | 操作失败 toast |
| `nav:notificationFilterAll` | All | 全部 | 筛选：全部（`status=all`） |
| `nav:notificationFilterActionable` | Actionable | 待处理 | 筛选：待处理（权限且源仍 alive） |
| `nav:notificationFilterMessages` | Messages | 消息 | 筛选：消息（`kind=chat_message`） |
| `nav:notificationFilterPermissions` | Permissions | 权限 | 筛选：权限（`kind=permission_request`） |
| `nav:notificationGone` | Expired | 已失效 | `displayStatus === "gone"` 灰态 |
| `nav:notificationKindChat` | Message | 消息 | 行 kind 标签/图标 aria |
| `nav:notificationKindPermission` | Permission | 权限 | 行 kind 标签/图标 aria |
| `nav:notificationChatTitle` | New message | 新消息 | chat 默认标题（无发送者名） |
| `nav:notificationChatTitleNamed` | `{{sender}}` | `{{sender}}` | chat 标题（有发送者） |
| `nav:notificationPermissionTitle` | Permission request | 权限请求 | 权限默认标题（无叙述者标题） |
| `nav:notificationPermissionTitleNamed` | `{{narrator}}` | `{{narrator}}` | 权限标题（有叙述者标题） |
| `nav:notificationPermissionPreview` | `{{tool}}: {{summary}}` | `{{tool}}：{{summary}}` | 权限 preview 模板 |

- **必须同时**改 `frontend/locales/en/nav.json` 与 `frontend/locales/zh-CN/nav.json`（已对称落地）。
- 校验：`bun server/scripts/check-i18n.ts`（或 `bun test tests/frontend/i18n-resources.test.ts --isolate`）。

---

## 7. 性能与安全（全任务包必读）

来自仓库铁律与调查结论，实现与评审时逐条核对：

1. 主线程 SQLite：小、快、有索引、LIMIT；禁止 FTS rebuild / 大聚合 / 无上限 `.all()` / 请求路径大事务。
2. 列表 API 不读 `raw_dump_json`、`output_json`、消息 `contentJson` 等大字段。
3. 未读计数有界；分页 cursor + `LIMIT n+1`。
4. WS 高频事件合并/节流；payload 极小。
5. 通知不得越过权限：撤销读权后历史行应 `gone` 或不再可执行；**可见 ≠ 可批**（P1 只跳转，仍要可读才出现在列表）。
6. 子进程/大输出与本功能无关，勿引入。
7. 源码文本守卫：若重命名被 `readFileSync` 测试引用的路径，必须全仓搜旧路径字符串。

---

## 8. 测试与验收（任务包 F + 各包自测）

### 8.1 命令（在 worktree 根目录）

```bash
bunx @biomejs/biome check .
bunx tsgo --noEmit
bun test --isolate <相关测试路径>
# 判断是否有回归时以 --isolate 为准；勿 kill 运行中的 NarraFork
```

### 8.2 最低测试面

| 层 | 用例 |
|----|------|
| service | insert 去重（同 userId+kind+sourceKey）；list 游标；markRead 只动本人；unread cap；preview 截断 |
| ACL | 非接收者 GET/POST → 404；list 不含他人数据 |
| fan-out | DM 非成员不收；mute 不收；发送者自言不收；权限仅可读者；重复 offer 不双插 |
| API | limit 边界；非法 kind；空 ids |
| UI | 徽章数字；筛选切换；gone 灰态；点击 navigate 参数（可 mock router） |

### 8.3 完成定义（Phase 1）

- [x] schema 迁移在 worktree 可生成/可执行（不手改 drizzle）— 0181 + 测试迁移 182 条通过；生产库本阶段不 apply
- [x] `/api/notifications*` 可用且鉴权正确 — 路由已挂 `app.ts` L387；service 测试覆盖他人资源 404；HTTP 层未在 F 启服务实测（代码级核对 + service 测试）
- [x] DM 与权限至少各一条真实 fan-out 路径接通 — DM：`chat-notify` → `fanoutChatMessageNotifications`（集成测试真实落库）；权限：`main.ts` `initNotificationFanout` → `narrator:permission_request`（fanout 测试 12 pass，permission 路径用 mock writer + ACL 源查询）
- [x] Header 铃铛 + Drawer + 筛选 + 标已读 + 跳转 — AppRootLayout 挂载 + UI 测试（筛选/gone/navigate）
- [x] i18n en + zh-CN 无缺失键 — nav 23 键对称；组件均 `useTranslation("nav")`
- [x] biome + tsgo + 相关 isolate 测试通过 — **通知中心范围内**通过；全仓 tsgo/biome 仍有环境/基线噪声（见 F 行）
- [x] 本文 §0 看板更新为 done，并附验证命令输出摘要
- [x] **不**把主工作区脏改动或 `spec://` 文件当作交付物 — 产物均在 worktree 分支文件中

---

## 9. 任务包拆分建议

### A Schema + Shared

- [ ] `server/db/schema.ts` 增加 `notifications` 及索引
- [ ] `bun run db:generate`（+ 本地 `db:migrate` 若环境允许）
- [ ] `shared/notification-center.ts` 按 §3.4
- [ ] 可选：export 到 `shared` 索引处（若有 barrel）

### B Service + API

- [ ] `notification-center-service.ts` + 单测
- [ ] validators + `routes/notifications.ts` 扩展（保留原 test-webhook 路由）
- [ ] `server/app.ts` 若未挂载则挂载（确认现有 `notificationRoutes` 挂载路径）

### C Fan-out

- [ ] DM：chat 服务/notify 钩子 + 测试
- [ ] 权限：与当前 worktree 权限/attention 代码对齐（注意基点已含未在主仓提交的相关逻辑时，**以 worktree 磁盘为准**）+ 测试

### D WS + Drawer

- [ ] event-bus / narrator-ws（或现有 broadcast 用户通道）发 `notification_center_changed`
- [ ] `useNotificationCenter` + Bell + Drawer
- [ ] 接入 `AppRootLayout` Header
- [ ] 组件测试

### E 设置 + i18n

- [ ] 默认策略常量（入中心）；P1 可不暴露复杂设置页，但预留 userPreferences 键名注释
- [ ] locales en/zh-CN

### F 验收

- [ ] 跑 §8.1；更新 §0；汇总阻塞

---

## 10. 参考源码（worktree 相对路径）

| 主题 | 路径 |
|------|------|
| 现有通知路由 | `server/routes/notifications.ts` |
| IM webhook 通知 | `server/services/notification-service.ts` |
| Human Attention 契约 | `shared/human-attention.ts` |
| Human Attention 服务 | `server/services/human-attention-service.ts` |
| 全局待办 UI | `frontend/components/narrator/question/GlobalQuestionInbox.tsx` |
| Dashboard 汇总 | `frontend/components/dashboard/NeedsAttention.tsx` |
| Chat ACL/未读 | `server/services/chat-service.ts` |
| Chat fan-out | `server/services/chat-notify.ts` |
| eventBus chat 契约 | `server/lib/event-bus.ts`（chat 段，禁止正文） |
| DM 路由 | `frontend/routes/messages.tsx` |
| Shell/Header | `frontend/components/AppRootLayout.tsx` |
| 前端 toast 壳 | `frontend/components/AppNotifications.tsx` |
| 瞬时通知 | `frontend/lib/notification.ts` |
| 用户偏好 | `server/db/schema.ts` `userPreferences` |
| ID | `server/lib/id.ts` |
| 错误 | `server/lib/errors.ts` |
| schema 生成规则 | `CLAUDE.md` 迁移与性能节 |

---

## 11. 变更记录

| 日期 | 变更 | 作者 |
|------|------|------|
| （创建日） | Phase 1 协作规格：锁定铃铛+Drawer、权限只跳转、username 交互/userId 存储、P1 仅 chat_message + permission_request | 主代理 |
| 2026-09-21 | 评审后修复 M1/m1/m2/m3；书面确认 M2 权限受众保守策略 | 主代理 |
| 2026-09-21 | F 验收：集成测试 4 pass；通知中心范围内 biome/tsgo/isolate 干净；记录环境限制与 preview clamp 行为；§0 F=done、§8.3 勾选 | F-subagent |

子代理若发现规格与磁盘冲突：**以可运行代码与仓库铁律为准**，并在 §0/§11 记录偏差，不得静默扩大范围。
