# Codex 凭据管理体验补齐计划

## 概述

本计划旨在将 Codex 凭据管理模块的用户体验提升至与 Kiro 凭据管理同等水平。通过对比分析，识别出 10 项关键体验缺失，按优先级分为 P0（核心功能）、P1（重要体验）、P2（优化增强）三个层级，逐步实施。

---

## 优先级分级标准

- **P0（核心功能）**：影响基本可用性、信息完整性或多账号管理效率的关键缺失
- **P1（重要体验）**：显著提升日常使用便利性、降低操作成本的改进
- **P2（优化增强）**：锦上添花的细节优化，提升专业感和一致性

---

## P0 任务清单（核心功能补齐）

### P0-1：自动用量拉取 + 首屏信息完整性

**问题**：Codex 需要手动逐个点击"查询用量"，首屏信息不完整，无法快速判断账号可用性。

**目标**：参考 Kiro `/status` 的自动拉取机制，对未查询过的凭据在后台自动获取用量。

**实现方案**：

1. **后端改动**（`server/routes/codex.ts`）
   - 在 `GET /api/codex/status` 中增加自动拉取逻辑
   - 维护 `Set<string>` 追踪正在拉取的凭据 ID，避免重复请求
   - 对 `usage` 为空且未 disabled 的凭据，异步调用 `manager.getUsage(id)`
   - 设置 30 秒超时，失败静默处理（不阻塞响应）

2. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 保持现有 30 秒轮询 `/status` 的逻辑
   - 用量数据会在后台拉取完成后自动出现在下次轮询中

**验收标准**：
- 新添加的凭据在 30 秒内自动显示用量信息
- 不影响 `/status` 接口响应速度（异步拉取）
- 失败凭据不会阻塞其他凭据的展示

---

### P0-2：移动端友好布局（响应式卡片视图）

**问题**：Codex 仅有桌面表格布局，手机端可读性和可操作性极差。

**目标**：参考 Kiro 的 `CredentialList` 组件，实现移动端卡片 + 桌面表格的双布局。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 提取当前表格代码为 `CredentialTable` 组件
   - 新增 `CredentialCards` 组件（移动端卡片布局）
   - 新增 `CredentialList` 包装组件，使用 `useMediaQuery("(max-width: 768px)")` 切换
   - 卡片布局包含：
     - 顶部：ID + 显示名称 + 状态徽章
     - 中部：账号 ID、优先级、过期时间、成功/失败计数
     - 用量进度条（如有）
     - 底部：操作按钮组（编辑、刷新、查询用量、启用/禁用、重置、删除）

2. **样式调整**
   - 卡片使用 `Paper` 组件，`withBorder` + `p="sm"`
   - 当前活跃凭据高亮边框（`borderColor: "var(--mantine-color-indigo-5)"`）
   - 操作按钮使用 `Group` 横向排列，自动换行

**验收标准**：
- 手机端（<768px）显示卡片布局，桌面端显示表格
- 卡片布局信息完整，操作便捷
- 当前活跃凭据在两种布局中均有视觉高亮

---

### P0-3：当前活跃凭据高亮

**问题**：后端 `snapshot.currentId` 有值，但前端未展示，无法直观看到当前在用哪个账号。

**目标**：在表格和卡片布局中高亮当前活跃凭据。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 在表格行和卡片中判断 `entry.id === status.currentId`
   - 表格行：ID 列字体加粗（`fw={700}`）
   - 卡片：边框高亮（`borderColor: "var(--mantine-color-indigo-5)", borderWidth: 2`）
   - 可选：添加 "Current" 徽章

**验收标准**：
- 当前活跃凭据在视觉上明显区别于其他凭据
- 切换凭据后（如禁用当前凭据），高亮自动更新

---

### P0-4：过期信息可视化

**问题**：`expiresAt` 字段存在但未展示，无法判断 token 是否即将过期。

**目标**：在表格和卡片中显示过期时间，并用颜色标识即将过期的凭据。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 复用 Kiro 的 `relativeTime()` 函数格式化 `expiresAt`
   - 新增 `isExpiringSoon()` 判断逻辑（60 分钟内过期）
   - 表格新增"过期时间"列，卡片在统计行显示
   - 即将过期的凭据用橙色文本标识

2. **样式调整**
   - 已过期：红色文本 + "Expired" 徽章
   - 即将过期（<60min）：橙色文本
   - 正常：灰色文本

**验收标准**：
- 所有凭据显示过期时间（相对时间格式）
- 即将过期和已过期凭据有明显视觉提示
- 过期凭据自动禁用（后端已有逻辑，前端展示即可）

---

## P1 任务清单（重要体验提升）

### P1-1：账号分层视图（按计划类型分组）

**问题**：Codex 凭据平铺展示，账号多时难以管理。

**目标**：参考 Kiro 的 free/paid/enterprise 分组，按 `plan_type` 分组展示。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 从 `usageCache` 中提取每个凭据的 `plan_type`
   - 按 `plan_type` 分组：`free`、`plus`、`team`、`pro`、`unknown`
   - 每组显示：
     - 分组标题徽章（颜色区分：free=gray, plus=blue, team=violet, pro=indigo）
     - 分组描述（i18n）
     - 凭据数量
   - 可折叠分组（默认展开）

2. **i18n 新增**（`frontend/locales/{en,zh-CN}/settings.json`）
   ```json
   "codexPlanFree": "Free",
   "codexPlanPlus": "Plus",
   "codexPlanTeam": "Team",
   "codexPlanPro": "Pro",
   "codexPlanUnknown": "Unknown",
   "codexPlanFreeDesc": "Free tier with limited usage",
   "codexPlanPlusDesc": "ChatGPT Plus subscription",
   "codexPlanTeamDesc": "Team subscription",
   "codexPlanProDesc": "Pro subscription"
   ```

**验收标准**：
- 凭据按计划类型分组展示
- 未查询用量的凭据归入 "Unknown" 组
- 分组可折叠，状态持久化（localStorage）

---

### P1-2：Browser OAuth 流程可视化

**问题**：浏览器登录只是开新窗口 + toast，缺少可视化等待/完成状态。

**目标**：增加 OAuth 流程的视觉反馈，提升用户信心。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 新增 `browserAuthPending` 状态
   - 点击"浏览器添加"后：
     - 显示 Loading 状态（Loader + 文本："等待浏览器授权..."）
     - 开始轮询 `/status`，检测 `entries.length` 是否增加
     - 成功后显示成功通知 + 新凭据高亮动画
     - 60 秒超时自动取消
   - 新增"取消"按钮，调用 `api.codexBrowserAuthCancel()`

2. **后端改动**（`server/routes/codex.ts`）
   - 完善 `POST /api/codex/auth/browser/cancel` 实现
   - 调用 `cancelBrowserOAuth()` 清理 pending 状态

**验收标准**：
- 浏览器登录期间显示等待状态
- 成功后自动刷新列表并高亮新凭据
- 超时或取消后清理状态，不影响后续操作

---

### P1-3：导入后反馈增强

**问题**：导入只返回 `added/duplicates`，后续可用性信息弱。

**目标**：导入后自动拉取用量，并反馈每个凭据的状态。

**实现方案**：

1. **后端改动**（`server/routes/codex.ts`）
   - `POST /api/codex/import` 导入成功后：
     - 对新添加的凭据异步调用 `manager.getUsage(id)`
     - 返回格式增加 `results` 字段：
       ```typescript
       {
         added: number,
         duplicates: number,
         results: Array<{
           id: string,
           displayName?: string,
           usageStatus: 'success' | 'failed' | 'pending',
           error?: string
         }>
       }
       ```

2. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 导入成功后显示详细结果表格
   - 每个凭据显示：ID、显示名称、用量状态（成功/失败/待查询）
   - 失败凭据显示错误原因

**验收标准**：
- 导入后立即显示每个凭据的可用性状态
- 失败凭据有明确错误提示
- 成功凭据自动显示用量信息

---

### P1-4：权限体验一致性（Admin 限制）

**问题**：Kiro 路由显式要求 admin，Codex 路由未加同等限制。

**目标**：统一权限治理，Codex 路由也要求 admin 权限。

**实现方案**：

1. **后端改动**（`server/routes/codex.ts`）
   - 在路由顶部添加：
     ```typescript
     import { requireAdmin, requireAuth } from "../middleware/auth";
     codexRoutes.use("*", requireAuth, requireAdmin);
     ```

2. **前端改动**（`frontend/routes/admin/providers.tsx`）
   - 确保 Codex Section 只在 admin 页面显示
   - 非 admin 用户访问时显示权限提示

**验收标准**：
- 非 admin 用户无法访问 Codex 管理接口
- 前端显示一致的权限提示
- 与 Kiro 权限体验完全一致

---

## P2 任务清单（优化增强）

### P2-1：模型管理能力增强

**问题**：Codex 模型是硬编码列表，无法动态发现新模型。

**目标**：支持从 ChatGPT API 动态获取可用模型列表。

**实现方案**：

1. **后端改动**（`server/lib/codex-manager.ts`）
   - 新增 `listModels(credentialId?: string)` 方法
   - 调用 ChatGPT `/backend-api/models` 接口获取模型列表
   - 缓存到 `~/.narrafork/codex-models-cache.json`

2. **后端改动**（`server/routes/codex.ts`）
   - 新增 `GET /api/codex/models` 返回缓存模型
   - 新增 `POST /api/codex/models/refresh` 刷新模型列表

3. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 模型列表从 API 获取，替代硬编码
   - 新增"刷新模型"按钮
   - 显示模型数量和最后更新时间

**验收标准**：
- 模型列表动态获取，支持手动刷新
- 新模型自动出现在选择器中
- 缓存机制避免频繁请求

---

### P2-2：每凭据代理配置

**问题**：文案写了"可按凭据覆盖代理"，但实际只有全局代理。

**目标**：支持为每个凭据单独配置代理。

**实现方案**：

1. **后端改动**（`server/lib/codex-manager.ts`）
   - `CodexCredential` 接口增加 `proxy?: string` 字段
   - `acquireContext()` 时优先使用凭据级代理，回退到全局代理
   - `updateCredential()` 支持更新 `proxy` 字段

2. **后端改动**（`server/routes/codex.ts`）
   - `PATCH /api/codex/credentials/:id` 支持 `proxy` 字段

3. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 编辑表单增加"代理"输入框
   - 表格/卡片显示凭据级代理（如有）

**验收标准**：
- 每个凭据可单独配置代理
- 凭据级代理优先于全局代理
- 未配置时回退到全局代理

---

### P2-3：负载均衡模式持久化

**问题**：负载均衡模式切换后未持久化到配置文件。

**目标**：确保负载均衡模式在服务器重启后保持。

**实现方案**：

1. **后端改动**（`server/routes/codex.ts`）
   - `POST /api/codex/load-balancing-mode` 已保存到 `settings.codex.loadBalancingMode`
   - 确认 `CodexManager` 构造函数从配置加载初始模式

2. **后端改动**（`server/lib/codex-manager.ts`）
   - 构造函数中从 `settings.codex?.loadBalancingMode` 加载初始值
   - 默认值为 `"priority"`

**验收标准**：
- 负载均衡模式切换后持久化
- 服务器重启后模式保持
- 与 Kiro 行为一致

---

### P2-4：用量查询批量操作

**问题**：需要逐个点击"查询用量"，效率低。

**目标**：支持批量查询所有凭据的用量。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 新增"批量查询用量"按钮（在凭据列表顶部）
   - 点击后并发调用所有凭据的 `api.codexCredentialGetUsage(id)`
   - 显示进度条（已完成/总数）
   - 完成后显示成功/失败统计

2. **样式调整**
   - 批量操作期间禁用单个凭据的"查询用量"按钮
   - 显示 Loading 状态

**验收标准**：
- 一键查询所有凭据用量
- 显示实时进度
- 失败凭据有明确提示

---

### P2-5：凭据排序和筛选

**问题**：凭据多时难以快速定位目标凭据。

**目标**：支持按优先级、状态、用量排序，支持按显示名称/账号 ID 筛选。

**实现方案**：

1. **前端改动**（`frontend/components/providers/CodexSection.tsx`）
   - 新增排序下拉菜单（优先级、状态、用量、最后使用时间）
   - 新增筛选输入框（模糊匹配显示名称/账号 ID）
   - 新增状态筛选（全部/启用/禁用）

2. **状态管理**
   - 排序和筛选条件存储到 localStorage
   - 页面刷新后保持

**验收标准**：
- 支持多维度排序
- 筛选实时生效
- 排序/筛选条件持久化

---

## 实施顺序建议

### 第一阶段（核心功能，1-2 天）
1. P0-1：自动用量拉取
2. P0-3：当前活跃凭据高亮
3. P0-4：过期信息可视化

### 第二阶段（移动端体验，1 天）
4. P0-2：移动端友好布局

### 第三阶段（重要体验，2-3 天）
5. P1-1：账号分层视图
6. P1-2：Browser OAuth 流程可视化
7. P1-3：导入后反馈增强
8. P1-4：权限体验一致性

### 第四阶段（优化增强，按需实施）
9. P2-1：模型管理能力增强
10. P2-2：每凭据代理配置
11. P2-3：负载均衡模式持久化
12. P2-4：用量查询批量操作
13. P2-5：凭据排序和筛选

---

## 技术风险评估

### 低风险
- P0-3、P0-4、P1-4：纯前端展示逻辑，无破坏性
- P2-3：配置加载逻辑，向后兼容

### 中风险
- P0-1：异步拉取可能影响性能，需要超时和错误处理
- P0-2：响应式布局需要充分测试各种屏幕尺寸
- P1-2：OAuth 流程涉及状态管理，需要处理边界情况

### 高风险
- P2-1：动态模型获取依赖 ChatGPT API 稳定性
- P2-2：代理配置涉及网络层，需要充分测试

---

## 测试策略

### 单元测试
- 后端：`CodexManager` 的新增方法（用量拉取、模型获取）
- 前端：排序、筛选、分组逻辑

### 集成测试
- OAuth 流程端到端测试（浏览器 + 设备码）
- 多凭据场景下的负载均衡和故障转移

### 手动测试
- 移动端布局（iOS Safari、Android Chrome）
- 各种屏幕尺寸（手机、平板、桌面）
- 边界情况（无凭据、全部禁用、网络错误）

---

## 国际化支持

所有新增文案需要同步更新：
- `frontend/locales/en/settings.json`
- `frontend/locales/zh-CN/settings.json`

新增 i18n 键（预估）：
- `codexPlan*`：计划类型相关（5 个）
- `codexBrowserAuth*`：OAuth 流程相关（3 个）
- `codexImport*`：导入反馈相关（2 个）
- `codexBatch*`：批量操作相关（3 个）
- `codexSort*`：排序筛选相关（5 个）

---

## 成功指标

### 定量指标
- 首屏信息完整率：从 0% 提升到 90%+（30 秒内）
- 移动端可用性评分：从 2/10 提升到 8/10
- 用量查询效率：从逐个点击提升到一键批量

### 定性指标
- 用户反馈：Codex 管理体验与 Kiro 持平
- 开发者体验：代码结构与 Kiro 保持一致
- 维护成本：新功能易于扩展和调试

---

## 后续优化方向

1. **智能推荐**：根据用量自动调整优先级
2. **健康监控**：凭据健康度评分（成功率、用量、过期时间）
3. **批量导入优化**：支持从文件导入（CSV、JSON）
4. **凭据标签**：支持自定义标签分类
5. **使用统计**：每个凭据的详细使用历史和趋势图

---

## 附录：关键代码位置

### 后端
- 凭据管理器：`server/lib/codex-manager.ts`
- 路由：`server/routes/codex.ts`
- OAuth 认证：`server/lib/codex-auth.ts`
- 用量查询：`server/lib/codex-usage.ts`

### 前端
- Codex Section：`frontend/components/providers/CodexSection.tsx`
- API 客户端：`frontend/lib/api.ts`（L970-L1125）
- i18n：`frontend/locales/{en,zh-CN}/settings.json`

### 参考实现
- Kiro Section：`frontend/components/providers/KiroSection.tsx`
- Kiro Manager：`server/lib/kiro/multi-token-manager.ts`
- Kiro Routes：`server/routes/kiro.ts`
