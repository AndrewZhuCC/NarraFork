# API 请求证据：循环录制与固定保存

请求证据回答的是「实际发送的请求是什么、返回的流里发生了什么」。NarraFork 只有一套证据存储：请求录制器（`server/lib/request-recorder*.ts`，在 worker 线程中运行）。使用历史、错误卡片、泄漏工具调用弹窗、`/save_dump` 都从这里取证据。旧版 usage-history `raw_dump_json`、`request-dumps/` 溢写文件、`malformed-request-dumps/` 和历史 Token 核对已经下线（设计与取舍见 `docs/REQUEST_EVIDENCE-DESIGN.md`）。

## 设置（`settings.requestEvidence`）

| 键 | 默认 | 含义 |
| --- | --- | --- |
| `ringEnabled` | `true` | 是否开启循环录制窗口 |
| `ringRequestBytes` | 16 MiB | 请求侧窗口容量；`0` 只关闭这一侧 |
| `ringResponseBytes` | 128 MiB | 返回侧窗口容量；`0` 只关闭这一侧 |
| `autoPin` | `"off"` | 自动固定：`off` 仅手动、`errors` 固定失败请求、`all` 固定每个请求 |

保存后实时生效。旧的 `agent.requestDump*` 键在加载 `settings.json` 时一次性映射到 `requestEvidence` 并删除；旧客户端 PATCH 里的 `agent.requestDump*` 同样会被映射（显式的 `requestEvidence` 段优先）。`autoPin` 非 `off` 时顶栏显示提醒徽标。

## 循环窗口

- **实例级窗口，独立双预算。** 整个实例共享请求、返回两份预算，不是每个叙述者各自获得这些容量。按实际录制文件字节记账，非零窗口至少 1 KiB；任一侧流量不能挤占另一侧；旧内容按循环策略淘汰，单条超限和不完整内容会明确标记，而不是声称保留了全量。循环区位于 `~/.narrafork/request-recordings/`。
- **请求头有独立预算。** 元数据通用预算是 8 KiB，但请求/响应头单独计：最多 256 个、单值 8 KiB、合计 64 KiB（`RECORDING_HEADER_*`，见 `request-recorder-sanitize.ts`），按原顺序保留，超出部分丢弃并在录制里留下 `headers truncated` gap。认证类头始终遮蔽为 `[REDACTED]`。
- **崩溃恢复核对实际段文件。** 请求开始先持久化归属 receipt，持续模式先发布初始部分保存再接收字节。重启时有界核对 `chunks` 和 checkpoint：合法未引用段重新入账，仅截去不完整尾帧；随后按当前双预算淘汰，已固定的快照不改写。

## 固定保存

固定保存位于 `~/.narrafork/saved-request-recordings/`，不随循环淘汰，保留到用户显式删除为止；容量不受窗口预算约束，另设保存数量、单份归档（512 MiB）、磁盘压力等安全限制。每份保存带 `reason`：

| `reason` | 来源 |
| --- | --- |
| `manual` | `/save_dump` 或 `POST /request-recordings`：当前窗口的多请求快照 |
| `request` | 使用历史 / 错误卡片下载单个请求时按需固定 |
| `continuous` | `autoPin: "all"`：从请求开始就持续写入固定区 |
| `error` | `autoPin: "errors"`：请求以错误结束时固定 |
| `malformed` | 上游以「请求体格式错误」拒绝时，loop 写入 `malformed_request_summary` 帧并固定 |
| `leak` | 流式工具调用捕获失败（recovered / unrecovered）时由事件处理器固定 |

- **`/save_dump` 是截至调用时的不可变快照。** 命令不接受参数，不调用模型、不排队，也不会中断运行中的请求；只截取调用时已收到且仍在窗口内的部分，响应未结束、前段被淘汰或超预算时标记 `partial` / `truncated`。重新执行会产生另一份保存。
- **自动固定是尽力而为。** 进行中的请求被固定时，在请求结束时写入保存；已结束但仍在窗口里的请求立即保存；同一请求重复固定不会产生第二份。保存失败（超限、磁盘压力）只在录制里留下 `pin failed` gap，不影响请求本身。
- 录制关闭（窗口关闭且 `autoPin: "off"`）时，格式错误、泄漏等异常只剩 `api_requests.diagnostics_json` 的有界摘要，没有完整证据。

## 与使用历史的关联

`api_requests` 不再保存 dump，只有两列：

- `diagnostics_json`：有界诊断摘要（`DIAGNOSTICS_MAX_BYTES` = 16 KiB），列表只返回 `hasDiagnostics`，详情接口才读内容。
- `recording_request_id`：loop 尝试 ID，即录制器里的 `requestId`。使用历史的「固定并下载请求证据」用它加上 `narrator_id` 调用下面的单请求接口；请求已滚出窗口且未固定时返回 404，前端提示证据已不存在。

泄漏工具调用通知（`leaked_tool_call_notice`）同样带 `recordingRequestId`，弹窗下载走同一路径。

## 接口与权限

沿用叙述者 ACL：保存、删除需要 **write**；列表、详情、下载需要 **read**。不存在、无权限以及归属不匹配均按 **404** 隐藏。保存 ID 必须符合系统 ID 格式。

| 方法与路径（前缀 `/api/narrators/:id`） | 返回 |
| --- | --- |
| `POST /request-recordings` | 当前窗口快照，`SavedRecording` |
| `GET /request-recordings?cursor=...&limit=...` | `{ items, nextCursor }`；默认 20 条，`limit` 1–100 |
| `GET /request-recordings/requests/:requestId` | 单请求详情（不写入），含已有的单请求保存与关联的 `api_requests` 有界元数据 |
| `POST /request-recordings/requests/:requestId` | 固定这一个请求，`SavedRecording` |
| `GET /request-recordings/:saveId/download` | 流式 ZIP 下载 |
| `DELETE /request-recordings/:saveId` | `{ ok: true }` |

下载走有界流式归档：不在 HTTP 主线程拼装响应；最多 2 个压缩任务、4 个待传输下载，临时文件有 120 秒租约，完成、取消、过期和进程恢复都会清理派生产物而不删除固定证据。

## 隐私与可见性边界

请求头、URL 与可识别认证字段会脱敏，但这不是匿名化：消息正文、提示词、工具输入/输出、文件内容及上游错误中可能仍有个人信息或商业机密。录制与 ZIP 应按会话数据管理，下载和转发前检查内容。

录制目录属于私有平台数据：通用文件预览、下载、编辑读取和 `ShareFile` 分享不能直接读取，也不能通过区外符号链接或打包祖先目录绕过。旧版 `request-dumps` / `malformed-request-dumps` / `token-repair-jobs` 目录在启动时后台删除（`legacy-request-dump-cleanup.ts`）；删除完成前 `token-repair-jobs` 仍按私有目录处理，另两个目录的可见性与此前一致。

WebSocket 录制是**应用层消息/帧证据**，不是网络抓包。插件提供商仅能录制经过宿主可见接入点的请求/响应或插件显式提供的证据。

## 升级说明

迁移 `0207_drop_legacy_request_dumps` 删除 `raw_dump_json`、Token 核对列和两张核对表；SQLite 的 `DROP COLUMN` 会重写 `api_requests` 表，大库升级时这一步需要一些时间。释放的空间需要在存储设置里手动执行 VACUUM 才会还给磁盘。
