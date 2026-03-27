---
description: 从 git 提交记录生成双语更新日志用于发布。当用户要求生成 changelog、准备发布、写更新日志时触发。
---

# generate-changelog

基于 git 提交历史生成双语（英文 + 中文）更新日志，输出为 JSON 文件供发布脚本使用。

## 步骤

1. 找到上一个版本标签：
   ```bash
   git describe --tags --abbrev=0 2>/dev/null || echo "none"
   ```
   如果没有标签，使用所有提交。

2. 获取从上一个标签到 HEAD 的所有提交：
   ```bash
   git log <last-tag>..HEAD --oneline --no-merges
   ```
   如果没有标签：`git log --oneline --no-merges -30`

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

5. 将结果写入 `/tmp/changelog.json`：
   ```json
   {
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
- 完成后告知用户文件路径，并提示运行发布命令：
  `bun scripts/release.ts <version> --changelog=/tmp/changelog.json`
