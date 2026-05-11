---
name: narrafork-release
description: NarraFork 快捷发布工具，用于构建、修改版本号、上传到更新服务器。
---

# NarraFork 发布工具

用于 NarraFork 应用的版本发布管理。

## 配置文件

发布前需在项目根目录创建 `.release.local.json` 文件（已加入 gitignore）：

```json
{
  "server": "https://update.narrafork.dev",
  "token": "your-upload-token-here"
}
```

## 执行流程

当用户调用 `/narrafork-release` 时，按以下步骤执行：

### 步骤 1：检查配置

读取 `.release.local.json`，确认 server 和 token 已配置。如果不存在，提示用户创建。

### 步骤 2：读取当前版本

从 `package.json` 读取当前版本号。

### 步骤 3：询问版本类型

使用 `AskUserQuestion` 工具询问版本类型：

选项：
- **补丁版本 (Patch)** - x.y.z → x.y.(z+1)，bug 修复
- **小版本 (Minor)** - x.y.z → x.(y+1).0，新功能
- **大版本 (Major)** - x.y.z → (x+1).0.0，重大更新
- **Beta 版本** - x.y.z → x.y.z-beta.1 或 x.y.z-beta.N → x.y.z-beta.(N+1)
- **自定义版本** - 用户输入自定义版本号

### 步骤 4：询问构建平台

使用 `AskUserQuestion` 工具询问要构建的平台（多选）：

选项：
- **Linux x64** - 标准 Linux 64位 (linux-x64)
- **Linux ARM64** - Linux ARM64 (linux-arm64)
- **Linux x64 Baseline** - 兼容旧 CPU 的 Linux (linux-x64-baseline)
- **macOS x64** - Intel Mac (darwin-x64)
- **macOS ARM64** - Apple Silicon Mac (darwin-arm64)
- **Windows x64** - Windows 64位 (windows-x64)
- **全部平台** - 构建所有平台 (all)

### 步骤 5：询问 Git 操作

使用 `AskUserQuestion` 工具询问是否执行 Git 操作：

选项：
- **完整发布** - 构建、上传、提交、打 Tag、推送
- **仅构建上传** - 跳过所有 Git 操作，仅构建和上传（快速发布）

### 步骤 6：运行发布脚本

根据用户选择，计算新版本号，然后执行 Go 发布脚本：

```bash
# 完整发布（单平台）
go run .kiro/skills/narrafork-release/release.go -token {token} -version {version} -platforms linux-x64

# 完整发布（多平台）
go run .kiro/skills/narrafork-release/release.go -token {token} -version {version} -platforms linux-x64,darwin-arm64,windows-x64

# 仅构建上传
go run .kiro/skills/narrafork-release/release.go -token {token} -version {version} -platforms linux-x64 -skip-git
```

参数说明：
- `-token` - 从 `.release.local.json` 的 `token` 字段读取
- `-version` - 新版本号，如 `0.1.0` 或 `0.1.0-beta.1`
- `-platforms` - 要构建的平台，逗号分隔（linux-x64,linux-arm64,darwin-x64,darwin-arm64,windows-x64）
- `-skip-git` - 跳过所有 Git 操作

脚本根据版本号自动判断渠道：
- 包含 `beta` → beta 渠道
- 不包含 `beta` → stable 渠道

**重要**：设置 Bash timeout 为 600000（10 分钟），因为构建和上传需要较长时间。

### 步骤 7：上传二进制到 123 网盘（仅正式版本）

**仅当版本号的 patch 位为 `0`（即 x.y.0）时执行此步骤。**

判断逻辑：
- 正式版本（如 `0.4.0`、`1.0.0`、`1.2.0`）→ patch 位为 0 → 执行上传
- 测试版本（如 `0.4.1`、`0.4.2`、`1.0.3`）→ patch 位非 0 → 跳过此步骤

上传流程：

1. 检查 rclone 是否已配置 `nf_update` 远端：
   ```bash
   rclone listremotes | grep nf_update
   ```
   如果未配置，提示用户先运行 `rclone config` 配置 WebDAV 远端（服务器地址：`https://webdav.123pan.cn/webdav`，账户使用团队私有配置或发布负责人提供的 123 网盘 WebDAV 账号），远端名称为 `nf_update`。

2. 先显式创建远端版本目录（123 网盘 WebDAV 可能不会在 `rclone copy` 时自动创建父目录）：
   ```bash
   rclone mkdir nf_update:{version} -v
   ```
   即使目录已存在，也可以安全执行此步骤。

3. 上传纯二进制文件到 `nf_update:{version}/` 子目录，排除 patch、metadata、blockmap、7z 等文件：
   ```bash
   rclone copy dist/ nf_update:{version} \
     --filter "+ narrafork-{version}-linux-arm64" \
     --filter "+ narrafork-{version}-linux-x64" \
     --filter "+ narrafork-{version}-linux-x64-baseline" \
     --filter "+ narrafork-{version}-macos-arm64" \
     --filter "+ narrafork-{version}-macos-x64" \
     --filter "+ narrafork-{version}-windows-x64.exe" \
     --filter "+ narrafork-{version}-windows-x64-baseline.exe" \
     --filter "- *" \
     -v --progress
   ```
   **注意**：`--filter` 规则按顺序匹配，必须先列出要包含的文件（`+ 文件名`），最后用 `- *` 排除其余所有文件。不要混用 `--include` 和 `--exclude`，否则顺序不确定会导致错误文件被上传。

4. 设置 Bash timeout 为 600000（10 分钟），上传大文件需要较长时间。

5. 上传完成后，验证远端文件数量是否与本地构建的平台数量一致：
   ```bash
   rclone ls nf_update:{version}
   ```

### 步骤 8：报告结果

发布完成后，显示：
- 上传的文件列表
- 新版本号
- 更新服务器地址
- 如果执行了 Git 操作，显示创建的 tag
- 如果上传了 123 网盘，显示上传的文件列表和远端路径 `nf_update:{version}/`

## 版本号规则

| 类型 | 格式 | 示例 | 渠道 | 上传 123 网盘 |
|------|------|------|------|------|
| 正式版本 | x.y.0 | 1.0.0、0.4.0 | stable | ✅ |
| 测试版本 | x.y.z（z≠0） | 0.4.1、1.0.3 | stable | ❌ |

## 版本号计算

```javascript
// 当前版本: 0.0.8
// Patch: 0.0.9
// Minor: 0.1.0
// Major: 1.0.0
// Beta: 0.0.8-beta.1

// 当前版本: 0.0.8-beta.3
// Beta: 0.0.8-beta.4
// Patch: 0.0.9 (移除 beta 后缀)
```

## 构建产物

脚本会自动查找 `dist/` 目录下的以下文件并上传：
- `narrafork-{version}-linux-x64` - Linux x64 二进制
- `narrafork-{version}-linux-x64.blockmap` - Linux x64 差异更新索引
- `narrafork-{version}-linux-arm64` - Linux ARM64 二进制
- `narrafork-{version}-darwin-arm64` - macOS ARM64 二进制
- `narrafork-{version}-darwin-x64` - macOS x64 二进制
- `narrafork-{version}-windows-x64.exe` - Windows 二进制
- `latest.yml` / `latest-mac.yml` / `latest-linux.yml` - 版本元数据

## Git 操作流程（非 skip-git 模式）

1. `git add package.json` - 暂存版本变更
2. `git commit -m "chore: release v{version}"` - 提交
3. `git tag -a v{version} -m "Release {version}"` - 创建标签
4. `git push origin HEAD` - 推送提交
5. `git push origin v{version}` - 推送标签

## 错误处理

- 构建失败时自动回滚 `package.json` 版本号
- 上传失败时显示具体错误信息
- 支持并发上传，单个文件失败不影响其他文件

## 示例对话

用户: `/narrafork-release`

Claude:
1. 检查 `.release.local.json` 存在
2. 读取当前版本 `0.0.8`
3. 询问版本类型 → 用户选择 "补丁版本"
4. 计算新版本 `0.0.9`
5. 询问构建平台 → 用户选择 "Linux x64"
6. 询问 Git 操作 → 用户选择 "完整发布"
7. 执行: `go run .kiro/skills/narrafork-release/release.go -token xxx -version 0.0.9`
8. 报告结果
