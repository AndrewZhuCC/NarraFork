# Bot 模式叙述者

> Bot 是一个**面向聊天房间而不是面向会话记录**的叙述者：人们在聊天界面里和它对话，只看到自己的发言和它用 `Send({ user })` 发来的消息；它的回复、思考和工具调用留在会话记录里，那是“监控”视图。
>
> 核心实现：`server/services/bot-chat.ts`（桥）、`server/services/chat-service.ts`（`bot` 房间）、`server/lib/agent/tools/send.ts`（`user` 目标）。

---

## 1. 两个正交的东西

| | 是什么 | 存在哪 |
|---|---|---|
| **Bot 身份** | 叙述者的永久 trait `bot`（`BOT_TRAIT`），创建时通过 `kind: "bot"` 打上，之后不切换 | `narrators.traits` |
| **对话** | 每个人与这个 bot 的一间 `bot` 房间，按人的 **id** 存（改名不分叉） | `chat_rooms`（`kind = "bot"`，`narrator_id` = bot，`dm_key = bot:<narratorId>:<userId>`）+ `chat_messages` |

监控（`/narrators/$id?view=monitor`）只是同一个叙述者的另一种视图，放在 URL 里，**不改变任何状态**：bot 不会因为被监控而变成普通叙述者。指向会话记录某条消息的深链接（`scrollTo` / `#msg-`）也直接进监控。

## 2. 两个方向

```
人 ── 发帖 ──▶ bot 房间 ── deliverToBot ──▶ bot 的下一条输入
人 ◀────────── bot 房间 ◀── sendAsBot ◀──── bot 调用 Send({ user })
```

- **入站**：`chat-service.postMessage` 只记录发言并发出 `chat:message_created`；`bot-chat.ts` 和 WS 扇出、通知中心一样是这个事件的监听者。投递走与 bot 自己输入框相同的 `acceptUserMessage`（忙时排队、空闲时开轮），作者是发言人，所以模型从普通的 `<sender kind="human" name="…">` 标记读出是谁在说话。回复会以 `> 引文` 前置，附件复制进 bot 的工作目录并附 `<attached_files>` 提示（与讨论房间转发同一路径）。同一个 bot 的投递按发帖顺序串行（`AsyncMutex`）。bot 自己的消息永远不会回投给它。
- **出站**：`Send({ user, message })` 只对 bot 提供（`AgentConfig.bot` 决定 schema；`sendAsBot` 重新读取 trait 作为权威检查）。按用户名解析（精确匹配优先，其次大小写不敏感的唯一匹配，忽略前导 `@`），写入 `postBotMessage`：`sender_narrator_id` 记作者，`sender_user_id` 为空。权限层对 bot 的 `Send({ user })` 自动放行（`shouldAutoAllowSendWithinScope`）——那是它唯一的声音。
- **提示词**：`runtimeBasePrompt` 在 bot 自身的 system prompt（人设）之后追加 bot 协议段（`server/lib/prompts/bot.ts`）：对方只看得到 Send 的内容、按发送者标记回复谁、每条消息都要回复、多人对话互相隔离。

## 3. 访问控制

| 操作 | 条件 |
|---|---|
| 打开房间（`GET /api/chat/rooms/bot/:narratorId`） | 能**读** bot（与 bot 页面同一门槛）；返回 `canPost` |
| 读房间 | 房间成员（与 DM 相同：bot 发给你的话归你读） |
| 发言 | 成员 **且** 能**写**（驱动）bot——发言就是 bot 的下一条输入 |
| bot 写入 | 只有 bot 叙述者；收件人是任意已存在用户（与 DM 一样） |

注意：共享给多人的 bot 只有**一个**上下文。能读 bot 的人可以打开监控看到所有人的对话——这是“共享 bot”本身的语义，提示词只约束 bot 不主动转述。

## 4. 复用的东西

bot 房间是普通聊天房间，所以分页、实时帧、未读角标（计入导航 `dmTotal`，见 `shared/chat-room-kinds.ts` 的 `isConversationRoomKind`）、通知中心、附件、引用回复、消息列表渲染（bot 作者显示叙述者头像 + Bot 徽标）全部沿用，`/messages` 也会列出 bot 对话。

## 5. 往群聊扩展

群聊（多 bot、多人）不需要改变上面的形状：

- 新房间种类（如 `group`），成员里加入 bot；`isConversationRoomKind` 加上它。
- 入站规则不变：**发帖投递给房间里除作者外的每个 bot**（目前 bot 房间只有一个 bot：`narrator_id`）。
- 出站规则不变：`Send` 增加 `room` 目标；作者仍记在 `sender_narrator_id`，引用作者在 `reply_to_sender_narrator_id`。
- bot 之间互相投递时需要防循环（例如只投递 @ 到自己的消息），这是群聊阶段要定的规则。

## 6. 文件地图

| 文件 | 职责 |
|---|---|
| `server/db/schema.ts` | `chat_rooms.kind` 加 `bot`；叙述者讨论房间唯一索引改为部分索引；`chat_messages.sender_narrator_id` / `reply_to_sender_narrator_id` |
| `server/services/chat-service.ts` | `resolveBotRoom`、`postBotMessage`、bot 作者快照、对话列表与角标 |
| `server/services/bot-chat.ts` | 入站投递监听、`sendAsBot` |
| `server/lib/agent/tools/send.ts` | `user` 目标 |
| `server/lib/prompts/bot.ts` | bot 协议提示词 |
| `server/services/narrator-service.ts` | `kind: "bot"`（必须独立、必须有名字） |
| `frontend/components/chat/BotChatPage.tsx` | bot 页面：头部（状态、监控）+ `ChatRoomView` |
| `frontend/routes/narrators/$narratorId.tsx` | bot 默认显示对话，`?view=monitor` 显示会话记录 |
