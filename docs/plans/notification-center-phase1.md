# 通知中心规格：统一入口，分离待办与动态

修订日期：2026-09-21。工作分支：`feat/notification-center`。

本文替代原 Phase 1 的「全部／待处理／消息／权限」规格及已完成看板。原实现的去重、权限范围、通知持久化和用户定向 WS 保留；旧验收结果不代表本次重做已完成。

## 1. 产品原则与范围

通知中心的职责是减少用户需要关注的事情，而不是把每个事件变成新的清理任务。

- 一个铃铛入口，两个主视图：**待处理**与**动态**。
- 待处理直接使用 Human Attention 的权威查询：权限、阻塞问题、异步问题、计划审批、反思。不得从通知历史或当前页推算。
- 动态是持久化事件历史：本次仍只投影 DM 和权限 offer，不扩展到群聊、讨论房、提及或运行错误。
- 已读不等于已处理；标动态已读不能批准请求、回答问题或移除待办。
- 用户在源聊天已读后，对应通知也消退；通知点击仅确认当前已展示的事件，不暗中推进源聊天水位。
- 不新建审批 API 或第二套审批表单。复用现有 Human Attention 内容组件和权限校验；必要时导航真实 owner 会话。
- 保留聊天专业入口和现有 Human Attention 入口；它们共享原查询／状态，不再增加待办状态副本。
- 不承诺通知投影可靠必达。事件投影失败有日志，但不回滚业务；待处理不依赖投影成功，聊天有效未读以源水位校准。本次不建消息队列或 outbox。

## 2. 交互设计

### 2.1 铃铛

- 常驻订阅通知摘要与 Human Attention；不因铃铛挂载读取完整审批详情。
- 有待办时，强调待办数量并优先打开「待处理」；数字来自已加载的权威条目，存在 nextCursor 时显示真实下界（如 `50+`，不是固定 `99+`）。只读条目由内容区明确标识，不暗示可审批。
- 无待办时，显示未读 DM **会话数**，普通动态使用非警报式颜色；无新消息时不显示数字。
- tooltip / aria-label 分别说明待处理与未读会话；不得把权限历史未读与当前待办相加重复计数。
- 首次加载失败是未知，不得伪装成“没有事情”。打开后有可见错误与重试。

### 2.2 待处理

- 从现有 `GlobalQuestionInbox.tsx` 提取无 Drawer 外壳的内容组件；原专业收件箱和通知抽屉复用同一实现。
- 阻塞项优先；保留 canAct、实际 owner 导航、详情按需读取、执行设备上下文、超限禁止审批、草稿恢复与快捷键隔离。
- 不嵌套 Drawer，不复制表单和决策逻辑。大内容组件按需 lazy 加载，避免铃铛把审批渲染器拉入首屏。
- 空候选页但有 nextCursor 时仍有继续加载入口；只有 items 为空且无 nextCursor 才显示真正空态。
- 导航到会话时关闭抽屉，复用现有移动端返回栈逻辑。
- 待处理视图没有“全部已读”，也不因阅读而隐藏待办。

### 2.3 动态

- 次级筛选仅按来源：全部／消息／权限；不再混合状态与类型。
- 同一个 DM 房间聚合为一行：发送者、最新有效预览、时间、聚合消息数；同一房间不得跨静态分页重复。
- 权限历史仍按请求显示，已完成标识为已处理，不占铃铛待办计数。
- 行信息层级：谁／哪个会话 → 什么内容 → 所属项目、章节及时间。避免图标加重复类型徽章。
- 未读用轻量标记和字重，普通消息不用警报色；已处理与不可访问是不同状态。
- 点击有效行：确认当前组 notificationIds 为已读并导航源；导航不因标读失败而阻塞。
- 不可访问行显示通用占位，无正文和链接；仍允许通过本行标读或顶部批量操作消除历史未读。
- “将动态标为已读”只作用于动态视图的当前来源筛选与服务端 asOf 边界，不作用于待办。失败必须可见，不吞掉错误。
- 分页入口独立于非空分支，避免有 cursor 的空页死路。
- 移动端抽屉全屏，筛选／操作可换行；桌面控制可读宽度。
- 本次保留可靠的 room/owner 级导航；不把 toolCallId 假冒 messageId。消息／权限精确定位属于后续独立接入，不在本次制造无消费者的参数。

## 3. 数据契约

以 `shared/notification-center.ts` 为唯一公共契约。

- `NotificationSourceState = active | resolved | gone`，只表达源状态。
- 前端用 `readAt === null` 表达未读；移除旧 `displayStatus` 和 `sourceAlive` 的重叠语义。
- 数据库暂时保留原 status/readAt 两列以兼容既有记录；写入与标读保持一致，不为改名字重建数据。
- `NotificationLink` 是严格判别联合：chat_room 必有 roomId；narrator 必有 narratorId；unavailable 不含目标标识。
- 动态条目包含 `groupKey`、有界 `notificationIds`、`groupSize`、`sourceState`、`readAt`、可空项目／章节标题。
- `NotificationUnreadCounts` 分开提供 `unreadConversations` 和 `unreadActivities`，二者都在明确保留窗口内有界计算；分别用 `conversationsLowerBound` 与 `activitiesLowerBound` 标记各自触顶，不能让权限历史数量影响 DM 角标。
- 权限历史的已读不是待办计数的数据来源。
- WS 事件名移入 shared；service 直接发既有 eventBus，不维护第二套私有订阅集合。

## 4. 聚合与分页

### 4.1 明确窗口

动态只展示 **最近 30 天、最近 500 个事件** 的投影，不是无限审计日志。这是事件预算，不是会话数量。即使后台清理尚未追上，所有查询也先按用户和时间索引限制到该窗口。

1. 按 `(createdAt DESC, id DESC)` 有界取窗口。
2. 使用白名单字段批量读取 source、membership、ACL 上下文。
3. 校准有效源状态、已读与安全摘要后，在整个窗口聚合会话。
4. 按来源／未读条件过滤，最后对聚合结果游标分页。禁止先对事件分页，再页内去重。
5. 游标校验形状、大小和筛选条件；下一 cursor 依据已扫描位置推进。
6. 这是实时 feed，不承诺数据库级快照；并发新事件可能改变排序。WS 变化后重新协调已加载页，前端按 groupKey 去重。静态数据遍历必须不重不漏。
7. 不引入有状态快照缓存／额外聚合表；若未来需要严格快照审计，另立契约，不能把实时 feed 说成快照。

### 4.2 源与撤权

- DM 有效未读：通知未标读、消息未删除、成员仍可读、`message.seq > lastReadSeq`。后台标读同步的延迟不得让源已读消息重新亮起。
- 读取源码只取 `id/roomId/seq/deletedAt` 等必要列；不得读取工具 inputJson/outputJson、narrator systemPrompt/backgroundResult、计划或完整消息正文来渲染摘要。
- 权限历史源仍 pending 且无决定时 active，否则 resolved；真正的待办只信 Human Attention，不信历史 pending。
- 无权限／源消失：服务端清空 title、preview、link 目标、sourceKey、项目／章节／叙述者标识及标题。客户端本地化通用占位；不能只禁用点击。
- 消息删除不得从旧预览恢复内容；聚合时优先仍有效消息，无有效消息则占位。
- ACL检查复用既有可读规则，不能把可读当可操作。

## 5. 标读、安全与边界

`POST /api/notifications/read` 必须显式意图：

- `{ scope: "items", ids: [...] }`：有界的通知 ID 集合，空数组是 no-op。
- `{ scope: "all", before: <服务端 asOf>, kind?: ... }`：确认当前保留窗口内指定来源的动态，不清除晚于边界的新事件。
- 空 body、`{}`、非法 JSON、未知 scope 和混合字段均拒绝，不能降级成全量操作。
- 单次最多处理 500 个 ID，SQL 按至多 200 个 ID 分批，使用 changes 计数；不使用无界 UPDATE RETURNING .all()。
- 不在请求里追赶窗口以外积压；那些记录由维护任务删除。
- 标读单调、只改本人、保留第一次 readAt。
- HTTP request body 有字节上限；游标和 ID 有长度上限。
- 删除只动本人；他人／不存在都是 404。

## 6. 源事件与维护

### 6.1 事件接线

- 通知投影独立订阅提交后的 chat:message_created，不依赖聊天 WS 广播先成功。
- chat:room_read 同步该用户被源水位覆盖的通知；发送消息推进作者水位也应发同一提交后事件。
- 不为 source 同步扫描无限历史：从固定通知窗口取候选，必要 source lookup 批量、有界。
- 插入与源阅读存在竞态时，列表／摘要仍由源水位派生正确未读。
- 权限 offer 仍走 narrator:permission_request，有限受众、唯一键去重、失败记录日志。
- 源完成、删除、撤权由既有 human_attention_changed、访问／删除 WS 事件触发通知查询失效；首次连接与重连都协调，避免 HTTP→WS 窗口漏事件。
- 通知变化 direct eventBus → 每用户 50ms 合并广播；仅传 ID/种类标记，无正文。

### 6.2 后台保留策略

本次必须落地，不再用 TODO 常量充当实现。

- 可启动、可停止、防重入，与 main 生命周期接线；不得重启承载当前会话的服务。
- 用户 keyset 公平轮询，每 tick 固定用户数、最多固定删除数和时间预算。
- 每用户保留最新 500 个事件，删除超过 30 天或超出数量窗口的候选；每条 SQL 都有限额并走索引。
- 每批后让出事件循环；记录慢操作与失败；不做全库聚合、FTS、VACUUM 或大事务。
- 修改 schema 仅为必要索引：`(user_id,created_at,id)` 与 `(user_id,status,created_at,id)`；不新增 room/seq 真相副本。由主代理统一自动生成迁移，不手改 drizzle，也不迁移生产数据库。

## 7. 工程边界与交付分工

分工只是实现组织方式，不进入生产代码注释或额外架构层。

| 工作包 | 范围 | 状态 |
|---|---|---|
| 公共契约与规格 | 本文、shared、schema 索引与生成迁移 | 已完成 |
| 通知数据与维护 | service、validator、route、retention、相关服务测试 | 已完成 |
| 源事件与广播 | fanout、chat/read hooks、direct WS bridge、相关测试 | 已完成 |
| 注意力界面 | 双视图、Human Attention 内容复用、hooks、API client、i18n、UI 测试 | 已完成 |
| 集成验收 | 跨路径测试、静态检查、最终差异审查 | 已完成；全仓基线限制见下 |

不新增泛化插件框架、任务队列、第三方依赖或设置矩阵。保留 IM、PWA、聊天和审批原语义。

## 8. 验收用例

### 数据与边界

- [x] 同来源去重仍有效；同房间多事件一组，跨静态分页不重不漏。
- [x] 混合类型筛选、有效未读和聚合先于分页；所有请求候选数有硬上限。
- [x] 聊天先读后投影／部分水位阅读／发送者水位推进，不产生幽灵未读。
- [x] 撤权、源删除、消息软删除，不返回残留摘要、链接或上下文标识。
- [x] 真实 HTTP handler 拒绝非法 JSON、空 body 和空对象；不产生任何标读副作用。
- [x] explicit items/all 只改本人、单调、有界，不吞晚到事件。
- [x] 后台清理数量／年龄／公平游标／停止与重入行为有测试。
- [x] source reader 使用字段白名单，性能测试／查询断言覆盖大字段禁读。

### 界面与接线

- [x] 待处理直接显示 Human Attention，含问题与权限，标动态已读不会隐藏待办。
- [x] 首页全是已处理历史不影响真正待办；空页有 cursor 仍可继续加载。
- [x] 铃铛区分待办和未读会话，不重复计权限历史；下界文案正确。
- [x] 聚合消息点击只标当前 notificationIds，导航后关闭抽屉。
- [x] 全部标读失败可见；筛选范围明确；消失来源可消除历史未读。
- [x] 源完成、撤权、删除、首次连接、重连能够刷新；只读表单和详情上限不回归。
- [x] en/zh-CN 键对称，移动端布局、可访问名称和键盘隔离保留。
- [x] 真实 source→writer→eventBus 接线测试，不以手动 emit/on 代替桥接测试。

### 验证命令与结果

所有相关测试使用 `bun test --isolate`；运行修改文件的 Biome 检查与 `bunx tsgo --noEmit`。若全仓静态检查有基线错误，列明具体来源，不能把过滤后的输出称为全仓通过。

### 2026-09-21 最终验收

- **联合隔离回归：270 pass / 0 fail，17 个测试文件、1085 次断言。** 覆盖通知 UI/hooks、HumanAttentionInbox、通知 service/fanout/真实 source events/maintenance/HTTP、chat service/notify、narrator ACL/delegation、recent-tabs 与旧 IM 通知消费路径。
- **改动文件 Biome：37 个 TS/TSX/JSON 文件通过，无修改建议。** `git diff --check` 通过。
- **`bun run build`：通过**（8.12s）；通知抽屉独立 chunk 8.61 kB（gzip 3.54 kB）。构建仍提示其他大 chunk，未把全仓 bundle 优化混入本次。
- **自动迁移：** `bun run db:generate` 生成 `drizzle/0182_light_nightmare.sql`，只重建两个通知分页索引增加 id 次键；隔离测试从空库应用 183 条迁移通过。`drizzle/` 按仓库既有规则 gitignored，未强制纳入版本控制，未迁移生产库。
- **全仓 TypeScript 未通过：4 项既有问题。** `server/lib/licenses/manifest.ts` 缺生成的 `embedded-licenses`；`server/routes/narrators.ts` 的 `updateWhitelistCmdSchema` / `updateWhitelistDirSchema` 导出缺失；`vscode-extension/src/token-store.ts` 缺 `vscode` 类型。本次改动文件没有类型错误。
- **全仓 i18n 扫描未通过：** `GitChangesTab.tsx:954`、`NarratorPublicShareManager.tsx:216/221`、`PublicSharedNarratorPage.tsx:563` 的既有 locale-sensitive formatting 守卫；通知 en/zh-CN 键对称测试通过。
- **复核修正：** 整个 Drawer 的 Enter/左右键不向底层审批快捷键冒泡；首次访问待处理后保持面板挂载，切换到动态期间提交失败仍保留锁、草稿和错误。相关 deferred Promise 与键盘传播测试通过。
- **安全补充：** 标读请求 40 KiB 流式字节上限、5 秒总读取预算及 AbortSignal 取消；受众与 recent-tabs 候选读取在 SQL 层截断，同时保留其他广播消费者的默认全体语义；均有回归覆盖。
- 未启动／停止／重启应用进程，未操作生产数据，未提交或推送 Git。移动端全屏断点与布局已实现，本轮未进行真实浏览器截图或真机视觉验收。

主要验收命令：

```bash
bun test --isolate frontend/components/notifications frontend/components/narrator/HumanAttentionInbox.test.tsx server/services/__tests__/notification-center-service.test.ts server/services/__tests__/notification-center-integration.test.ts server/services/__tests__/notification-center-maintenance.test.ts server/services/__tests__/notification-fanout.test.ts server/services/__tests__/notification-source-events.test.ts server/routes/__tests__/notifications.test.ts server/lib/__tests__/notification-center-ws-bridge.test.ts server/services/__tests__/chat-service.test.ts server/services/__tests__/chat-notify.test.ts server/services/__tests__/narrator-acl.test.ts server/services/__tests__/narrator-acl-delegation.test.ts tests/server/services/recent-tabs-service.test.ts server/services/__tests__/notification-recent-tabs.test.ts
bun run build
bunx tsgo --noEmit
bun server/scripts/check-i18n.ts
```
