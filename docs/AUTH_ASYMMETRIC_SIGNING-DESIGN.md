# 会话令牌非对称签名迁移：调研与设计

> 状态：设计稿（未实现）。范围：`settings.auth.jwtSecret` 的全部用途，以及把会话/MFA JWT 从 HS256 迁到非对称签名（EdDSA）。

## 1. 结论先行

1. **HS256 本身不弱。** `jwtSecret` 是 `randomBytes(32)` 生成的 256 位随机值，验证端也固定了 `alg: "HS256"`，不存在算法混淆或暴力破解问题。真正的问题在**密钥管理**，而不是算法。
2. **现状的风险点**（按严重程度）：
   - **一把密钥承担四种职责**：会话 JWT、MFA 挑战 JWT、登录限流指纹 HMAC、评审边界规则 HMAC。任何一处泄露都等于全部泄露，而且无法单独轮换。
   - **私钥放在 `settings.json` 里。** 这个文件会被整体读写、深合并、做快照，管理员工具和设置 API 也会碰到它；目前只靠多处“记得打码”来保护（`maskAuthSettings`、`narrafork-admin` 的 `***MASKED***`、`fs-secret-paths`、`settings.ts` 的排除逻辑）。只要以后漏掉一处，就会泄露。
   - **没有 `kid`，也没有轮换机制。** 换密钥只能改 `settings.json` 后重启，所有人同时掉线，而且会顺带打断评审边界 HMAC（见 §2.3）。
   - **只要能验证就能签发。** 任何需要校验会话的组件（脚本、将来的多节点、插件宿主、外部验证方）都必须拿到可以伪造任意用户（包括 admin）令牌的同一把密钥。
3. **非对称签名能解决：** 验证端和签发端分权、按 `kid` 平滑轮换、密钥从 `settings.json` 中独立出来、并为 JWKS 和外部验证留出接口。
4. **非对称签名解决不了：** 能读到私钥文件的进程依然可以伪造令牌。叙述者的 Bash 工具和服务进程使用同一个 OS 用户，`fs-secret-paths` 只能拦截文件工具和 `/api/fs` 路由，拦不住 `cat ~/.narrafork/...`。这个问题要靠密钥存放位置来缓解（§6），换算法本身不能消除它。
5. **建议方案：** 先把四种用途拆开（阶段 0，这一步收益最大），再引入 Ed25519 密钥集，验证期间同时接受 HS256 和 EdDSA（阶段 1–2）。借助现有的滑动续期，大约 7 天后自然淘汰 HS256（阶段 3）。

## 2. 现状盘点

### 2.1 `jwtSecret` 的生成与存放

- `server/lib/settings/index.ts:365`：首次启动时如果为空，就用 `randomBytes(32).toString("hex")` 生成并写回 `~/.narrafork/settings.json`。
- `server/lib/settings/types.ts:904` / `defaults.ts:144` 中定义了这个字段；`defaults.ts:1041` 把它标记为内部字段，不进入设置文档。

### 2.2 签名 / 验签调用点（全部使用 `hono/jwt`，HS256）

| 位置 | 用途 |
|------|------|
| `server/lib/auth.ts` `signSessionToken` / `verifyToken` / `isAuthenticSessionTokenIgnoringExpiry` | 会话 JWT（7 天 TTL，滑动续期，30 天绝对上限，`sv` 版本号吊销） |
| `server/lib/mfa.ts` `issueMfaToken` / `verifyMfaToken` | MFA 挑战令牌（5 分钟，`stage: "mfa_pending"`，jti 一次性） |
| `server/middleware/auth.ts` | 调用 `verifyToken`；依赖 `hono` 的 `JwtTokenExpired` 区分“过期”与“伪造” |
| `server/main.ts:829, 1072` | WebSocket `?token=` 验证 |
| `server/routes/oauth.ts:63` | 授权同意页通过会话 JWT 识别用户（按“三段式 JWS”的形状和 OAuth 不透明令牌区分） |
| `server/scripts/approve-all.ts` | 运维脚本**直接读取 settings.json 里的 jwtSecret**，自行签发 5 分钟的 admin 令牌 |

### 2.3 非 JWT 的派生用途（迁移时最容易漏掉）

| 位置 | 用途 | 换密钥的影响 |
|------|------|--------------|
| `server/lib/auth-attempt-limiter.ts:419` `fingerprintAuthIdentifier` | 用 HMAC 处理登录标识后，作为限流桶的 key | 限流器在内存中，换密钥只会让计数归零 |
| `server/services/narrator-review-boundary.ts:25` `boundaryDigest` | 评审边界规则 ID `review-boundary:v1:<nonce>:<hmac>`，**持久化在规则行里**，重启后还要靠它验证 | **直接换密钥会让已有的评审边界全部失效**，必须保留 v1 验证路径 |

对照参考：`server/services/gen-ui/kit-signature.ts` 已经采用“独立密钥文件 `~/.narrafork/gen-ui-kit-signing.key`，权限 0600，用 `wx` 原子创建，竞争失败时读取胜出方的密钥”。本设计沿用这个模式。

### 2.4 测试对 `jwtSecret` 的直接依赖

共 14 个文件直接用 `settings.auth.jwtSecret` 自行签名，例如 `server/routes/__tests__/oauth*.test.ts`、`external-v1.test.ts`、`server/lib/__tests__/session-token.test.ts`、`server/middleware/__tests__/session-renewal.integration.test.ts`、`tests/server/services/auth/pg-auth-loop.test.ts`。`tests/preload.ts` 的快照恢复逻辑也假设密钥在 `settings` 树里。

### 2.5 依赖

- `jose@6.1.3` 已经在依赖树中（经 `@modelcontextprotocol/sdk` 传递引入），`server/lib/oidc.ts` 也已在用它的 `jwtVerify` / `createRemoteJWKSet`，但 **`package.json` 没有直接声明**。迁移时应当显式加入 `jose`。
- `hono/jwt` 也支持 `EdDSA` / `ES256`，但不支持多 key 选择、`typ` / `aud` 校验，而且会先检查 `exp` 再验签（所以才需要 `isAuthenticSessionTokenIgnoringExpiry` 绕一圈）。`jose` 会先验签再校验声明，正好去掉这个绕路。

## 3. 算法选型

在 Bun 1.4.3 / WebCrypto 上测了 5000 次的平均值（µs/次）：

| 算法 | sign | verify | 签名长度 | 备注 |
|------|------|--------|----------|------|
| HS256 | 80 | 64 | 32B | 现状；数值主要是 WebCrypto 异步调用的开销 |
| **EdDSA (Ed25519)** | 85 | 160 | 64B | 确定性签名，不依赖随机数质量；key 只有 32B |
| ES256 (P-256) | 145 | 200 | 64B | 兼容性最广，签名依赖随机数 |

**推荐 EdDSA (Ed25519)。** 每次请求只多出约 0.1 ms 的验签，远低于主线程规则需要关注的量级。所有验证方都在我们自己的代码（jose / WebCrypto）里，不需要考虑老旧 JWT 库的兼容性。将来如果要对外发布 JWKS 给不支持 EdDSA 的第三方，密钥集格式本身允许再加一把 ES256 key（按 `kid` 区分），无需改动设计。

不选 RSA：签名大、私钥大，性能也差，在这里没有任何优势。

## 4. 目标设计

### 4.1 用途拆分（阶段 0，和换算法无关，应当先做）

| 用途 | 新密钥 | 形式 |
|------|--------|------|
| 会话 JWT、MFA JWT | 签名密钥集（§4.2） | Ed25519，按 `kid` |
| 限流指纹 | `~/.narrafork/keys/instance-hmac.key` 用 HKDF 派生，`info="narrafork/auth-limiter/v1"` | HMAC-SHA256 |
| 评审边界 | 同一个文件用 HKDF 派生，`info="narrafork/review-boundary/v2"` | HMAC-SHA256，规则 ID 前缀改为 `v2` |

- `instance-hmac.key` 复用 `kit-signature.ts` 的加载逻辑（32 字节、0600、`wx` 创建、处理竞争）。建议把这段逻辑抽成通用的 `server/lib/instance-keys.ts`，GenUI 也改用它。
- 评审边界：新签发一律使用 `v2`；`isSignedReviewBoundaryRow` 在过渡期继续接受 `v1`（仍用 `jwtSecret` 验证）。阶段 3 删除 `jwtSecret` 前，用一次性后台 job 把仍然有效的 `v1` 行重签为 `v2`，之后再移除 `v1` 分支。

### 4.2 签名密钥集

存放：`~/.narrafork/keys/session-signing.jwks.json`（目录 0700，文件 0600），先写临时文件再 `rename` 原子替换。

```jsonc
{
  "version": 1,
  "keys": [
    {
      "kid": "2026-10-21-k7Hq2",       // 日期 + generateShortId()
      "kty": "OKP", "crv": "Ed25519", "alg": "EdDSA",
      "x": "<公钥 base64url>", "d": "<私钥 base64url>",
      "status": "active",               // active | retiring
      "createdAt": 1792540800,
      "retireAfter": null               // retiring 时 = 降级时刻 + 会话 TTL + 时钟容差
    }
  ]
}
```

- 任何时刻**只有一把 `active`** key 用于签发；`active` 和 `retiring` 的 key 都可以用来验签。
- 启动时没有这个文件就自动生成一把 `active` key（和现在自动生成 `jwtSecret` 的体验一致）。
- 进程内缓存 `CryptoKey`（`importJWK`），文件变化时用 `hotSafe` 重新加载；**不进入 `settings` 单例**，也就不会被设置 API、深合并、快照或 `narrafork-admin` 工具碰到。
- 加入 `fs-secret-paths.ts` 的 `SECRET_HOME_FILES`（`keys/` 整个目录），同时更新 `settings.json` 那一行的注释。
- 部署覆盖：环境变量 `NARRAFORK_SESSION_KEYS_FILE` 可以指向挂载进来的 secret 文件（容器 / 多节点场景）；指定后改为只读，不再自动生成和轮换。

### 4.3 令牌格式

```
header:  { "alg": "EdDSA", "typ": "nf-session+jwt", "kid": "<kid>" }
payload: { sub, role, iat, exp, sst, sv, iss: "<instanceId>", aud: "narrafork" }
```

- MFA 令牌使用 `typ: "nf-mfa+jwt"`，`aud: "narrafork/mfa"`。**验签时强制校验 `typ` 和 `aud`**，用类型分离替代现在“检查 `stage` 字段是否存在”这种约定式防护，两种令牌就不可能互相顶替。
- `iss` 使用实例 ID（如果已有就复用，没有就首次生成后存到 `keys/instance.json`），防止两个实例错误共用 key 时令牌被跨实例使用。
- 验证固定使用 `algorithms: ["EdDSA"]`；如果 `kid` 不在密钥集里，直接按 401 `UNAUTHORIZED` 处理，不尝试其他 key。

### 4.4 验证流程（新的 `server/lib/session-keys.ts` + 改写 `auth.ts`）

```
parse header (不信任, 仅用于分派)
├─ alg=EdDSA 且 kid ∈ keyset  → jose.jwtVerify(token, key, {algorithms:["EdDSA"], typ, aud, iss})
├─ alg=HS256 且 legacyHs256Until > now → hono verify(token, jwtSecret, "HS256")   // 仅过渡期
└─ 其他                       → 401
```

- 改用 jose 之后，过期错误变成 `JWTExpired`（`code: "ERR_JWT_EXPIRED"`）。因为 jose **先验签**，抛出 `JWTExpired` 就说明令牌是真的，`middleware/auth.ts` 中 `isExpiredSessionJwt` 的二次验签对 EdDSA 路径可以去掉。HS256 过渡路径保留现有逻辑，并在阶段 3 一起删除（`reportsExpiredJwt` 上方那段关于 jose 的注释也要同步更新）。
- `oauth.ts` 按 JWS 形状区分令牌的逻辑不变。
- `assertSessionPayload` 保留（`sub` / `exp` 必须存在）。

### 4.5 轮换

- **计划轮换**：`bun run auth:rotate-key`（或者给 admin 用的、需要确认的 API）生成新 key 设为 `active`，旧 key 改为 `retiring`，`retireAfter = now + SESSION_TOKEN_TTL_SECONDS + 300`。到期后由后台清理删除。用户无感：未过期的令牌仍然可以验证，进入续期窗口后会自然换成新 key 签发。
- **紧急轮换（怀疑私钥泄露）**：`--revoke-previous` 立即删除旧 key，所有会话失效，用户需要重新登录。如果只怀疑个别账户，继续使用现有的 `revokeUserSessions`（`token_version` +1）即可，不必换 key。
- 轮换和清理都要写审计日志（只记录 kid，不记录密钥材料）。

### 4.6 可选：JWKS 公开端点

`GET /api/auth/jwks.json` 只返回公钥（`kty/crv/x/kid/alg`），**默认关闭**（`settings.auth.publishJwks = false`）。只有出现外部验证方时（独立网关、插件宿主进程、将来的多节点只读副本）才打开。现在没有使用方，不必提前暴露。

## 5. 迁移计划

| 阶段 | 内容 | 用户可见影响 | 回滚 |
|------|------|--------------|------|
| 0 | 新增 `instance-keys.ts`；限流指纹和评审边界改为派生 key（边界 v2 签发 + v1 兼容验证）；显式添加 `jose` 依赖 | 无（限流计数在重启时本来就会归零） | 直接回退代码 |
| 1 | 新增密钥集与验证分派；**签发仍用 HS256**，同时接受 EdDSA | 无 | 直接回退代码 |
| 2 | 签发切换为 EdDSA；记录 `legacyHs256Until = 部署时刻 + TTL + 容差`（写入密钥集文件） | 无：旧 HS256 令牌在 TTL 内仍有效，进入续期窗口时就换发 EdDSA | 回到阶段 1：已发的 EdDSA 令牌仍然能验证 |
| 3 | 过了 `legacyHs256Until` 后，删除 HS256 分支；后台 job 把 v1 评审边界重签为 v2，然后删除 v1 分支；在 `stripObsoleteSettingsKeys` 中移除 `auth.jwtSecret`，同时删除 `maskAuthSettings` 和 `narrafork-admin` 中的打码分支 | 7 天以上没有活动、令牌已过期的用户本来就要重新登录，没有新增影响 | 不可逆，需单独发版 |

阶段 1 和 2 必须分两个版本发布，这样阶段 2 出问题时回退到阶段 1 不会让已发的 EdDSA 令牌失效。

**配套改动：**

- `server/scripts/approve-all.ts`：不再自行读取 settings.json 签名，改为 import `createToken`（或者读取密钥集文件中的 active key）。
- 测试：新增 `tests/helpers/session-token.ts`，提供 `signTestSessionToken(payload, { alg?, kid?, expired? })`，替换 14 处 `sign(..., settings.auth.jwtSecret)`；`tests/preload.ts` 为每个测试进程生成临时密钥集目录。HS256 兼容路径要有专门测试：过渡期内可用、过期后拒绝、伪造的过期令牌不能被识别为 `TOKEN_EXPIRED`。
- 必须新增的回归用例：`typ` 互换（MFA 令牌当会话令牌用）被拒；未知 `kid` 被拒；`alg: none`、`alg: HS256` 但用公钥当 HMAC key（经典算法混淆）被拒；retiring key 签的令牌仍可用，删除后被拒。
- 文档：`docs/OPEN_API.md` 中提到会话 JWT 的段落、`CLAUDE.md` 认证一节补充“密钥集位于 `~/.narrafork/keys/`”。

## 6. 私钥本身的保护（非对称签名之外的问题）

迁移完成后，“能读私钥文件 = 能伪造任意 admin 会话”这件事依然成立。按投入从小到大排列的缓解措施：

1. **（本设计已包含）** 把私钥移出 `settings.json`，缩小意外泄露面（设置 API、备份、日志、管理员工具输出）。
2. **Bash 工具层面**：执行策略 / 目录黑名单默认把 `~/.narrafork/keys/` 设为 host 级 deny（`execution-policy` 已有目录黑名单机制）。这只能防住大多数误读，同一 OS 用户下无法做到完全隔离。
3. **可选的口令加密**：设置了 `NARRAFORK_KEY_PASSPHRASE` 时，密钥集文件中的 `d` 用 scrypt 加 AES-GCM 加密，口令只存在于服务进程的环境变量里。对 agent 读文件有效，但 agent 读 `/proc/<pid>/environ` 仍然能拿到口令。
4. **根本隔离**：服务进程和 agent 执行分属不同 OS 用户 / 容器（podman 后端），私钥文件只对服务用户可读；或者由 OS keychain / KMS 托管。这是部署形态层面的工作，超出本设计范围，在这里记录下来作为后续方向。

## 7. 顺带发现（不在本次范围内）

- WebSocket 鉴权通过 `?token=` 传完整会话 JWT，令牌可能出现在反向代理的访问日志里。External API 已经有 WebSocket ticket 机制（`docs/OPEN_API.md`），第一方 WS 也可以改用一次性短期 ticket。
- `narrator-worktree-service.ts` 的列表游标 HMAC 使用进程内 `randomUUID()`，重启后游标失效，这是有意为之，和本设计无关，保持不变。

## 8. 待决问题

1. 是否需要多节点 / PG 部署共享签名密钥？如果需要，首选 `NARRAFORK_SESSION_KEYS_FILE` 挂载；不建议把私钥存进数据库。
2. 计划轮换是否需要自动化（例如每 90 天），还是只提供手动命令？建议先只提供手动命令。
3. JWKS 端点是否需要在阶段 2 就提供给 VS Code 插件或插件宿主做本地验签？目前没有发现这样的使用方。
