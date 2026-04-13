# Token 估算功能 - 通用实现

## 概述

某些 AI 提供商（如 Kiro、NKP 等）不在 API 响应中返回详细的 token 使用统计。为了在请求历史中提供一致的使用数据，我们为所有没有返回 usage 数据的提供商实现了基于文本长度的 token 估算功能。

## 实现位置

### 1. Token 估算函数

位置：`server/lib/agent/estimate-tokens.ts`

该函数使用字符级启发式方法估算 token 数量：
- ASCII/拉丁字符：约 0.3 tokens/字符
- CJK/宽字符：约 0.6 tokens/字符

这个估算是保守的（略微高估），以确保不会超出预算。

### 2. Agent Loop 集成

位置：`server/lib/agent/loop.ts` (第 1241-1258 行)

在 `api_request_end` 事件发出之前，检查是否已有 usage 数据：
- 如果 `requestUsage` 为空（任何提供商）
- 则估算输入和输出 token 数量：
  - **输入 tokens**: history + system prompt + 当前用户消息
  - **输出 tokens**: assistant 响应文本

```typescript
// ── Estimate token usage when provider doesn't report it ──
// Some providers (e.g., Kiro, NKP) don't report token usage in their API responses.
// For these cases, we estimate based on text length to provide usage statistics.
if (!requestUsage) {
	const historyText = JSON.stringify(history);
	const systemText = config.systemPrompt ?? "";
	const estimatedInputTokens =
		estimateTokens(historyText) + estimateTokens(systemText) + estimateTokens(content);
	const estimatedOutputTokens = estimateTokens(assistantText);

	requestUsage = {
		inputTokens: estimatedInputTokens,
		promptTokens: estimatedInputTokens,
		completionTokens: estimatedOutputTokens,
	};
}
```

### 3. 数据库存储

位置：`server/services/narrator-event-handler.ts` (第 1316-1374 行)

`api_request_end` 事件处理器将估算的 token 数据保存到 `api_requests` 表：
- `inputTokens`: 输入 token 数量
- `outputTokens`: 输出 token 数量
- `cachedInputTokens`: 缓存输入 token（Kiro/NKP 为 0）
- `cacheCreationInputTokens`: 缓存创建 token（Kiro/NKP 为 0）
- 其他缓存相关字段均为 0

### 4. 前端显示

位置：`frontend/components/usage-history/UsageHistoryTable.tsx`

前端表格正确显示所有提供商的 token 使用情况，包括估算的数据。对于不返回 usage 的提供商：
- Token 列显示估算的输入和输出 token
- 成本列根据提供商类型显示相应数据（metering 或美元成本）

## 数据流

1. **Agent Loop** → 检查是否有 usage 数据，如果没有则估算 token 使用量
2. **api_request_end 事件** → 包含实际或估算的 usage 数据
3. **Event Handler** → 保存到 `api_requests` 表
4. **Usage History API** → 查询并返回记录
5. **前端表格** → 显示 token 统计

## 适用场景

此功能自动应用于所有不返回 usage 数据的提供商，包括但不限于：
- Kiro
- NKP (所有前缀)
- 其他未来可能添加的不返回 usage 的提供商

对于返回精确 usage 数据的提供商（如 Anthropic、OpenAI），继续使用 API 返回的数据。

## 注意事项

1. **估算精度**：估算值是近似的，可能与实际 token 使用量有偏差（通常略高）
2. **自动应用**：只要提供商不返回 usage 数据，就会自动使用估算
3. **精确数据优先**：如果提供商返回了 usage 数据，则使用精确数据而非估算
4. **成本计算**：某些提供商（如 Kiro、NKP）使用 metering（credits）而非基于 token 的成本
5. **缓存支持**：估算不包括缓存相关字段，所有缓存字段为 0
6. **向后兼容**：现有的 Anthropic 和 OpenAI 请求不受影响，继续使用 API 返回的精确 token 数据

## 测试

可以通过以下方式验证功能：

1. 使用任何不返回 usage 数据的提供商（如 Kiro 或 NKP）创建一个叙述者会话
2. 发送一些消息
3. 访问 `/admin/usage-history` 页面
4. 验证 token 列显示非零值
5. 检查 tooltip 显示输入和输出 token 的详细信息

## 未来改进

1. 使用更精确的 tokenizer（如 tiktoken）进行估算
2. 根据不同模型调整估算系数
3. 添加估算标记（在 UI 中显示"估算值"标签）
4. 支持更复杂的估算算法（考虑特殊 token、格式化等）
