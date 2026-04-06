# 自我改进工作流补全计划

理想工作流：提出需求/bug → 分叉 chapter → review → 启动容器绑定域名 → browser 测试 → 修复/二轮 review → 提交 → 合并主分支。

本计划分阶段补全各环节的断层和稳健性问题。

---

## 第零阶段：稳健性修补（基础设施）✅ 已完成

所有后续阶段的前提，修复现有流程中的硬伤。四项全部并行完成。

- **0-A** Fork 流程关键失败可见化 → `2619dba`
  - warnings 数组收集非致命错误（快照恢复失败、容器启动失败）
  - 容器启动失败保留 containerConfig 而非清空
  - 前端 ChapterForkModal 用黄色 toast 显示警告
- **0-B** Review 初始消息改为 await → `c7267dd`
  - sendMessage 从 fire-and-forget 改为 await，失败触发完整回滚
- **0-C** Merge 的 git/DB 一致性保护 → `06ee29c`
  - preMergeTargetSha 移入 worktreeLock 内部消除竞态
  - DB 两次重试失败后尝试 git reset --hard 回滚
  - finalizeTempMerge 中即时获取 preMergeTargetSha
- **0-D** Review diff 大小限制 → `cfb8b35`
  - 80k 字符上限，committed diff 优先，行边界截断
  - 截断后附加提示引导使用 Read/Grep 工具

---

## 第一阶段：结构化 Review 结论 + 合并门控

打通"review → 修复 → 合并"闭环的核心。

### 1-A. 结构化 Review 结论（先行，1-B 和 1-C 依赖此项）

新增 `review_conclusions` 表：

```typescript
review_conclusions = {
  id: text (PK),
  reviewChapterId: text (FK → chapters),
  sourceChapterId: text (FK → chapters),
  verdict: "approve" | "request_changes" | "comment_only",
  findingsJson: JSON,  // [{severity, file, line, message}]
  createdAt: text,
}
```

修改 review 系统提示词，要求评审者在结论中调用新的 `ConcludeReview` 工具（替代当前的自然语言结论）。工具执行时写入 `review_conclusions` 表并触发 `concludeReview()`。

涉及文件：
- `server/db/schema.ts` — 新增表定义
- `server/lib/prompt-i18n.ts` — 修改 review 系统提示词，加入 ConcludeReview 工具说明
- `server/services/review-service.ts` — concludeReview 改为从 review_conclusions 读取结论
- `server/lib/agent/tools/` — 新增 conclude-review 工具定义
- `server/routes/reviews.ts` — 新增 GET /api/reviews/:id/conclusion 端点
- 运行 `bun run db:generate` 生成迁移

### 1-B. 合并门控（依赖 1-A）

在 `chapter-merge.ts` 的 `merge()` 方法中增加可选检查：

```typescript
// 项目级配置：chapterSettings.requireReviewBeforeMerge
if (project.chapterSettings?.requireReviewBeforeMerge) {
  const latestReview = await getLatestReview(sourceChapterId);
  if (!latestReview || latestReview.verdict !== "approve") {
    throw new ValidationError("Review approval required before merge");
  }
}
```

涉及文件：
- `server/services/chapter-merge.ts` — merge() 增加 review 检查
- `server/db/schema.ts` — projects.chapterSettings 类型扩展（加 requireReviewBeforeMerge）
- `frontend/components/graph/NodeContextMenu.tsx` — 合并按钮根据 review 状态显示不同样式
- `frontend/locales/` — 新增 i18n 键

### 1-C. Review 意见自动注入源叙述者（依赖 1-A）

监听 `review:concluded` 事件，将结构化结论作为 system message 注入源章节的 primary narrator：

```typescript
eventBus.on("review:concluded", async (event) => {
  const conclusion = await getReviewConclusion(event.reviewChapterId);
  const sourceNarrator = await getPrimaryNarrator(event.sourceChapterId);
  await persistSystemMessage(sourceNarrator.id, {
    type: "review_feedback",
    verdict: conclusion.verdict,
    findings: conclusion.findings,
  });
});
```

涉及文件：
- `server/services/narrator-event-handler.ts` 或新建 `server/services/review-event-handler.ts` — 事件监听
- `server/services/narrator-service.ts` — persistSystemMessage 支持 review_feedback 类型
- `frontend/components/narrator/` — 渲染 review_feedback 系统消息卡片
- WebSocket 广播确保前端实时显示

---

## 第二阶段：容器 → 测试闭环 ✅ 已完成

可与第一阶段并行实施。

### 2-A. 容器就绪事件链

监听 `container:started` 事件，自动向章节的 primary narrator 注入系统消息：

```typescript
eventBus.on("container:started", async (event) => {
  const narrator = await getPrimaryNarrator(event.chapterId);
  const containerInfo = await getContainerAccessInfo(event.chapterId);
  await persistSystemMessage(narrator.id, {
    type: "container_ready",
    urls: containerInfo.urls,
    services: containerInfo.services,
  });
});
```

涉及文件：
- `server/services/container-service.ts` 或新建事件处理器 — 事件监听
- `server/services/narrator-service.ts` — persistSystemMessage 支持 container_ready 类型
- `frontend/components/narrator/` — 渲染 container_ready 系统消息卡片

### 2-B. Browser 工具自动启用

当章节有 containerConfig 且容器启动成功时，自动将 Browser 加入叙述者的 enabledTools：

```typescript
// container:started 事件处理中
if (!narrator.enabledTools?.includes("Browser")) {
  await db.update(narrators).set({
    enabledTools: [...(narrator.enabledTools ?? []), "Browser"],
  });
}
```

涉及文件：
- 与 2-A 同一个事件处理器
- `server/db/schema.ts` — 无需修改（enabledTools 已是 JSON 字段）

### 2-C. 容器 IP 获取重试

`getContainerIp()` 增加指数退避重试（最多 3 次，间隔 1s/2s/4s），解决容器网络未就绪的问题。

涉及文件：
- `server/services/container-service.ts` — getContainerIp 方法

---

## 第三阶段：Review 修复自动化

依赖第一阶段的结构化结论（1-A）。3-A 和 3-B 可并行。

### 3-A. "Apply Review Fixes" 操作

在 review 结论为 `request_changes` 时，前端右键菜单增加"Apply Review Fixes"选项。后端向源叙述者发送包含结构化评审意见的用户消息，叙述者根据意见执行修复：

```typescript
async applyReviewFixes(sourceChapterId: string, reviewChapterId: string) {
  const conclusion = await getReviewConclusion(reviewChapterId);
  const narrator = await getPrimaryNarrator(sourceChapterId);
  const fixMessage = buildFixPrompt(conclusion.findings);
  await sendMessage(narrator.id, fixMessage);
}
```

涉及文件：
- `server/services/review-service.ts` — 新增 applyReviewFixes 方法
- `server/routes/reviews.ts` — 新增 POST /api/reviews/:id/apply-fixes 端点
- `server/lib/prompt-i18n.ts` — buildFixPrompt 模板
- `frontend/components/graph/NodeContextMenu.tsx` — 新增菜单项
- `frontend/locales/` — 新增 i18n 键

### 3-B. 二轮 Review 快捷操作

修复完成后，前端提供"Re-review"按钮，自动创建新的 review 章节，系统提示词中注入上一轮的结论作为对比基线：

```typescript
async createFollowUpReview(sourceChapterId: string, previousReviewId: string) {
  const previousConclusion = await getReviewConclusion(previousReviewId);
  const review = await reviewService.createReview(sourceChapterId, {
    previousReviewConclusion: previousConclusion,
  });
}
```

评审提示词增加："以下是上一轮评审的结论，请重点检查这些问题是否已修复。"

涉及文件：
- `server/services/review-service.ts` — createReview 支持 previousReviewConclusion 参数
- `server/lib/prompt-i18n.ts` — 增加 follow-up review 提示词模板
- `server/routes/chapters.ts` — 新增 POST /api/chapters/:id/re-review 端点
- `frontend/components/graph/NodeContextMenu.tsx` — 新增菜单项
- `frontend/locales/` — 新增 i18n 键

---

## 第四阶段：端到端工作流编排

锦上添花，在前三个阶段稳定后启动。

### 4-A. 改进任务（Improvement Task）实体（先行，4-B 和 4-C 依赖此项）

新增 `improvement_tasks` 表，贯穿整个工作流：

```typescript
improvement_tasks = {
  id: text (PK),
  projectId: text (FK → projects),
  title: text,
  description: text,
  status: "open" | "in_progress" | "reviewing" | "testing" | "done" | "closed",
  sourceNarratorId: text,     // 需求讨论的独立叙述者
  chapterId: text,            // 实施章节
  reviewChapterId: text,      // 最新 review 章节
  mergedAt: text,
  createdAt: text,
  updatedAt: text,
}
```

涉及文件：
- `server/db/schema.ts` — 新增表定义
- `server/db/relations.ts` — 关系定义
- `server/services/improvement-task-service.ts` — CRUD 服务
- `server/routes/improvement-tasks.ts` — API 路由
- `server/app.ts` — 注册路由
- 运行 `bun run db:generate` 生成迁移

### 4-B. 事件驱动的状态自动流转（依赖 4-A）

通过 event-bus 监听关键事件，自动推进任务状态：

```
chapter:forked        → status: in_progress
review:created        → status: reviewing
review:concluded      → status: reviewing (verdict 附加)
container:started     → status: testing
chapter:merged        → status: done
```

涉及文件：
- `server/services/improvement-task-service.ts` — 事件监听和状态流转逻辑

### 4-C. 前端任务看板（依赖 4-A）

在项目页面增加任务视图（侧边面板或独立路由），显示所有 improvement tasks 的状态流水线。每个任务卡片链接到对应的叙述者、章节、review。

涉及文件：
- `frontend/routes/` — 新增任务路由
- `frontend/components/` — 任务看板组件
- `frontend/hooks/` — useImprovementTasks hook
- `frontend/lib/api.ts` — API 调用
- `frontend/locales/` — 新增 i18n 键

---

## 并行实施总览

```
时间线 →

第零阶段（并行）:  ┌─ 0-A Fork 警告 ─────┐
                   ├─ 0-B Review await ───┤  ✅ 全部完成
                   ├─ 0-C Merge 一致性 ───┤
                   └─ 0-D Diff 限制 ──────┘
                            ↓
第一阶段:          ┌─ 1-A 结构化结论 ─┐
                   │        ↓         │
                   │  ┌─ 1-B 合并门控 ─┤  (并行)
                   │  └─ 1-C 意见注入 ─┘
                   ↓
第二阶段（并行）:  ┌─ 2-A 容器就绪链 ──┐     ← 可与第一阶段并行
                   ├─ 2-B Browser 自启 ─┤
                   └─ 2-C IP 重试 ──────┘
                            ↓
第三阶段（并行）:  ┌─ 3-A 自动修复 ───┐     ← 依赖 1-A
                   └─ 3-B 二轮 Review ─┘
                            ↓
第四阶段:          ┌─ 4-A Task 实体 ──┐
                   │        ↓         │
                   │  ┌─ 4-B 事件流转 ─┤  (并行)
                   │  └─ 4-C 任务看板 ─┘
```

最大并行度：第二阶段 3 项可以与第一阶段的 1-B/1-C 同时推进。前三个阶段可以在两个迭代周期内完成。第四阶段在前三个阶段稳定后启动。
