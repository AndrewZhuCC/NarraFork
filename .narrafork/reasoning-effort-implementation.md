# Codex 模型思考强度设置功能实现

## 概述

为 Codex 模型添加了思考强度（Reasoning Effort）设置功能，允许用户控制模型的推理深度。

## 后端实现

### 1. 数据库 Schema
- **文件**: `server/db/schema.ts`
- **修改**: 在 `narrators` 表中添加 `reasoningEffort` 字段
- **类型**: `text("reasoning_effort", { enum: ["low", "medium", "high", "xhigh"] })`
- **迁移文件**: `drizzle/0003_slim_meteorite.sql`

### 2. 类型定义
- **文件**: `server/lib/agent/types.ts`
- **修改**: 在 `AgentConfig` 接口中添加 `reasoningEffort?: "low" | "medium" | "high" | "xhigh"`

### 3. Provider 接口
- **文件**: `server/lib/agent/provider.ts`
- **修改**: 在 `ChatParams` 接口中添加 `reasoningEffort` 参数

### 4. Agent Loop
- **文件**: `server/lib/agent/loop.ts`
- **修改**: 在调用 `provider.chat()` 时传递 `reasoningEffort` 参数

### 5. OpenAI Provider
- **文件**: `server/lib/agent/openai-provider.ts`
- **修改**: 在 Responses API 请求中添加 reasoning 配置
```typescript
if (params.reasoningEffort) {
  body.reasoning = {
    effort: params.reasoningEffort,
    summary: "auto",
  };
  body.include = ["reasoning.encrypted_content"];
}
```

### 6. Narrator Service
- **文件**: `server/services/narrator-service.ts`
- **新增方法**: `updateReasoningEffort(narratorId, reasoningEffort)`

### 7. Narrator Session
- **文件**: `server/services/narrator-session.ts`
- **修改**: 在构建 `AgentConfig` 时从数据库读取 `reasoningEffort`

### 8. API 路由
- **文件**: `server/routes/narrators.ts`
- **新增端点**: `PATCH /api/narrators/:id/reasoning-effort`
- **请求体**: `{ reasoningEffort: "low" | "medium" | "high" | "xhigh" | null }`

## 前端实现

### 1. API 客户端
- **文件**: `frontend/lib/api.ts`
- **新增方法**: `updateNarratorReasoningEffort(id, reasoningEffort)`
- **修改**: 在 `createNarrator` 参数中添加 `reasoningEffort` 字段

### 2. React Hook
- **文件**: `frontend/hooks/useNarrator.ts`
- **新增 Hook**: `useUpdateReasoningEffort()`

### 3. UI 组件
- **文件**: `frontend/components/narrator/NarratorPanel.tsx`
- **修改内容**:
  - 导入 `useUpdateReasoningEffort` hook
  - 添加 `reasoningEffortMutation` 变量
  - 在 Desktop selects 区域添加思考强度选择器（权限模式选择器之后）
  - 在 Mobile 区域添加思考强度选择器（使用字母缩写：A/L/M/H/X）
  - 仅在 `narrator.model?.startsWith("codex:")` 时显示

### 4. 国际化
- **文件**: 
  - `frontend/locales/en/narrator.json`
  - `frontend/locales/zh-CN/narrator.json`
- **新增翻译**:
  - `reasoningEffort`: "Reasoning Effort" / "思考强度"
  - `reasoning_auto`: "Auto" / "自动"
  - `reasoning_low`: "Low" / "低"
  - `reasoning_medium`: "Medium" / "中"
  - `reasoning_high`: "High" / "高"
  - `reasoning_xhigh`: "Extra High" / "超高"

## 功能特性

1. **条件显示**: 思考强度选择器仅在使用 Codex 模型时显示
2. **五个级别**: 
   - Auto（默认，不设置 reasoning 参数）
   - Low（低强度思考）
   - Medium（中等强度思考）
   - High（高强度思考）
   - Extra High（超高强度思考）
3. **桌面端**: 使用 NativeSelect 显示完整文本
4. **移动端**: 使用 ActionIcon 显示字母缩写（A/L/M/H/X）
5. **实时更新**: 修改后立即生效，无需刷新页面
6. **样式一致**: 与模型选择器和权限模式选择器保持一致的 UI 风格

## API 参数映射

根据 Codex 模型版本，支持的思考强度级别：
- **Codex 5.2 和 5.3**: low, medium, high, xhigh
- **其他 Codex 版本**: low, medium, high

当前实现支持所有四个级别，由 API 自动处理版本兼容性。

## 使用方式

1. 创建或打开一个使用 Codex 模型的叙述者会话
2. 在顶部工具栏中找到思考强度选择器（权限模式选择器旁边）
3. 点击选择器，从下拉菜单中选择所需的思考强度
4. 下次发送消息时，模型将使用选定的思考强度

## 技术细节

- **数据库迁移**: 下次启动项目时自动执行
- **代码质量**: 已通过 Biome 代码检查和 TypeScript 类型检查
- **向后兼容**: 现有叙述者的 `reasoningEffort` 字段为 NULL，表示使用默认（自动）模式
