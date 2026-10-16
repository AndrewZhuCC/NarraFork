# 知识库前端重构设计

状态：**规划中**（P0 缺陷修复单独提交，不在本文范围内实施）。
关联文档：数据模型、权限和审核状态机以 `docs/KNOWLEDGE_BASE.md` 为准，本文只改前端信息架构与少量为其服务的后端接口，**不改审核状态机和 ACL 语义**。

---

## 1. 为什么要重构

现有前端按后端实体与状态机拆页面，而不是按用户任务组织。结果是同一件事分散在多处、同一概念有多套界面：

| 问题 | 现状 |
|------|------|
| "我的版本"分三处 | 关联型个人条目在条目页 `#draft` tab 编辑，独立型在 `/knowledge/personal/$id`；「我的库」tab 按类型和状态跳到三个不同目的地 |
| 五套编辑器 | 新建条目弹窗、条目页直接编辑、草稿 tab、独立条目页、「我的库」常驻新建表单，全是裸 `Textarea`，无预览、无未保存提醒；标签输入一处是逗号文本、其余是 `TagsInput` |
| 条目页过载 | 5 个 tab 对只读用户也全部显示；约 280px 的 sticky 侧栏塞入可编辑元数据表单、链接摘要、ACL 面板；元数据标题重复两次 |
| 审核三种形态 | 审核中心列表 → 关联型跳条目页 `#submissions/<id>`，独立型弹窗审；条目页提交 tab 混合作者与审核者视角 |
| 权限不可见 | 前端自己猜权限：`canDirectWrite = isAdmin \|\| isOwner`，但服务端 `addRevision` 还允许 write grant 持有者与集合 owner 直接写——这些人看不到编辑按钮；用户无从知道"为什么能看/能改/能审" |
| 缺口 | 知识图谱、ACL 审计、知识包、标签/密级筛选均无界面（见 KNOWLEDGE_BASE.md §10） |
| 代码 | `routes/knowledge/index.tsx` 与 `$entryId.tsx` 各约 1300 行、内联 10+ 组件；状态徽章配色复制 3 份 |

## 2. 已确认的决策

1. **允许修改 URL**。旧 URL 全部保留重定向（见 §4.2），铃铛、审核导航、叙述者知识气泡与 dock 面板同步迁移。
2. **保留管理员直接修改 main 的能力。**
3. 本次重构**不改服务端权限规则**。owner / write grant / 集合 owner 的直接写权限维持服务端现状，前端改为按服务端返回的能力位展示（§6.1），不再自行推断。是否收紧非管理员的直接写，属于独立的策略变更，见 §10。

## 3. 设计原则

- **按任务分区**：浏览（读）/ 我的（写）/ 审核（审）/ 管理（配），每个任务一个入口。
- **一个概念一个页面**：一个个人版本只有一个编辑页；一个发布请求只有一个详情页，作者与审核者看到同一页、按角色出现不同操作。
- **阅读优先**：条目页的主体是正文；编辑、历史、请求是次级入口，且仅在有内容/有权限时出现。
- **能力由服务端给出**：按钮显隐只读 `viewerCaps`，前端不复刻 ACL 判定。
- **可链接**：所有视图状态进 path 或 search params，不再用 hash 承载 tab。

## 4. 信息架构与路由

### 4.1 新路由表

| 路由 | 页面 | 说明 |
|------|------|------|
| `/knowledge` | 浏览 | search：`collection`、`q`、`tag`、`level`、`sort`；左侧集合树，右侧分页条目列表 |
| `/knowledge/$entryId` | 条目阅读页 | 正文 + 元信息条 + 链接区；保持现有路径，聊天里已分享的链接不失效 |
| `/knowledge/$entryId/history` | 版本历史 | 版本列表 + 任意两版 diff |
| `/knowledge/$entryId/edit` | 直接编辑 main | 仅 `viewerCaps.canWriteMain` 时可进，否则重定向回阅读页 |
| `/knowledge/$entryId/settings` | 条目设置 | 元数据、状态、ACL、review tags、转移 owner、删除；按能力位分区显示 |
| `/knowledge/mine` | 我的 | 我的个人版本（关联型 + 独立型同一列表）+ 我发起的请求及进度 |
| `/knowledge/mine/new` | 新建个人条目 | 统一编辑器；可选目标集合 |
| `/knowledge/mine/$personalEntryId` | 个人版本编辑页 | 关联型与独立型同一页面：编辑、diff、drift/rebase、发布、该版本的请求历史 |
| `/knowledge/review` | 审核收件箱 | search：`status`；分组展示可审请求 |
| `/knowledge/requests/$submissionId` | 发布请求详情 | 作者与审核者共用；按角色显示 verdict / 解冲突 / 撤回 / 重提 |
| `/knowledge/admin` | 管理（admin） | 子路由：`levels`、`tags`、`users`、`audit`、`packs` |

### 4.2 旧 URL 重定向

| 旧 | 新 |
|----|----|
| `/knowledge?tab=mylibrary` | `/knowledge/mine` |
| `/knowledge?tab=review` | `/knowledge/review` |
| `/knowledge?tab=review&submission=<id>` | `/knowledge/requests/<id>` |
| `/knowledge?tab=admin` | `/knowledge/admin` |
| `/knowledge/$entryId#content` | `/knowledge/$entryId` |
| `/knowledge/$entryId#history` | `/knowledge/$entryId/history` |
| `/knowledge/$entryId#links` | `/knowledge/$entryId`（滚动到链接区） |
| `/knowledge/$entryId#submissions` | `/knowledge/review?entry=$entryId`（无可见请求时回阅读页） |
| `/knowledge/$entryId#submissions/<id>` | `/knowledge/requests/<id>` |
| `/knowledge/$entryId#draft` | 有个人版本 → `/knowledge/mine/<draftId>`；没有 → 阅读页并打开"创建个人版本"确认 |
| `/knowledge/personal/$id` | `/knowledge/mine/$id` |

实现：在 `/knowledge/` 与 `/knowledge/$entryId` 的 `beforeLoad` 里解析旧 search/hash 并 `redirect({ replace: true })`；`/knowledge/personal/$id` 保留为只做重定向的薄路由。重定向至少保留两个发布周期，并有单测覆盖每一行。

### 4.3 需要同步修改的入口

- `frontend/lib/knowledge-review-nav.ts`：`knowledgeReviewDestination` 简化为统一返回 `/knowledge/requests/<id>`（作者无 submission 时回 `/knowledge/mine/<draftId>`）；`parseKnowledgeEntryHash` 等仅保留给重定向使用；`knowledge-review-nav.test.ts` 同步改写。
- `frontend/components/notifications/KnowledgeReviewNotices.tsx`：铃铛点击目标。
- `frontend/components/narrator/knowledge/KnowledgeEntryPanelContent.tsx`：dock 面板"在知识库中打开"。
- `shared/pretext-layout/tool-detail.ts` 的 `knowledgeEntryHref`：路径不变，无需改动，但个人条目链接需对照新路由核对。
- `frontend/components/nav/nav-items.tsx`：入口不变，徽章仍为审核收件箱计数。

## 5. 页面设计

### 5.1 浏览 `/knowledge`

- 顶部：标题 + 全局搜索框 + 「新建」菜单（新建条目到集合 / 新建个人条目）。
- 左栏集合树：集合名 + 可读条目计数（P0 结论为准，计数不可泄露不可读条目的存在）；集合的编辑、ACL、转移、删除收进每行的"⋯"菜单。
- 右侧列表：表格式行（标题、摘要、标签、密级、更新时间、owner），筛选条（标签、密级、状态），排序（更新时间 / 标题 / 相关度），分页"加载更多"（复用 P0 的 cursor 接口）。
- 有个人版本的条目在行上标"我有个人版本"/"落后 main"，点击仍进阅读页。
- 空状态区分"没有集合""集合为空""无搜索结果""你没有权限看到更多"。

### 5.2 条目阅读页 `/knowledge/$entryId`

- 头部：面包屑（知识库 › 集合）、标题、密级/状态/标签、owner、最后更新时间与版本号。
- 主操作（按 `viewerCaps`，只出现一个主按钮）：
  - `canWriteMain` → 「编辑」（直接改 main）+ 次级「创建个人版本」；
  - 否则 → 「提议修改」：已有个人版本则进入它，没有则创建后进入 `/knowledge/mine/<id>`。
- 次级入口（"⋯"或右上图标）：历史、设置（仅有对应能力位时）、复制链接。
- 正文：`MarkdownContent` 全宽阅读；下方"关联条目"区（出链 / 入链，按类型分组，可在有写权限时添加）。
- 提示条：存在可审的打开请求时显示"有 N 个待审请求"→ 收件箱；我有打开的请求时显示其状态 → 请求详情。
- 删除常驻侧栏；元数据编辑全部移到设置页。

### 5.3 统一编辑器（共享组件 `KnowledgeEditor`）

所有写入正文的地方只用这一个组件：

- 编辑 / 预览 / 与基线 diff 三视图切换（窄屏为 tab，宽屏编辑+预览并排）。
- 标题、目标集合、标签、关键词等字段由调用方以 slot 传入，使用统一的 `TagsInput`。
- 脏状态跟踪 + 路由离开拦截（TanStack Router `useBlocker`）+ `beforeunload`。
- `Ctrl/Cmd+S` 保存。
- 可选 drift 横幅（落后 N 版、合并 main、采用 main 的确认）与冲突 diff 展示，直接复用现有 rebase 逻辑。

使用位置：`/knowledge/$entryId/edit`（基线 = 当前 main）、`/knowledge/mine/new`、`/knowledge/mine/$id`（基线 = fork 基线或空）。叙述者 dock 面板只读展示，不嵌入编辑器。

### 5.4 我的 `/knowledge/mine`

- 列表合并关联型与独立型：标题、类型（关联到「条目名」/ 独立·目标集合）、状态徽章（落后 main、打开请求状态）、更新时间。
- "我发起的请求"分区：打开中的请求在上，近期结论（approved / rejected / changes_requested，含 findings）在下，点击进请求详情。
- 新建改为按钮 → `/knowledge/mine/new`，去掉常驻表单。

### 5.5 个人版本编辑页 `/knowledge/mine/$personalEntryId`

- 顶部状态条：类型、关联条目链接或目标集合、drift 状态、当前打开的请求。
- `KnowledgeEditor`（关联型 diff 基线 = 当前 main；独立型无 diff）。
- 发布区：变更说明 + 「提交审核」；不可提交时显示具体原因（缺标题、缺目标集合、已有打开请求、已归档）。
- 本版本的请求历史（时间线），点击进请求详情。
- 删除（确认弹窗，说明会撤回哪些打开请求）。

### 5.6 审核收件箱 `/knowledge/review`

- 筛选：待审 / 冲突 / 全部；可按集合筛。
- 每行一个请求：标题（关联型用条目标题，独立型用提案标题）、类型、提交者、轮次、提交时间、是否落后 main。
- 顶部说明我的审核范围（复用 `ReviewScopeNotice`）。
- 点击统一进 `/knowledge/requests/$submissionId`，不再分"跳条目页"和"弹窗"两条路。

### 5.7 请求详情 `/knowledge/requests/$submissionId`

- 头部：状态、轮次与上一轮链接、提交者、目标（条目 / 集合）、变更说明、drift 提示。
- 主体：关联型显示提案 vs 当前 main 的 diff（可切换 vs 基线）；独立型显示完整预览。
- 右侧或底部操作按角色出现：
  - 审核者：approve / request changes / reject / comment，findings 输入；冲突时的合并编辑器（复用 `KnowledgeEditor` 的 diff/编辑能力）。
  - 作者：撤回、去修改（进入个人版本页）、重提。
  - 其他人：只读。
- 评审意见与 findings 时间线。

### 5.8 设置与管理

- `/knowledge/$entryId/settings`：分区卡片：基本信息（标题/标签/关键词/状态）、访问控制（密级、受控标签、review tags，含空 review tags 的 fail-closed 说明）、所有权（转移）、危险操作（删除）。每个分区按能力位显示或只读。
- `/knowledge/admin`：左侧子导航（密级、标签与标签类型、用户授权、ACL 审计、知识包）。现有 `AclAdminPanel` 的三个 tab 拆为三个子路由；审计和知识包为新页面（P3）。

## 6. 需要的后端改动

均为**新增**字段或接口，不改现有语义。

### 6.1 `viewerCaps`

`GET /entries/:id` 增加：

```ts
viewerCaps: {
  canWriteMain: boolean;      // admin / owner / write grant / 集合 owner（与 addRevision 同一判定）
  canEditMeta: boolean;
  canManageAcl: boolean;
  canTransferOwner: boolean;
  canDelete: boolean;
  canReview: boolean;         // canReview(caps, entry)
  myPersonalEntryId: string | null;
}
```

判定函数直接复用 `knowledge-acl.ts`，并补测试保证与对应写接口的判定一致（同一 fixture 下"caps 为 true ⇔ 接口不拒绝"）。集合列表同理增加 `viewerCaps`（编辑、ACL、转移、删除、在其中建条目）。

### 6.2 其他

- 条目列表：分页与计数由 P0 提供。P2 需在其上加 `level`、`sort` 参数，`tag` 已支持。
- `GET /submissions/:id`：补充展示所需的元信息（目标条目标题或集合名、提交者用户名、是否 drift），省去前端拼装多次请求；仍需 ACL 校验，不可读时返回 404。
- "我的"页：评估是否需要一个聚合接口（个人条目 + 打开请求 + 近期结论），避免三次请求；如不需要可沿用现有三接口。

## 7. 前端代码组织

```
frontend/routes/knowledge/
  index.tsx                     浏览
  $entryId/index.tsx            阅读
  $entryId/history.tsx
  $entryId/edit.tsx
  $entryId/settings.tsx
  mine/index.tsx  mine/new.tsx  mine/$personalEntryId.tsx
  review/index.tsx
  requests/$submissionId.tsx
  admin/{index,levels,tags,users,audit,packs}.tsx
  personal/$personalEntryId.tsx 仅重定向
frontend/components/knowledge/
  editor/KnowledgeEditor.tsx    统一编辑器
  SubmissionStatusBadge.tsx     唯一的状态配色来源
  EntryRow.tsx  CollectionTree.tsx  DriftBanner.tsx  EntryLinksSection.tsx …
frontend/hooks/knowledge/       按领域拆分 useKnowledge.ts（entries / personal / submissions / acl）
```

路由文件只做数据装配与布局，单文件控制在约 300 行以内。

## 8. 分期与 PR 切分

| 期 | 内容 | 验收 |
|----|------|------|
| **P0**（进行中） | 条目列表 cursor 分页、集合计数修正、卡片悬停样式 | 超过一页的条目可达；切换集合或搜索不改变其他集合计数 |
| **P1** 基础件 | `viewerCaps` 接口；`KnowledgeEditor`、`SubmissionStatusBadge`、`DriftBanner` 抽取；现有页面换用这些组件，路由暂不动 | 前端不再出现 `isAdmin \|\| isOwner` 式权限推断；write grant 持有者能看到编辑入口；所有编辑处有预览与未保存提醒 |
| **P2a** 请求与审核 | `/knowledge/review`、`/knowledge/requests/$id`；铃铛与审核导航迁移；对应旧 URL 重定向 | 关联型与独立型请求同一页面；作者与审核者同一 URL |
| **P2b** 个人版本 | `/knowledge/mine*`；关联与独立统一编辑页；`#draft` 与 `/personal/$id` 重定向 | "我的版本"只有一个入口 |
| **P2c** 阅读与设置 | 条目阅读页、`/history`、`/edit`、`/settings`；浏览页筛选排序；删除侧栏 | 只读用户只看到正文、历史、链接 |
| **P2d** 管理 | `/knowledge/admin/*` 子路由拆分 | 现有管理功能无回退 |
| **P3** 缺口 | ACL 审计页、知识包管理、知识图谱视图、"为什么我能/不能访问"说明 | 对应 KNOWLEDGE_BASE.md §10 条目可关闭 |

P2 的子项相互独立，可按 a → b → c → d 顺序并行开发、逐个合入；每个 PR 带上它负责的那部分旧 URL 重定向。

## 9. 测试策略

- 每条旧 URL 重定向一条单测（纯函数解析 + 路由级测试）。
- `viewerCaps` 与写接口判定一致性测试（服务端）。
- `KnowledgeEditor`：脏状态、离开拦截、保存快捷键、diff 基线（组件测试；参考 composer 的 browser fixture 模式）。
- 请求详情页：同一 submission 在作者 / 审核者 / admin / 无关用户下的操作集合。
- 回归以 `bun test --isolate` 为准（见 docs/TESTING.md）；移动被 `readFileSync` 守卫测试引用的文件后全仓搜索旧路径。
- i18n：新增文案同时进 `en` 与 `zh-CN`，删除不再使用的键。

## 10. 待确认

1. **非管理员的直接写**：本文默认维持服务端现状（owner、write grant、集合 owner 可直接改 main），前端按 `viewerCaps` 展示。若希望只有管理员能直接改 main，需要改服务端 `addRevision` 判定，并处理"owner 不能自审"带来的后果（owner 的修改需要他人审核），建议单独立项。
2. **知识包、审计、图谱（P3）**的优先级。
3. **archived 个人条目在"我的"页的展示**：默认隐藏，提供"显示已归档"开关；清理策略仍按 KNOWLEDGE_BASE.md §10.2 待定。
