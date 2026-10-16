# 知识库前端重构设计

状态：**实施中**——P0 已合入（NarraFork/NarraFork#81），P1 已完成（见 §8.1），**P2 全部完成**：P2a（见 §8.2）、P2b（见 §8.3）、P2c（见 §8.4）、P2d（见 §8.5）。剩余 P3。
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
| `/knowledge/admin` | 管理（admin） | 子路由：`levels`、`tags`、`users`（P2d 已实现，见 §8.5）；`audit`、`packs` 属 P3 |

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
- `/knowledge/admin`：左侧子导航（密级、标签与标签类型、用户授权、ACL 审计、知识包）。现有 `AclAdminPanel` 的三个 tab 拆为三个子路由；审计和知识包为新页面（P3）。——P2d 已完成前三项，见 §8.5；审计与知识包在实现前不出现在子导航中。

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
| **P0**（✅ 已合入 #81） | 条目列表 cursor 分页、去掉错误的集合计数、卡片悬停样式 | 超过一页的条目可达；侧栏不再显示错误计数 |
| **P1** 基础件（✅ 已完成，见 §8.1） | `viewerCaps` 接口；`KnowledgeEditor`、`SubmissionStatusBadge`、`DriftBanner` 抽取；现有页面换用这些组件，路由暂不动 | 前端不再出现 `isAdmin \|\| isOwner` 式权限推断；write grant 持有者能看到编辑入口；所有编辑处有预览与未保存提醒 |
| **P2a** 请求与审核（✅ 已完成，见 §8.2） | `/knowledge/review`、`/knowledge/requests/$id`；铃铛与审核导航迁移；对应旧 URL 重定向 | 关联型与独立型请求同一页面；作者与审核者同一 URL |
| **P2b** 个人版本（✅ 已完成，见 §8.3） | `/knowledge/mine*`；关联与独立统一编辑页；`#draft` 与 `/personal/$id` 重定向 | "我的版本"只有一个入口 |
| **P2c** 阅读与设置（✅ 已完成，见 §8.4） | 条目阅读页、`/history`、`/edit`、`/settings`；浏览页筛选排序；删除侧栏 | 只读用户只看到正文、历史、链接 |
| **P2d** 管理（✅ 已完成，见 §8.5） | `/knowledge/admin/*` 子路由拆分 | 现有管理功能无回退 |
| **P3** 缺口 | ACL 审计页、知识包管理、知识图谱视图、"为什么我能/不能访问"说明 | 对应 KNOWLEDGE_BASE.md §10 条目可关闭 |

P2 的子项相互独立，可按 a → b → c → d 顺序并行开发、逐个合入；每个 PR 带上它负责的那部分旧 URL 重定向。

### 8.1 P1 落地记录

**后端 `viewerCaps`**（`server/services/knowledge-service.ts` 的 `entryViewerCaps` / `collectionViewerCaps`；路由 `GET /entries/:id` 走 `getEntryForViewer`，`GET /collections` 走 `listCollectionsForViewer`）。每个能力位调用的就是对应写接口的判定函数；原本内联的判定已抽出为共享函数，两边不可能漂移：

| 能力位 | 判定函数（写接口同用） | 对应写接口 |
|--------|------------------------|------------|
| `canWriteMain` | `canDirectWriteEntry` = `canWriteMain(entry) \|\| canWriteCollection(collection)`（从 `addRevision` 内联判定抽出） | `POST /entries/:id/revisions` |
| `canEditMeta` | `canWriteMain`（`loadWritableEntry`） | `PATCH /entries/:id` |
| `canDelete` | `canWriteMain`（`loadWritableEntry`） | `DELETE /entries/:id` |
| `canTransferOwner` | `isEntryOwnerOrAdmin`（`assertCanManageEntry` 的内联判定改为调用它） | `POST /entries/:id/transfer-owner` |
| `canManageAcl` | `canAdministerKnowledgeAcl` = admin（与路由上的 `requireAdmin` 同一规则） | `PATCH /entries/:id/acl` |
| `canReview` | `canReview(caps, entry)`（linked 提交的审核判定；自审禁止是逐请求规则，不并入） | `POST /submissions/:id/review`、`/resolve` |
| `myPersonalEntryId` | read store `findActivePersonalEntryId`（`idx_kd_entry_author` 单次索引查询，SQLite/PG 双实现） | — |
| 集合 `canEdit` / `canTransferOwner` / `canDelete` | `isCollectionOwnerOrAdmin`（`assertCanManageCollection`） | `PATCH` / `transfer-owner` / `DELETE /collections/:id` |
| 集合 `canManageAcl` | `canAdministerKnowledgeAcl` | `PATCH /collections/:id/acl` |
| 集合 `canCreateEntry` | `canWriteCollection`（`createEntry`） | `POST /entries` |

所有能力位都建立在调用方已通过读闸（`canRead` 含集合读闸）之上；principal caps 每请求解析一次，集合列表逐行只做纯函数计算。一致性测试：`server/services/__tests__/knowledge-viewer-caps.test.ts`，在 admin / 条目 owner / 集合 owner / write grant / review tag / 普通读者六类 principal 上逐位验证"能力位为 true ⇔ 对应调用不被拒绝"。外部 API v1 不暴露知识库（`/api/knowledge` 仅接受会话 JWT），新字段只出现在内部接口。

**前端**：`components/knowledge/editor/KnowledgeEditor.tsx`（编辑/预览/与基线 diff，宽屏另有并排；`savedValue` 判脏；`useBlocker` + `beforeunload`；Ctrl/Cmd+S）、`SubmissionStatusBadge.tsx`、`DriftBanner.tsx`。正文编辑处全部换用编辑器：新建条目弹窗、「我的库」新建表单、条目页直接编辑、草稿 tab、独立个人条目页、冲突解决。新建条目弹窗的标签改为 `TagsInput`。条目页、集合菜单与叙述者 dock 面板的按钮显隐全部改读 `viewerCaps`。

**与旧前端推断的差异（P1 修正的问题）**：

- write grant 持有者与集合 owner 可以直接写 main，但旧前端只给 admin/owner 显示「直接编辑」（dock 面板同样如此）。
- 集合 owner 能追加 revision，却**不能**改元数据或删除条目（`updateEntryMeta`/`deleteEntry` 只认 `canWriteMain`，不含集合 owner）；旧前端把元数据面板绑在 admin/owner 上，恰好对集合 owner 隐藏，但对 write grant 持有者也隐藏了（服务端其实允许）。现在两者都按服务端显示。
- 删除条目：服务端允许 write grant 持有者，旧前端只给 admin/owner。
- 集合的「编辑」「删除」按钮旧前端对所有人显示（服务端仅 admin/集合 owner），现在按能力位隐藏；「新建条目」只提供可建条目的集合。
- 条目 ACL：HTTP 接口仅 admin，但 agent 工具 `KnowledgeEdit` 允许条目 owner 设置 ACL（`isEntryOwnerOrAdmin`）。`canManageAcl` 反映 UI 所用的 HTTP 接口（admin）；两条路径的差异保持现状，未在 P1 改动。
- 自审：服务端允许 admin 审核自己的提交，条目页审核面板仍对作者本人隐藏操作（保守，维持旧行为）。

**偏差**：

- 状态徽章统一后 `rejected` 显示为红色（旧代码多处为灰色），`approved` 统一为绿色。
- 叙述者 dock 面板（§5.3 规定只读展示）仍保留其原有的轻量编辑，未嵌入 `KnowledgeEditor`，只把权限改为 `viewerCaps.canWriteMain`；去掉 dock 编辑留给 P2。
- 编辑器的 Ctrl/Cmd+S 绑定在编辑器根节点（焦点在编辑器内时生效），不是全局快捷键，避免同页多个编辑器互相抢占；未保存拦截对同页 hash 切 tab 同样生效（非活动 tab 会被卸载，编辑会丢）。
- `SubmissionAuthorActions` 的 `isAdmin`（撤回/重提）与独立提交审核弹窗的权限仍按旧逻辑，属于 P2a 请求详情页的范围。

### 8.2 P2a 落地记录

**后端**（`server/services/knowledge-branch-service.ts`）。`GET /submissions/:id` 走 `getSubmissionForViewer`，返回原有字段 + `diff`，以及：

- `viewerCaps`（`submissionViewerCaps`，纯函数，输入为同一套共享判定）：

| 能力位 | 判定（写接口同用） | 对应写接口 |
|--------|--------------------|------------|
| `isSubmitter` | `sub.submitterUserId === principal.userId` | — |
| `canReview` | `submissionReviewAuthority` 通过 ∧ `!isSelfReviewBlocked` ∧ status ∈ {pending, conflict} | `POST /submissions/:id/review`（至少 `comment_only`） |
| `canApprove` | 同上 ∧ status = pending（conflict 上 approve 被拒，须走 resolve） | `POST /submissions/:id/review`（`approve`） |
| `canResolveConflict` | 同上 ∧ status = conflict ∧ linked | `POST /submissions/:id/resolve` |
| `canWithdraw` | `canActAsSubmitter`（提交者或 admin）∧ `isWithdrawableStatus` | `POST /submissions/:id/withdraw` |
| `canResubmit` | `canActAsSubmitter` ∧ status = changes_requested | `POST /submissions/:id/resubmit` |

  `submissionReviewAuthority` 从 `review` / `resolveConflict` / 旧 `assertCanReviewStandalone` 的内联判定抽出：linked = 集合可读 ∧ `canReview(entry)`，standalone = 目标集合可读 ∧ 可写；返回值带写接口原样抛出的错误，写接口经 `assertSubmissionReviewAuthority` 调用，与 caps 不可能漂移。`isSelfReviewBlocked`、`canActAsSubmitter`、`isWithdrawableStatus` 同样被写接口与 caps 共用。`canResubmit` 只覆盖 resubmit 自身的闸门；`submitForReview` 的内容前置条件（个人条目已归档、独立型缺标题/目标集合）仍由调用返回错误说明。
- 展示元信息（§6.2），均在读闸之上再做 ACL：`entryTitle`、`currentMainContent`、`drift`（`drifted` + `versionsBehind`）需要对目标条目 `canRead`；`collectionName` 需要 `canReadCollection`；`baseContent`（fork 基线）可由 `proposedContent` + `diff` 反推，不构成新泄露；`submitterUsername` / `reviewerUsername` 一次 `inArray` 查询；`history` 为同一个人条目、**同一目标**的各轮请求（`idx_ks_draft`，上限 20，不含正文）。
- 读闸 `loadViewableSubmission`：提交者本人，或对目标有审核权；其余一律 404。修正了旧行为——standalone 请求对"能读不能写目标集合"的用户返回 400 并点名权限。
- `GET /submissions`（审核者列表）每行补 `entryTitle`（可读才给）、`targetCollectionId`、`collectionName`、`submitterUsername`、`drifted`（仅打开中的 linked 请求），三次批量查询，无逐行查询。
- 一致性测试 `server/services/__tests__/knowledge-submission-viewer-caps.test.ts`：admin / 条目 owner / 集合 owner / write grant / review tag / 普通读者 / 提交者七类 principal × pending / conflict / changes_requested / standalone 四种状态，逐位验证"能力位为 true ⇔ 对应调用不被拒绝"（看不到的请求按全 false 处理），另测 404 不泄露标题/正文与展示字段。

**前端**：

- `/knowledge/review`（`routes/knowledge/review/index.tsx`）：search `status`（`open` 默认 = pending + conflict，与导航徽章同口径；`pending`、`conflict`、`all`）、`collection`、`entry`；顶部 `ReviewScopeNotice`；每行一个请求，点击进请求页。知识库首页标题栏有带计数徽章的「审核收件箱」按钮，取代原 Review tab。
- `/knowledge/requests/$submissionId`（`routes/knowledge/requests/$submissionId.tsx`）：头部（状态、类型、轮次 + 上一轮链接、提交者、目标、变更说明、drift 警告）；正文 `SubmissionBody`（linked：对比当前 main / 对比基线 / 预览；无权读 main 时只给基线对比；standalone：预览）；审核者操作 `SubmissionReviewPanel`（verdict + findings，conflict 时为 `KnowledgeEditor` 合并编辑 + 非 approve 的 verdict）；作者操作 `SubmissionAuthorActions` + 「编辑我的版本」；其余只读；`SubmissionTimeline` 列出各轮 verdict / 审核者 / findings。页面上的操作集合由纯函数 `requestActions(viewerCaps)` 决定（`request-actions.test.ts`）。
- 去掉 §8.1 遗留的客户端推断：`SubmissionAuthorActions` 改收 `caps`（`canWithdraw` / `canResubmit`），独立提交审核弹窗与条目页提交 tab 的 `submissionTabNotice` 随页面一并删除。
- 导航迁移：`knowledgeRequestDestination(id)` 统一返回请求页；铃铛的审核者行与作者结论行（linked / standalone 一样）、「我的库」中有打开请求的条目、个人条目页的请求列表都进请求页；铃铛的收件箱入口进 `/knowledge/review`；导航徽章仍为收件箱计数。

**旧 URL 重定向**（`lib/knowledge-review-nav.ts` 纯函数，`beforeLoad` 中 `redirect({ replace: true })`，每行一条单测）：

| 旧 | 新 |
|----|----|
| `/knowledge?tab=review` | `/knowledge/review` |
| `/knowledge?tab=review&submission=<id>` | `/knowledge/requests/<id>`（id 不合法 → `/knowledge/review`） |
| `/knowledge/$entryId#submissions/<id>` | `/knowledge/requests/<id>`（id 不合法 → 下一行） |
| `/knowledge/$entryId#submissions` | `/knowledge/review?entry=$entryId&status=all` |

**偏差**：

- 条目页的 Submissions tab **删除**而非保留为精简列表：请求只有一个详情页，作者从「我的库」、铃铛或草稿 tab 下的请求列表进入，审核者从收件箱进入，保留第二个列表只会再造一个入口。草稿 tab 与独立个人条目页改为显示本版本的请求列表（`PersonalEntryRequests`，只链接，不再内嵌撤回/重提），因为个人版本页（§5.5）本就规划了"本版本的请求历史"。
- `#submissions` 重定向到 `?entry=` 过滤的收件箱且 `status=all`（与旧 tab 列出该条目所有可审请求一致）；**不**在无可见请求时自动跳回阅读页（需要先等列表加载，重定向就不再是纯函数），改为空状态里给"返回条目"链接。
- 收件箱多了 `open` 筛选并作为默认值：§5.6 的"待审"若只含 pending，会把同样等着审核者处理的 conflict 藏起来，且与导航徽章计数不一致。
- 集合筛选在客户端对（有上限的）审核者列表过滤：linked 请求的集合要经条目解析，服务端列表接口未加该参数。
- 请求页的"评审意见与 findings 时间线"按**轮次**展示：`knowledge_submissions` 每行只存最后一次 verdict/findings（`comment_only` 会覆盖），没有逐条评审事件表；补事件表属于后端状态机变更，不在本期。
- 自审：§8.1 中"审核面板对作者本人保守隐藏"的做法随 caps 取消——admin 审自己的请求时请求页会显示审核操作，与服务端 §6.7.5 的 admin 例外一致；非 admin 作者的 `canReview` 恒为 false。
- `/knowledge/personal/$id` 对 linked 条目的重定向改为：有带 findings 或打开中的请求 → 该请求页；否则 → 条目 `#draft`（P2b 再改到 `/knowledge/mine/<id>`）。「编辑我的版本」同样暂指 `#draft` / `/knowledge/personal/$id`。——P2b 已全部替换，见 §8.3。

### 8.3 P2b 落地记录

**后端**（`server/services/knowledge-branch-service.ts`，均为新增字段，未改语义）：

- `GET /personal-entries/:id` 走 `getMineForViewer`（读闸不变：`loadOwnDraft`，作者或 admin，其余 404），在条目本身之外返回：
  - `entryTitle`、`currentMainContent`（diff 基线）、`drift`（`drifted` + `versionsBehind`）——仅 linked 且调用者**当前**对该条目 `canRead`（集合闸 + 条目闸）；失去读权限后作者仍能看/改自己的副本，但 main 的标题、正文与漂移不再经个人条目泄露；
  - `targetCollectionName`——仅 standalone 且目标集合 `canReadCollection`；
  - `openSubmissions`——本版本处于 `CLOSED_ON_ENTRY_DELETE_STATUSES`（pending / conflict / changes_requested）的请求（id、状态、轮次、时间，无正文，上限 `OPEN_SUBMISSION_SCAN_LIMIT`），即删除会一并撤回的那些；
  - `viewerCaps`（`personalEntryViewerCaps`，纯函数）：

| 能力位 | 判定（写接口同用） | 对应写接口 |
|--------|--------------------|------------|
| `canEdit` | 未归档（`updateDraft`） | `PATCH /drafts/:id` |
| `canEditMeta` | 未归档 ∧ standalone（`updateStandaloneMeta`） | `PATCH /personal-entries/:id` |
| `canRebase` | 未归档 ∧ linked ∧ 条目可读（`rebaseDraft` 的 `assertCanRead`） | `POST /drafts/:id/rebase` |
| `canDelete` | 未归档（归档条目再删是幂等空操作，无可删） | `DELETE /personal-entries/:id` |
| `canSubmit` / `submitBlockers` | `personalEntrySubmitBlockers`：`archived`、`missing_target`、`missing_title`（`submitForReview` 改为调用同一纯函数 `personalEntrySubmitPreconditionBlocker` 抛错），`open_request` = 存在 `CLAIMABLE_SUBMISSION_STATUSES`（pending / conflict）请求，与 store 的 `createSubmissionGuarded` 同一集合 | `POST /drafts/:id/submit` |

- `GET /personal-entries` 走 `listMineForViewer`：原字段 + `drifted`（不变）+ `entryTitle`（linked 且可读）+ `targetCollectionName`（standalone 且可读）。条目与集合各一次 `inArray`，无逐行查询。
- 一致性测试 `server/services/__tests__/knowledge-personal-entry-viewer.test.ts`：linked / 漂移 / 有 pending 请求 / 已归档 / standalone / 缺目标 / standalone 有 pending 七种状态，逐位验证"能力位为 true ⇔ 对应调用不被拒绝"（每个探测用全新 fixture）；另测失去条目/集合读权限后标题、正文、drift 被隐去（详情与列表），以及他人 404。

**前端**：

- `/knowledge/mine`（`routes/knowledge/mine/index.tsx`）：关联型与独立型同一列表（标题——关联型用全局条目标题；类型「关联到「条目」」/「独立 · 目标集合」；落后 main、打开请求状态、已归档徽章；更新时间），search `archived=1` 显示已归档（默认隐藏，§10.3）；「我发起的请求」区：进行中（pending / conflict / changes_requested）在上、近 30 天结论在下，changes_requested 只列一次（在上，带 findings），每行进请求页。请求接口按设计不带标题，行标签取自本人的个人条目列表（需要时额外取一页 archived）。新建按钮 → `/knowledge/mine/new`。知识库首页标题栏「我的版本」按钮取代原「我的库」tab 及其常驻新建表单。
- `/knowledge/mine/new`：`KnowledgeEditor` + 标题、可选目标集合、关键词；保存后 `replace` 跳到 `/knowledge/mine/<id>`。
- `/knowledge/mine/$personalEntryId`：状态条（类型、关联条目链接或目标集合、drift、当前打开请求链接、更新时间）；`KnowledgeEditor`（linked 基线 = 当前 main，不可读时无基线；standalone 无基线）；linked 且 `canRebase` 时 `DriftBanner`；发布区（变更说明 + 提交审核，`submitBlockers` 全部列出；有未保存元数据时先要求保存；提交成功后给出新请求页链接，带 `driftWarning` 时同时提示落后版本数）；`PersonalEntryRequests` 请求历史；删除确认列出将被撤回的请求（链接到各请求页）。linked 的元数据卡只说明"标题/标签/关键词属于共享条目"并链接过去；standalone 可编辑标题/目标/关键词。
- 条目页：删除「我的草稿」tab；标题栏新增 `PersonalVersionAction`——有 `viewerCaps.myPersonalEntryId` → 「我的个人版本」链接；否则只读用户为「提议修改」，可直接写 main 的用户为次级「创建个人版本」，确认后 `createDraft` 并进入新页面。直接编辑 main（`canWriteMain`）不变。
- 带路径参数的链接用 `components/knowledge/router-links.tsx`（`createLink` 包装的 Mantine `Anchor` / `Button`），是真实 `<a href>` 且参数有类型检查。

**旧 URL 重定向**（`lib/knowledge-review-nav.ts` 纯函数，`beforeLoad` 中 `redirect({ replace: true })`，每行一条单测）：

| 旧 | 新 |
|----|----|
| `/knowledge?tab=mylibrary` | `/knowledge/mine` |
| `/knowledge/$entryId#draft`（有个人版本） | `/knowledge/mine/<draftId>` |
| `/knowledge/$entryId#draft`（没有） | `/knowledge/$entryId?personal=create`：阅读页并自动打开"创建个人版本"确认；关闭确认即去掉该参数 |
| `/knowledge/personal/$id` | `/knowledge/mine/$id`（linked 与 standalone 相同；路由文件只剩 `beforeLoad` 重定向） |

`#draft` 需要知道 `myPersonalEntryId`，所以这一行在 `beforeLoad` 里先 `queryClient.ensureQueryData` 读条目（与 `useKnowledgeEntry` 同一 query key，页面随后直接命中缓存）；读失败（条目不存在/不可读）按"没有"处理，落到阅读页的 not-found 状态。创建是写操作，从不由重定向自动完成。

**替换的 P2a 临时链接**：请求页「编辑我的版本」、P2a 的 `linkedPersonalRedirect`（删除）、「我的库」行目标（随 tab 删除）、叙述者 dock 面板 `KnowledgeEntryPanelContent` 个人条目的"在知识库中打开"、`NarratorPanel` 知识提示的个人条目回退导航、`shared/pretext-layout/tool-detail.ts`（`KnowledgeSearch` 的 standalone 结果与带 `personalEntryId` 的 `KnowledgeRead` / `KnowledgeEdit` 原先错误地链到 `/knowledge/<personalEntryId>`），全部改为 `knowledgePersonalEntryDestination(id)` / `/knowledge/mine/<id>`。铃铛无需改动：作者结论行一直有 submission，进请求页。

**偏差**：

- 目标集合下拉列出**所有可见集合**，而不是只列 `viewerCaps.canCreateEntry`：`submitForReview` 只检查"有目标"，不检查作者对目标的写权限（批准时需要的是**审核者**对目标的写权限，§6.7.5），只列可建条目的集合会把"通过审核发布"这条路对普通作者藏起来。服务端对目标集合的可读性也不校验，这一点维持现状；不可读的已存目标在下拉与状态条里显示为"你无权读取的集合"，不显示名称。
- "My" 未新增聚合接口（§6.2 的评估项）：沿用 `GET /personal-entries`（active，必要时 archived）、`/my-open-submissions`、`/my-submission-outcomes` 四个有上限的请求，各自已有缓存与失效路径；聚合只省请求数，却要复制三处 ACL 与上限逻辑。
- `#draft` 无个人版本时落到 `?personal=create`（新 search 参数）而非纯 hash，因为确认弹窗是页面状态、应可链接（§3）。
- 条目页的个人版本入口暂放在标题栏（与转移/删除并列）；§5.2 的完整头部与主次按钮布局属于 P2c。——P2c 已完成，见 §8.4。
- 去掉草稿 tab 后不再被任何页面使用的 hook（`useMyKnowledgeDraft`、`useKnowledgeDraftDrift`、`useUpdateKnowledgeDraft`、`useSubmitKnowledgeDraft`）与 `draft` / `draftDrift` 缓存键的失效一并删除；通知失效改为刷新 `personalEntry`（详情里带 drift 与打开请求）。对应 HTTP 接口保留（agent 工具与旧客户端仍在用）。

### 8.4 P2c 落地记录

**后端**（均为新增字段/参数，未改语义）：

- `GET /entries` 分页模式新增 `level`（有效密级：条目自身，为空取集合 `defaultLevel`，SQL 侧 `OR` + 集合 `IN` 子查询）、`status`（`active` / `archived`，SQL 侧）、`sort`（`updated` 默认 / `title`；带 `q` 时另有 `relevance` 默认）。新端口方法 `listEntriesOrdered`（SQLite 与 PostgreSQL 两份实现，同一投影、同一顺序）；`tag` 与 ACL 仍为后过滤，沿用 `ENTRY_PAGE_MAX_SCAN_ROWS` 扫描预算与"短页 + 续游标"语义。
- **游标设计**：`updated` 仍为 base64url(`[updatedAt, id]`)，旧游标继续有效；`title` 为 base64url(`["title", title, id]`)，keyset 谓词 `title > t OR (title = t AND id > i)`，与 `ORDER BY title, id` 同一列排序规则（SQLite 二进制、PG 数据库排序规则），重复标题由 `id` 决胜。游标与排序绑定：拿 `updated` 游标请求 `sort=title`（或反之）返回 400，不会静默返回错页。标题游标携带标题本身，游标上限由 512 提到 4096 字符。
- **未加索引**：按标题排序在集合内走 `idx_ke_collection` 过滤后排序，全量按标题排序是整表 top-N——与 P0 起全量按时间排序的现状相同，返回行数由扫描预算限界。补 `(collection_id, title, id)` 索引需要 SQLite + PG 两条迁移，与并行分支的迁移编号冲突风险大于收益，记为后续项（KNOWLEDGE_BASE.md §4.2 已注明）。
- 分页行注解：`ownerUsername`、`myPersonalEntryId`、`myPersonalEntryBehindMain`，每页两次 `IN` 批量查询（`listUsernamesByIds`、`listActivePersonalEntriesForEntries`，后者走 `idx_kd_entry_author`）。检索（`q`）行补齐 `ownerUserId` / `classificationLevel` 用一次 ACL 投影批量查询；检索行没有 revision id，"落后 main"只在非检索列表上标出。
- `GET /entries/:id` 增加 `ownerUsername`、`currentVersion`（最新 revision 元数据，`LIMIT 1`、不含正文）与能力位 `canManageLinks`；`GET /entries/:id/links` 每行增加 `canRemove`：

| 能力位 | 判定（写接口同用） | 对应写接口 |
|--------|--------------------|------------|
| `canManageLinks` | `canManageEntryLinks(caps, entry, collection)`（当前 = `canRead`；`addLink` 的源端、`removeLink` 改为调用它） | `POST /entries/:id/links`（本条目作源） |
| 链接 `canRemove` | 对该链接**源条目**的 `canManageEntryLinks` | `DELETE /links/:id` |

  一致性测试：`knowledge-viewer-caps.test.ts` 六类 principal 上验证 `canManageLinks ⇔ addLink 不被拒绝 ⇔ removeLink 不被拒绝`，以及入链 `canRemove ⇔ removeLink 不被拒绝`。列表/排序/筛选测试：`server/services/__tests__/knowledge-entry-list-sort-filter.test.ts`（标题序含重复标题、ACL 回填、游标与排序绑定、长中文标题游标往返、有效密级含集合默认与覆盖、状态、路由注解与 drift、`q` 分支的筛选与标题序、详情的 owner/版本号）；PG 集成测试（`PG_INTEGRATION=1`）补了标题序与筛选。

**前端**：

- 路由改为目录：`routes/knowledge/$entryId/{index,history,edit,settings}.tsx`，共用 `components/knowledge/entry/EntryPageFrame`（加载/不可读状态 + 面包屑 知识库 › 集合 [› 条目 › 子页]）。
- 阅读页（§5.2）：`EntryReadHeader`——标题、有效密级（继承自集合时为描边徽章 + 提示）、归档状态、标签、owner、更新时间与版本号；主操作由纯函数 `entryReadActions(viewerCaps)` 决定：`canWriteMain` → 「编辑」（`/edit`）+ 次级个人版本按钮，否则个人版本按钮（「提议修改」/「我的个人版本」，P2b 流程）为唯一实心主按钮；次级图标：历史、设置（任一设置能力位时）、复制链接。`EntryBanners`：`canReview` 时统计本条目 pending + conflict 请求数 → `/knowledge/review?entry=<id>`；有个人版本且其详情带打开请求时显示状态 → 请求页。正文 `MarkdownContent` 全宽；`EntryRelatedSection`（锚点 `#related`）出链/入链各按类型分组，添加按 `canManageLinks`，删除按每条 `canRemove`。常驻侧栏删除。
- `/history`：`EntryHistory`（原 HistoryTab：元数据列表 + 任选两版按需取正文 diff）。
- `/edit`：`KnowledgeEditor`（基线 = 当前 main，宽屏默认并排）+ 变更说明（写入新 revision 的 `changeNote`）；无改动时 Ctrl/Cmd+S 不追加空版本；保存后先释放离开拦截再回阅读页。`beforeLoad` 以与 `#draft` 相同的方式 `ensureQueryData` 读条目，`entryEditRedirect(entryId, canWriteMain)` 非直接写者（含读不到条目）一律回阅读页。
- `/settings`（§5.8）：`EntrySettingsCards`——基本信息（`canEditMeta` → `EntryMetaPanel`，否则只读）、访问控制（`canManageAcl` → `EntryAclPanel`，否则只读展示密级/受控标签/审核标签名，审核标签为空时显示 fail-closed 说明）、所有权（`canTransferOwner` → `TransferOwnerModal`）、危险操作（`canDelete` → 输入标题确认删除；无权限时整卡隐藏）。
- 浏览页（§5.1）：search `collection` / `q` / `tag` / `level` / `status` / `sort`（`lib/knowledge-browse-search.ts`，默认排序不入 URL：有 `q` 为相关度，否则为最近更新），文本框防抖写 URL；左侧 `CollectionTree`，每行"⋯"菜单按集合 `viewerCaps` 列出编辑 / 访问设置 / 转移 / 删除；右侧 `EntryTable` 表格行（标题 + 检索摘要、标签——点击即按标签筛选、有效密级、更新时间、owner、"我的版本"/"落后 main"徽章），沿用 P0 的游标"加载更多"；「新建」菜单（新建条目到可建集合 / 新建个人条目 → `/knowledge/mine/new`）；空状态区分 没有集合 / 无搜索结果（有任一筛选）/ 集合为空 / 无条目（`browseEmptyState`）。

**旧 URL 重定向**（`lib/knowledge-review-nav.ts` 纯函数，`/knowledge/$entryId/` 的 `beforeLoad` 中 `redirect({ replace: true })`，每行一条单测）：

| 旧 | 新 |
|----|----|
| `/knowledge/$entryId#content` | `/knowledge/$entryId` |
| `/knowledge/$entryId#history` | `/knowledge/$entryId/history` |
| `/knowledge/$entryId#links` | `/knowledge/$entryId#related`（加载后滚动到"关联条目"） |

P2a 的 `#submissions[/<id>]` 与 P2b 的 `#draft` 随路由一起搬到 `$entryId/index.tsx`，行为与测试不变。

**偏差**：

- §5.1 的"你没有权限看到更多"空状态未单列：服务端不（也不应）告知被 ACL 隐藏的行数；扫描预算耗尽的空页仍显示原有"继续加载"提示，"无搜索结果"的说明里注明不可读条目不会列出。
- 筛选条的状态筛选为新增服务端参数 `status`（§6.2 只列了 `level`、`sort`），成本是一个 SQL 等值条件（有 `idx_ke_status`）。
- 标签筛选是自由文本框（条目标签是自由标签，没有可枚举的列表接口；`/tags` 是 ACL 标签），另可点击行上的标签徽章。
- 浏览页的「Access Control」管理 tab 保留在 `?tab=admin`，属于 P2d 的拆分范围。——P2d 已拆出，见 §8.5。
- 设置入口仅在有任一设置能力位时出现在阅读页；设置页本身对任何可读者可直接打开（全部只读）。危险操作卡在无 `canDelete` 时隐藏而非只读——没有可展示的只读内容。
- 管理员在访问控制卡里仍可通过 `EntryAclPanel` 的 owner 字段改 owner（`PATCH …/acl` 的既有能力），与所有权卡的转移并存，未在 P2c 合并。
- 叙述者 dock 面板、`NarratorPanel` 知识提示与 `shared/pretext-layout/tool-detail.ts` 只链接 `/knowledge/<id>`（不带 tab hash），路径不变，无需修改。

### 8.5 P2d 落地记录

**无后端改动**：管理区的所有写接口（密级、标签、标签类型的增改删，`/grants/bulk`，`PUT /users/:id/acl`）与授权读取（`GET /grants`、`GET /users/:id/acl`、`/api/admin/users`）在服务端已是 `requireAdmin`，未改；密级与标签列表的 `GET` 本就对所有登录用户开放（浏览筛选与条目设置也在用）。

**前端**：

- 路由 `routes/knowledge/admin/`：`route.tsx` 为布局（面包屑 知识库 › 管理、标题与说明、左侧子导航，窄屏改为横向按钮行；`<Outlet />`），`index.tsx` 只在 `beforeLoad` 里 `redirect({ to: "/knowledge/admin/levels", replace: true })`，`levels.tsx` / `tags.tsx` / `users.tsx` 各挂一个分区组件。
- 子导航与门禁在 `lib/knowledge-admin-nav.ts`（纯函数 + `knowledge-admin-nav.test.ts`）：`KNOWLEDGE_ADMIN_SECTIONS` 只列已实现的三项；`knowledgeAdminGate(user, isLoading)`——当前用户 `role === "admin"` 放行，加载中不判定（显示 Loader），其余（含取不到用户）在布局里 `<Navigate to="/knowledge" replace />`。按角色决定纯管理界面属于 §8.1 允许的 `isAdmin` 用法；接口本身仍由服务端 `requireAdmin` 把关。
- `AclAdminPanel.tsx`（约 1160 行）按分区拆到 `components/knowledge/admin/`，函数体原样搬移、行为不变：`LevelsAdmin`（密级）、`TagsAdmin` + `TagTypesModal`（标签与标签类型）、`UsersAdmin` + `BulkGrantModal` + `UserPermissionDetail`（用户授权、批量授权、逐用户权限）。原文件删除。
- 浏览页：去掉只为管理 tab 保留的 `Tabs` 脚手架，`BrowsePanel` 直接渲染；标题栏对 admin 多一个「管理」按钮 → `/knowledge/admin`。`parseKnowledgeBrowseSearch` 不再保留 `tab`。
- i18n：新增 `adminEntry`、`adminTitle`、`adminNavLevels`、`adminNavTags`、`adminNavUsers`；删除不再使用的 `tabEntries`、`tabAdmin`、`aclTabLevels`、`aclTabTags`、`aclTabUsers`。说明文字沿用 `accessControlDesc`，移到管理区标题下。

**旧 URL 重定向**（`lib/knowledge-review-nav.ts` 的 `legacyKnowledgeIndexRedirect`，`/knowledge/` 的 `beforeLoad` 中 `redirect({ replace: true })`，单测在 `knowledge-review-nav.test.ts` 的 P2d 分组）：

| 旧 | 新 |
|----|----|
| `/knowledge?tab=admin` | `/knowledge/admin`（→ `/knowledge/admin/levels`；非 admin 再被布局送回 `/knowledge`） |

**偏差**：

- 子导航未列「ACL 审计」「知识包」：两页属于 P3、尚未实现，按"不放死链接"处理；实现时把条目加进 `KNOWLEDGE_ADMIN_SECTIONS` 即可。
- 门禁在布局组件里判定（与 `/settings` 的 admin 页相同做法），而不是在 `beforeLoad` 里：当前用户来自 `useCurrentUser` 的 query，组件层判定无需复制其 token / 重试逻辑；子路由组件只在放行后才挂载，非 admin 不会发出管理接口请求。
- 旧管理 tab 内的分区切换是组件状态（不可链接）；现在每个分区有自己的 URL。用户授权分区里"当前选中的用户"仍是页面内状态，未进 URL（与旧行为一致，属于 §5.8 之外的新功能）。
- 非 admin 打开旧的 `/knowledge?tab=admin`：旧行为是忽略该参数、停在浏览页；现在经两次 `replace` 重定向后同样落在 `/knowledge`，历史记录里不留中间页。

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
3. **archived 个人条目在"我的"页的展示**：默认隐藏，提供"显示已归档"开关（P2b 已按此实现，`/knowledge/mine?archived=1`）；清理策略仍按 KNOWLEDGE_BASE.md §10.2 待定。
