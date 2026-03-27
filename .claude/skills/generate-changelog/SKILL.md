---
name: "generate-changelog"
description: 从 git 提交记录生成双语更新日志用于发布。当用户要求生成 changelog、准备发布、写更新日志时触发。
---

# generate-changelog

基于 git 提交历史生成双语（英文 + 中文）更新日志，写入 `changelogs/` 目录供发布脚本和前端 changelog 页面使用。

## 确定版本号

- 如果用户指定了版本号，直接使用
- 否则，读取 `package.json` 中的 `version` 字段作为当前版本，询问用户本次发布的版本号

## 步骤

1. 找到上一个版本标签：
   ```bash
   git describe --tags --abbrev=0 2>/dev/null || echo "none"
   ```
   如果没有标签，使用所有提交。

2. 获取从上一个标签到 HEAD 的所有提交（包含完整 body 以获取更多上下文）：
   ```bash
   git log <last-tag>..HEAD --format="%B---COMMIT_SEP---" --no-merges
   ```
   如果没有标签：`git log --format="%B---COMMIT_SEP---" --no-merges -30`

3. 按 conventional commit 前缀分类：
   - **feat** → New Features / 新功能
   - **fix** → Bug Fixes / 修复
   - **refactor** → Refactoring / 重构
   - **perf** → Performance / 性能优化
   - **docs** → Documentation / 文档
   - 其他 → Other Changes / 其他变更

4. 生成双语 changelog，每种语言的格式：
   ```
   ## New Features

   - Description of feature A
   - Description of feature B

   ## Bug Fixes

   - Description of fix A
   ```

5. 将结果写入 `changelogs/v{version}.json`：
   ```json
   {
     "version": "0.2.0",
     "date": "2026-04-01",
     "en": "## New Features\n\n- Feature A\n- Feature B\n\n## Bug Fixes\n\n- Fix A",
     "zh-CN": "## 新功能\n\n- 功能 A\n- 功能 B\n\n## 修复\n\n- 修复 A"
   }
   ```

## 规则

- 不要逐字翻译 commit message，用简洁的用户视角描述变更
- 合并相关的小 commit 为一条描述
- 忽略纯内部重构（除非影响用户体验）
- 每条描述控制在一行内
- 中文描述不需要是英文的直译，应该自然流畅
- 空分类不要输出
- `date` 字段使用当天日期，格式 `YYYY-MM-DD`
- 完成后告知用户文件路径，并提示：
  - 发布命令：`bun scripts/release.ts <version>`（release.ts 会自动从 `changelogs/v<version>.json` 读取）
  - 构建时 `scripts/build-cross-platform.ts` 会自动将 `changelogs/` 下所有 JSON 嵌入二进制
  - 前端 `/changelog` 页面可查看所有历史版本的更新日志
