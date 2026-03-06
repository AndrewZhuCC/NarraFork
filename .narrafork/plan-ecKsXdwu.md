# 后台 Task 能力实现计划

## 背景分析

### 现有能力

NarraFork 已实现类似 Claude Code 的 Task（子 agent）能力：

- **子 agent 类型**：`explore`（只读）、`plan`（规划）、`general`（完整工具集）
- **并行执行**：`PARALLEL_TOOLS` 已包含 Task，SDK 返回多个 Task 工具调用时并行执行
- **消息树**：`parentToolUseId` 实现嵌套消息树，前端 `SubagentCard` 递归渲染
- **权限系统**：子 agent 权限请求广播到父叙述者的 WebSocket 频道
- **上下文管理**：子 agent 支持 compact/prune 循环
- **继续执行**：`ContinueTask` 工具支持继续已完成的子 agent

### 当前限制

1. **子 agent 阻塞父叙述者**：虽然多个 Task 并行执行，但父叙述者的当前 turn 必须等待所有 Task 完成才能继续
2. **无后台执行模式**：用户无法在子 agent 运行时继续与父叙述者对话
3. **无全局任务视图**：子 agent 状态只在 SubagentCard 内展示，无独立的任务管理界面
4. **子 agent 不能嵌套**：子 agent 不能再启动子 agent

### 目标

实现"后台 Task"能力，让叙述者可以启动后台运行的子 agent，父叙述者不阻塞，用户可以继续交互。后台 Task 完成后，结果异步注入父叙述者的上下文。

---

## 设计方案

### 核心概念

引入 `BackgroundTask` 概念——一种特殊的子 agent 执行模式：

- 父叙述者发起 Task 时可指定 `background: true`
- 后台 Task 立即返回一个 `taskId`，父叙述者继续执行
- 后台 Task 在独立的执行上下文中运行
- 完成后通过事件通知父叙述者和前端
- 父叙述者可通过 `CheckBackgroundTask` 工具查询状态/获取结果

### 方案选择：渐进式扩展

不引入新的数据库表，而是在现有 `narrators` 和 `narrator_tool_calls` 表上扩展字段，保持架构一致性。

---

## 详细实现步骤

### Phase 1: 数据模型扩展

#### 1.1 扩展 `narrators` 表

在 `server/db/schema.ts` 中添加字段：

```typescript
// narrators 表新增字段
isBackground: integer("is_background").default(0),  // 是否为后台 Task
backgroundStatus: text("background_status"),          // "running" | "completed" | "failed" | "cancelled"
backgroundResult: text("background_result"),          // 后台 Task 的最终结果文本
backgroundCompletedAt: text("background_completed_at"), // 完成时间
```

#### 1.2 扩展 `narrator_tool_calls` 表

```typescript
// narrator_tool_calls 表新增字段
isBackground: integer("is_background").default(0),  // 标记此 tool_call 是后台 Task
```

#### 1.3 生成迁移

```bash
bun run db:generate
```

### Phase 2: 后端核心逻辑

#### 2.1 修改 Task 工具定义

在工具定义中添加 `background` 参数（文件位置待确认，可能在 `server/lib/agent/tools/` 下）：

```typescript
// Task 工具的参数 schema 扩展
{
  name: "Task",
  parameters: {
    // ... 现有参数
    background: {
      type: "boolean",
      description: "If true, run the task in the background without blocking the parent. Returns a task ID immediately.",
      optional: true,
    }
  }
}
```

#### 2.2 修改 `narrator-subagent.ts` 中的 `runSubagent()`

核心变更：当 `background: true` 时，不等待子 agent 完成，立即返回 taskId。

```typescript
async function runSubagent(opts: RunSubagentOptions): Promise<string> {
  const { background, ...rest } = opts;
  
  // 创建子 agent narrator（同现有逻辑）
  const subagentNarrator = await createSubagentNarrator(rest);
  
  if (background) {
    // 标记为后台 Task
    await db.update(narrators)
      .set({ isBackground: 1, backgroundStatus: "running" })
      .where(eq(narrators.id, subagentNarrator.id));
    
    // 标记 tool_call 为后台
    await db.update(narratorToolCalls)
      .set({ isBackground: 1 })
      .where(eq(narratorToolCalls.toolUseId, opts.toolUseId));
    
    // 广播后台 Task 启动事件
    eventBus.emit({
      type: "narrator:background_task_started",
      narratorId: opts.parentNarratorId,
      taskNarratorId: subagentNarrator.id,
      toolUseId: opts.toolUseId,
    });
    
    // 后台执行（fire-and-forget）
    executeBackgroundTask(subagentNarrator.id, rest).catch((err) => {
      // 错误处理：更新状态，广播事件
    });
    
    // 立即返回 taskId
    return `<background_task_id>${subagentNarrator.id}</background_task_id>\n\nBackground task started. Use CheckBackgroundTask with this ID to check status or get results.`;
  }
  
  // 现有同步逻辑不变
  return await executeSubagentSync(rest);
}
```

#### 2.3 实现 `executeBackgroundTask()`

```typescript
async function executeBackgroundTask(
  narratorId: string,
  opts: SubagentExecOptions
): Promise<void> {
  try {
    const result = await executeSubagent(opts);
    
    // 更新状态和结果
    await db.update(narrators).set({
      backgroundStatus: "completed",
      backgroundResult: result.finalText,
      backgroundCompletedAt: new Date().toISOString(),
    }).where(eq(narrators.id, narratorId));
    
    // 广播完成事件
    eventBus.emit({
      type: "narrator:background_task_completed",
      narratorId: opts.parentNarratorId,
      taskNarratorId: narratorId,
      toolUseId: opts.toolUseId,
      resultPreview: result.finalText.slice(0, 500),
    });
  } catch (err) {
    await db.update(narrators).set({
      backgroundStatus: "failed",
      backgroundResult: String(err),
      backgroundCompletedAt: new Date().toISOString(),
    }).where(eq(narrators.id, narratorId));
    
    eventBus.emit({
      type: "narrator:background_task_failed",
      narratorId: opts.parentNarratorId,
      taskNarratorId: narratorId,
      toolUseId: opts.toolUseId,
      error: String(err),
    });
  }
}
```

#### 2.4 新增 `CheckBackgroundTask` 工具

创建新工具，让父叙述者查询后台 Task 状态：

```typescript
// server/lib/agent/tools/check-background-task.ts
{
  name: "CheckBackgroundTask",
  description: "Check the status of a background task or retrieve its results.",
  parameters: {
    task_id: {
      type: "string",
      description: "The background task ID returned by a previous Task call with background=true",
      required: true,
    },
    wait: {
      type: "boolean",
      description: "If true, wait for the task to complete before returning (with timeout). Default: false",
      optional: true,
    },
    timeout_ms: {
      type: "number",
      description: "Maximum time to wait in milliseconds when wait=true. Default: 30000",
      optional: true,
    }
  }
}
```

执行逻辑：

```typescript
async function executeCheckBackgroundTask(input: CheckBackgroundTaskInput): Promise<string> {
  const narrator = await narratorService.getById(input.task_id);
  
  if (!narrator || !narrator.isBackground) {
    return "Error: Invalid background task ID.";
  }
  
  if (input.wait && narrator.backgroundStatus === "running") {
    // 等待完成（通过事件监听 + 超时）
    const result = await waitForBackgroundTask(
      input.task_id,
      input.timeout_ms ?? 30000
    );
    return formatTaskResult(result);
  }
  
  return formatTaskStatus(narrator);
}

function formatTaskStatus(narrator): string {
  if (narrator.backgroundStatus === "running") {
    return `Background task ${narrator.id} is still running.`;
  }
  if (narrator.backgroundStatus === "completed") {
    return `Background task ${narrator.id} completed.\n\nResult:\n${narrator.backgroundResult}`;
  }
  if (narrator.backgroundStatus === "failed") {
    return `Background task ${narrator.id} failed.\n\nError:\n${narrator.backgroundResult}`;
  }
  return `Background task ${narrator.id} status: ${narrator.backgroundStatus}`;
}
```

#### 2.5 新增 `CancelBackgroundTask` 工具（可选）

```typescript
{
  name: "CancelBackgroundTask",
  description: "Cancel a running background task.",
  parameters: {
    task_id: { type: "string", required: true }
  }
}
```

通过 `AbortController` 取消后台 Task 的执行。

#### 2.6 修改 `loop.ts` 中的工具分组逻辑

将 `CheckBackgroundTask` 添加到快速工具列表（不需要权限检查）：

```typescript
// 后台 Task 相关工具不需要权限
const AUTO_ALLOW_TOOLS = new Set(["CheckBackgroundTask"]);
```

将 `PARALLEL_TOOLS` 保持不变（Task 已在其中）。

### Phase 3: 事件系统扩展

#### 3.1 扩展事件类型

在 `server/lib/event-bus.ts` 中添加：

```typescript
| {
    type: "narrator:background_task_started";
    narratorId: string;        // 父叙述者 ID
    taskNarratorId: string;    // 后台 Task 的叙述者 ID
    toolUseId: string;
    subagentType: string;
  }
| {
    type: "narrator:background_task_completed";
    narratorId: string;
    taskNarratorId: string;
    toolUseId: string;
    resultPreview: string;     // 结果预览（前 500 字符）
  }
| {
    type: "narrator:background_task_failed";
    narratorId: string;
    taskNarratorId: string;
    toolUseId: string;
    error: string;
  }
| {
    type: "narrator:background_task_cancelled";
    narratorId: string;
    taskNarratorId: string;
    toolUseId: string;
  }
```

#### 3.2 扩展 WebSocket 消息类型

在 `server/websocket/narrator-ws.ts` 中添加对应的服务器消息类型，并在事件监听器中广播。

### Phase 4: 权限处理

#### 4.1 后台 Task 的权限策略

后台 Task 的权限处理是关键设计决策。方案：

**方案 A（推荐）：继承父叙述者的权限模式**
- 后台 Task 继承父叙述者的 `permissionMode`
- 权限请求仍然广播到父叙述者的 WebSocket 频道
- 用户可以在前端看到后台 Task 的权限请求并做出决策
- 如果用户不响应，5 分钟超时自动拒绝

**方案 B：预设权限模式**
- 后台 Task 启动时可指定权限模式（如 `bypassPermissions`）
- 适合信任度高的场景

实现方案 A：

```typescript
// 后台 Task 的权限处理器
const permissionHandler = async (toolName, input, toolUseId) => {
  // 使用父叙述者的 broadcastTargetId
  return handlePermission({
    narratorId: subagentNarratorId,
    broadcastTargetId: parentNarratorId,  // 广播到父叙述者
    toolName,
    input,
    toolUseId,
  });
};
```

#### 4.2 后台 Task 权限请求的前端展示

权限请求通过父叙述者的 WebSocket 频道广播，前端已有处理逻辑。需要在权限请求 UI 中标注"来自后台 Task"。

### Phase 5: 前端实现

#### 5.1 扩展 SubagentCard

修改 `SubagentCard.tsx` 以支持后台 Task 的特殊展示：

```tsx
// 后台 Task 标识
{toolCall.isBackground && (
  <Badge color="blue" variant="light" size="xs">
    {t("background")}
  </Badge>
)}

// 后台 Task 状态
{toolCall.isBackground && toolCall.status === "running" && (
  <Text size="xs" c="dimmed">
    {t("backgroundRunning")}
  </Text>
)}

// 后台 Task 完成通知
{toolCall.isBackground && toolCall.status === "completed" && (
  <Text size="xs" c="green">
    {t("backgroundCompleted")}
  </Text>
)}
```

#### 5.2 后台 Task 通知

在 `useNarratorPanelWS.ts` 中处理后台 Task 事件：

```typescript
case "background_task_completed":
  // 显示通知
  notifications.show({
    title: t("backgroundTaskCompleted"),
    message: event.resultPreview,
    color: "green",
  });
  // 更新工具调用状态
  updateToolCallInCache(event.toolUseId, { status: "completed" });
  break;

case "background_task_failed":
  notifications.show({
    title: t("backgroundTaskFailed"),
    message: event.error,
    color: "red",
  });
  break;
```

#### 5.3 后台 Task 面板（可选增强）

在 `NarratorPanel` 中添加一个可折叠的"后台任务"区域，显示所有活跃的后台 Task：

```tsx
<BackgroundTasksBar
  narratorId={narratorId}
  tasks={backgroundTasks}
  onViewTask={(taskId) => scrollToTask(taskId)}
/>
```

### Phase 6: API 扩展

#### 6.1 后台 Task 查询接口

```
GET /api/narrators/:id/background-tasks
```

返回该叙述者的所有后台 Task 列表（状态、进度、结果预览）。

#### 6.2 后台 Task 取消接口

```
POST /api/narrators/:id/background-tasks/:taskId/cancel
```

取消正在运行的后台 Task。

### Phase 7: 国际化

在 `frontend/locales/{en,zh-CN}/narrator.json` 中添加后台 Task 相关的翻译键。

---

## 实现顺序

建议按以下顺序实现，每个步骤都可独立测试：

1. **数据模型扩展**（Phase 1）— 添加字段，生成迁移
2. **后端核心**（Phase 2.1-2.3）— Task 工具 background 参数 + 后台执行逻辑
3. **事件系统**（Phase 3）— 后台 Task 事件类型
4. **CheckBackgroundTask 工具**（Phase 2.4）— 查询后台 Task 状态
5. **权限处理**（Phase 4）— 后台 Task 权限策略
6. **前端 SubagentCard 扩展**（Phase 5.1-5.2）— 后台 Task 标识和通知
7. **API 接口**（Phase 6）— 查询和取消
8. **前端面板**（Phase 5.3）— 后台任务聚合视图
9. **国际化**（Phase 7）
10. **CancelBackgroundTask 工具**（Phase 2.5）— 取消能力

---

## 风险和注意事项

### 资源管理
- 后台 Task 消耗 API 配额和服务器资源
- 需要限制每个叙述者的并发后台 Task 数量（建议上限 3-5 个）
- 后台 Task 应有最大执行时间限制（建议 30 分钟）

### 上下文一致性
- 后台 Task 运行期间，父叙述者可能修改了文件
- 后台 Task 的结果可能基于过时的文件状态
- 需要在结果中标注"基于 Task 启动时的文件状态"

### 权限安全
- 后台 Task 的权限请求可能在用户不关注时到达
- 超时自动拒绝是安全的默认行为
- 考虑添加"后台 Task 权限预批准"选项

### AbortController 管理
- 后台 Task 需要独立的 AbortController
- 父叙述者中断时，需要决定是否同时取消后台 Task
- 建议：父叙述者中断不影响后台 Task（除非显式取消）

### 并发冲突
- 多个后台 Task 可能同时修改同一文件
- `general` 类型的后台 Task 有写入能力，需要注意冲突
- 建议：后台 Task 默认使用 `explore` 类型（只读），`general` 类型需要显式确认

---

## 与现有架构的兼容性

本方案的核心设计原则是**最小侵入**：

1. **不引入新表**：复用 `narrators` 和 `narrator_tool_calls` 表
2. **不改变现有子 agent 流程**：`background: false`（默认）时行为完全不变
3. **复用事件系统**：新事件类型遵循现有模式
4. **复用权限系统**：后台 Task 权限走现有通道
5. **复用前端组件**：扩展 SubagentCard 而非新建组件
6. **复用消息树**：后台 Task 的消息仍通过 `parentToolUseId` 关联

唯一的"破坏性"变更是 Task 工具的返回时机——当 `background: true` 时立即返回而非等待完成。这需要在 `loop.ts` 的工具结果处理中做适配。
