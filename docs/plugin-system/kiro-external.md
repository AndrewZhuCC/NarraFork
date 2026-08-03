# kiro-external：Kiro 作为外部插件

## 定位

`examples/plugins/kiro-external/` 把 Kiro 实现为一个进程外的供应商插件，**与内置实现并存**，不替代它。

内置实现（`server/lib/kiro/`、`server/lib/kiro-adapter.ts`、`frontend/components/providers/KiroSection.tsx`）**一行未改**，只新增了一个 guard 测试文件。

| | 内置 | kiro-external |
|---|---|---|
| provider prefix | `kiro` | `kiro-ext` |
| 凭据存储 | `~/.narrafork/kiro/credentials.json` | 宿主 secret vault（`provider.kiro.credentialsBundle`） |
| 多令牌轮换 | 有（`multi-token-manager` 全功能） | **有**（同一模块，凭据以 bundle 形式存储） |
| 凭据 UI | 宿主 React 组件（1468 行） | 插件自己的 iframe（原生 DOM） |
| 运行位置 | 宿主进程内 | 独立子进程 |

## 功能对等矩阵

内置前端调用 15 个端点，本插件覆盖 11 个：

| 插件 command | 覆盖的内置端点 |
|---|---|
| `status` | `GET /status` |
| `credentials.import` | `POST /credentials/import` |
| `credentials.setState` | `/credentials/:id/disable`、`/enable`、`/reset`（3 个） |
| `credentials.remove` | `DELETE /credentials/:id` |
| `credentials.setPriority` | `POST /credentials/:id/priority` |
| `credentials.refresh` | `POST /credentials/:id/refresh` |
| `credentials.update` | `PATCH /credentials/:id` |
| `credentials.usage` | `GET /credentials/:id/usage` |
| `config.setLoadBalancing` | `POST /load-balancing-mode` |
| （`provider.listModels`） | `GET /models`、`POST /models/refresh` |

**不做的 4 个，及理由**：

- `GET /mcp-tools`、`POST /mcp-search` —— Kiro 自带 MCP 客户端（`mcp-client.ts`，298 行）。它与宿主
  MCP manager 的归属是独立问题，本轮不做。
- `POST /chat` —— 内置的调试端点；插件对话走 `provider.chat`，无需对等。
- `POST /global-proxy` —— 写的是**宿主全局** `settings.proxy`。`07-security-and-sandbox.md` §6.2 明文
  禁止插件覆盖宿主代理策略，插件继承宿主代理即可。

**为什么凭据不共用一个文件**：两个 token manager 并发刷新同一条凭据会互相覆盖 refresh token，这是真实的数据丢失，不是洁癖。并存也天然提供 A/B 对照——同一账号可在两条路径上比对行为。

## 已验证（自动化）

- `server/lib/kiro/plugin-bundle.guard.test.ts` —— 6 项，钉住可打包边界
- `tests/server/integration/plugins/kiro-external-event-mapping.test.ts` —— 23 项，事件映射逐条对宿主真实 `providerStreamEventSchema` 校验
- `bun run plugins:check-artifacts` —— 产物与源码一致性
- `bun test tests/server/` —— 1103 pass / 0 fail（基线 1080）

## 已验证（真实上游，手工脚本）

`bun scripts/verify-kiro-plugin-bundle.ts --live`：

- 客户端子集在**无 NarraFork home 的独立子进程**内可加载运行
- 真实 `ListAvailableModels` 成功
- token 刷新、代理层、请求签名在宿主缺席时全部工作

另经真实 `PluginRuntime` 子进程验证：宿主注入凭据 → 插件 → 上游，返回 8 个模型（含 `contextWindow`）。

## 需要你手工验证

我无法自证这些——它们需要重启 NarraFork 进程，而项目铁律禁止我动承载当前会话的进程。

1. 设置页出现 `Kiro (External)` 供应商
2. 凭据页列出全部凭据，且**不显示任何明文**（只有「已配置/未设置」与 id、优先级、状态）——该视图
   不调用 `secrets.get`，有测试断言产物里不含这个方法名
3. 粘贴凭据导入 → 列表出现新条目，输入框自动清空
4. 启用/禁用、改优先级、Reset、Refresh token、删除各自生效且刷新后保持
5. 「Usage」返回配额数字；负载均衡在 `priority`/`balanced` 间切换
6. `kiro-ext:claude-sonnet-4.5` 出现在模型选择器
7. 完成一次真实对话（多凭据时观察是否按优先级选择）
8. **对话中 credit 消耗与上下文占用百分比在前端可见**
9. 重启后凭据与各项状态仍生效
10. 内置 `kiro` 供应商行为不变

## 关键设计点

### Kiro 报 credit，不报 token

上游发 `meteringEvent`（credit 消耗）与 `contextUsageEvent`（上下文占用百分比），不发 token 数。两者映射到契约既有字段 `metering{unit,unitPlural,usage}` 与 `contextUsagePercentage`——**没有新增任何自定义事件类型**（契约每个事件 schema 都是 `.strict()`，新增会被拒）。

**token 字段留空，绝不填 0。** 宿主会把 0 当作真实测量：报告零成本请求，并按「prompt 为空」计算上下文余量。留空则宿主回落到百分比，这是此处唯一诚实的信号。有测试专门钉这一点。

### 复用而非复制

插件打包 `server/lib/kiro/` 的客户端子集 + `shared/codewhisperer/`。

**`server/lib/kiro/index.ts` 被排除**：它是内置适配器的门面，会读写宿主 `~/.narrafork/settings.json`。这对内置实现是正确行为，对插件不可接受。guard 测试用 `PLUGIN_BUNDLE_ENTRYPOINTS` 记录允许打包的模块，并断言 `index.ts` 确实仍在写 settings——这样如果它哪天不再写了，测试会失败，豁免可以被有意识地移除，而不是悄悄保护一个不存在的问题。

## 已知差距与风险

**1. 安全契约已为此放宽。** 凭据页要自包含就必须能保存修改，而 iframe 的 CSP 是
`connect-src 'none'`，无法调用宿主 admin 端点。因此新增了 `secretWrites` 返回通道，
`07-security-and-sandbox.md` §13 原本「不能自行持久化凭据」一条已相应修订。

放宽的范围比本文早期版本描述的更大，如实记录（实现见 `server/services/plugin-command-secret-writes.ts`
文件头、以及 `11-capability-policy.md` §3.3–§3.4）：

- **仍在宿主侧强制**：命名空间（key 必须是 `provider.<contributionId>.<field>` 且 contribution 归
  请求插件所有，从 registry 派生而非取自请求）、单值 ≤ 64 KB、错误信息不可区分、值不入日志。
- **已撤销**：`configSchema` 字段白名单、条目数上限、单批总量上限、重复 key 拒绝、**批量原子性**。
  后者有实际代价——写入按顺序应用，中途 I/O 失败会留下已写入的前序条目，插件必须容忍半应用的批次。
- **插件不是完全不能读明文**：`secrets.get` 会返回明文（后端方法集与 iframe 方法集都有）。iframe
  路径已收紧为**仅 admin session** 可读（`plugin-ui-host.ts` 的 `secretsGet`），非 admin 得到
  `PERMISSION_DENIED`；`secrets.list` 只给键名与已配置标记，`set`/`delete` 不回显值。跨插件读取
  在结构上仍不可表达（pluginId 由 session 注入，API 无此参数）。

代价是这条先例存在，后续插件可以引用它申请同类能力。

**2. 凭据 bundle 有 64 KB 上限。** 整个凭据列表作为单个 secret 值存储，以避免引入动态 secret key
（那需要拆分前后端方法集）。12 条凭据约 10 KB，50 条仍在限内；超限会报明确错误而非静默截断。

**3. 本地进程出站网络无运行时强制**（既有缺口，非本插件引入）。manifest 声明了 `network.mode: "allowlist"` 与域名/端口，但 `07-security-and-sandbox.md` §6.2 记录该项为「[待决策]」，实际只有 manifest 校验、没有运行时阻断。本插件是**第一个真正依赖真实 egress 的插件**，等于把这个缺口摆上台面。

**4. `MultiTokenManager` 没有公开的凭据读取方法。** 变更后的列表通过读回插件自有数据目录的
`credentials.json` 获得（该文件是中转，真正持久化的是 vault 条目）。这样做是为了不修改内置模块；如果
将来内置暴露了访问器，这一步可以简化。

**5. 双消费者一致性。** 内置与插件解析同一套上游事件。guard 测试保证可打包，但不保证两者行为一致——映射层有自己的测试，不依赖巧合。

**6. 自动化测试不打真实上游。** 避免 CI 依赖外部账号与配额，代价是上游协议漂移不会被 CI 发现，需要定期跑 `--live` 脚本。

## 构建

```bash
bun run build:plugin kiro-external        # 构建产物
bun run plugins:check-artifacts           # 校验产物与源码一致
```

产物（`server/index.js`、`ui/provider-settings.iife.js`）提交进仓库，与其他示例插件一致：`validate-plugin-release.ts` 需要现成产物做 runtime 校验，插件运行器也直接 spawn 该文件。
