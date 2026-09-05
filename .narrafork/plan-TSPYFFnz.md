# Codex 多账号凭据管理方案

## 核心思路

将 Codex 从"嵌入在 OpenAIProviderConfig 中的单账号 OAuth"改为独立的凭据池管理模式。Codex 所有账号共享同一组模型（codex 系列），因此不需要 per-provider 绑定，而是作为一个独立的 provider 存在。

## 架构设计

### 存储

文件：`~/.narrafork/codex-credentials.json`

```ts
interface CodexCredential {
  id: string;                    // nanoid 8字符
  displayName?: string;          // 用户自定义名称
  refreshToken: string;          // OAuth refresh token
  accessToken?: string;          // 短期 access token
  expiresAt?: number;            // access token 过期时间戳 (ms)
  accountId?: string;            // ChatGPT account ID (from JWT)
  email?: string;                // 从 JWT 提取的邮箱
  priority?: number;             // 优先级，越小越高，默认 0
  disabled?: boolean;            // 手动禁用
  disabledReason?: "manual" | "too_many_failures" | "quota_exhausted";
  proxy?: string;                // 该凭据专用代理 URL
}
```

运行时统计（内存 + `~/.narrafork/codex-stats.json`）：

```ts
interface CodexCredentialStats {
  [id: string]: {
    successCount: number;
    failureCount: number;
    lastUsedAt?: string;
  };
}
```

### 新文件

| 文件 | 职责 |
|------|------|
| `server/lib/codex-manager.ts` | 凭据池管理器：加载/保存/选择/轮换/刷新/失败处理 |
| `server/routes/codex.ts` | 重写：凭据 CRUD + OAuth 流程 + 状态查询 |
| `frontend/components/providers/CodexSection.tsx` | 重写：凭据管理 UI（参考既有 provider Section） |

### 修改文件

| 文件 | 改动 |
|------|------|
| `server/lib/settings/index.ts` | 新增 `codex?: { proxy?: string; loadBalancingMode?: "priority" \| "balanced" }` 顶层配置；从 `OpenAIProviderConfig` 移除 codex 相关字段 |
| `server/lib/codex-auth.ts` | 保留 OAuth 流程函数，不变 |
| `server/lib/agent/openai-provider.ts` | 移除 codex OAuth 逻辑；codex 模式改为从 codex-manager 获取凭据 |
| `server/lib/agent/provider.ts` | `getProvider("codex")` 返回专用的 codex provider |
| `server/lib/settings/index.ts` | `resolveProvider` 识别 `codex_*` 前缀 |
| `server/app.ts` | 路由不变（已注册 `/api/codex`） |
| `frontend/routes/admin/providers.tsx` | 添加 CodexSection |
| `frontend/lib/api.ts` | 更新 codex API 方法 |
| `frontend/components/providers/OpenAIProvidersSection.tsx` | 移除 codex 相关 UI |
| `frontend/components/providers/types.ts` | 移除 OpenAIProviderState 的 codex 字段 |
| `frontend/locales/{en,zh-CN}/settings.json` | 更新翻译 |

### CodexManager 设计

```ts
class CodexManager {
  private credentials: CodexCredential[];
  private stats: Map<string, Stats>;
  private currentIdx: number;
  private mode: "priority" | "balanced";

  // 凭据选择（同多凭据池逻辑）
  selectCredential(): CodexCredential;
  
  // Token 管理
  async ensureValidToken(cred: CodexCredential): Promise<string>;
  
  // 失败处理
  reportSuccess(id: string): void;
  reportFailure(id: string): void;  // 连续3次失败自动禁用
  
  // CRUD
  addCredential(oauth: CodexTokens): CodexCredential;
  removeCredential(id: string): void;
  disableCredential(id: string): void;
  enableCredential(id: string): void;
  
  // 快照
  getSnapshot(): CodexManagerSnapshot;
}
```

### Provider 集成

`getProvider("codex")` → 返回一个特殊的 `OpenAIProvider`，其 config 动态从 `CodexManager.selectCredential()` 获取。每次 `chat()`/`generate()` 调用时：

1. `selectCredential()` 选择一个可用凭据
2. `ensureValidToken(cred)` 确保 token 有效（自动刷新）
3. 构建 headers（Bearer token + ChatGPT-Account-Id）
4. 请求失败时 `reportFailure()`，成功时 `reportSuccess()`

### API 路由

| 方法 | 路径 | 功能 |
|------|------|------|
| GET | `/api/codex/status` | 返回凭据快照 + 统计 |
| POST | `/api/codex/auth/browser` | 浏览器 OAuth → 自动添加凭据 |
| POST | `/api/codex/auth/browser/wait` | 浏览器 OAuth（长轮询） |
| POST | `/api/codex/auth/browser/cancel` | 取消浏览器 OAuth |
| POST | `/api/codex/auth/device/start` | 设备码流程开始 |
| POST | `/api/codex/auth/device/poll` | 设备码轮询 |
| POST | `/api/codex/auth/device/cancel` | 取消设备码 |
| POST | `/api/codex/credentials/:id/disable` | 禁用 |
| POST | `/api/codex/credentials/:id/enable` | 启用 |
| POST | `/api/codex/credentials/:id/reset` | 重置失败计数 |
| DELETE | `/api/codex/credentials/:id` | 删除 |
| PATCH | `/api/codex/credentials/:id` | 更新 displayName/priority/proxy |
| POST | `/api/codex/credentials/:id/refresh` | 手动刷新 token |
| POST | `/api/codex/load-balancing-mode` | 切换模式 |
| POST | `/api/codex/logout` | 删除凭据（兼容旧 API） |

### 前端 CodexSection

参考既有 provider Section 的结构：
- 状态概览：凭据总数/可用数 + 负载均衡模式切换
- 添加凭据：两个按钮（浏览器登录 / 设备码登录）
- 凭据列表：每行显示 displayName、accountId、状态、优先级、成功/失败计数
- 操作：启用/禁用、刷新 token、编辑、重置、删除
- 每个凭据可配置独立代理

### 模型处理

Codex 模型列表是固定的（不需要从 API 获取）：
```ts
const CODEX_MODELS = [
  "gpt-5.3-codex",
  "gpt-5.2-codex", 
  "gpt-5.1-codex",
  "gpt-5.1-codex-mini",
  // ... 随 OpenAI 更新手动维护
];
```

注册到 settings 的模型系统，前缀为 `codex_cx:`（与普通 openai provider 区分）。

### 迁移

从旧的 per-provider codexOAuth 迁移：
- `loadSettings()` 中检测 `openaiProviders[].codexOAuth`
- 自动提取到 `codex-credentials.json`
- 清理 provider 上的 codex 字段

## 实施顺序

1. `server/lib/codex-manager.ts` — 凭据池管理器
2. 修改 `server/lib/settings/index.ts` — 新增顶层 codex 配置，迁移逻辑
3. 重写 `server/routes/codex.ts` — 新 API
4. 修改 `server/lib/agent/openai-provider.ts` — 移除内嵌 codex OAuth
5. 修改 `server/lib/agent/provider.ts` — codex provider 解析
6. 清理 `server/routes/settings.ts` — 移除 codexOAuth schema
7. 前端：`CodexSection.tsx` + API 更新 + i18n
8. 清理 `OpenAIProvidersSection.tsx` — 移除 codex UI
9. Biome + TypeScript 检查
