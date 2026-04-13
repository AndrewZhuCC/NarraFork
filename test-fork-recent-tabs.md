# 测试：分叉章节后叙述者自动添加到 recentTabs

## 问题描述
分叉出来的叙述者无法进入 recentTabs，**即使手动访问该叙述者页面也不会添加**。

## 根本原因
叙述者详情页的 `useEffect` 中使用了 `updateOnly: true` 参数调用 `addRecentTab`。这导致：
- 如果 tab 已存在，会更新其属性
- 如果 tab 不存在，**不会添加新 tab**（直接返回）

分叉出来的叙述者是全新的，不在 recentTabs 中，所以即使访问也无法添加。

## 修复内容

### 1. 移除 `updateOnly: true` 参数（核心修复）
**文件**: `frontend/routes/narrators/$narratorId.tsx`

在叙述者详情页的 `useEffect` 中，移除 `updateOnly: true`，让它能够添加新的 tab：

```typescript
// 修复前
addRecentTab({
    type: "chapter",
    id: chapterId,
    narratorId: narratorId,
    title: displayTitle,
    subtitle: chapterTitle,
    status: narratorStatus,
    updateOnly: true,  // ❌ 导致新 tab 无法添加
});

// 修复后
addRecentTab({
    type: "chapter",
    id: chapterId,
    narratorId: narratorId,
    title: displayTitle,
    subtitle: chapterTitle,
    status: narratorStatus,
    // ✅ 移除 updateOnly，允许添加新 tab
});
```

### 2. Fork 成功后立即添加到 recentTabs（额外优化）
在所有调用 `api.forkChapter` 的地方，在成功回调中添加 `addRecentTab` 调用，这样用户无需手动访问就能看到新叙述者：

**修改的文件**：
- `frontend/routes/narrators/$narratorId.tsx` - 叙述者详情页的 fork
- `frontend/components/graph/ChapterNode.tsx` - 图节点的 fork  
- `frontend/components/chapter/ChapterForkModal.tsx` - Fork 模态框
- `frontend/components/ruler/RulerFlow.tsx` - Ruler 模式的 fork

## 测试步骤

### 测试 1：手动访问分叉的叙述者（核心修复）
1. 使用任意方式 fork 一个章节（不要点击通知跳转）
2. 在浏览器地址栏直接输入新叙述者的 URL 访问
3. **预期结果**：新叙述者应该立即出现在左侧 recentTabs 中

### 测试 2：从叙述者详情页 fork（消息右键菜单）
1. 打开一个章节的叙述者页面
2. 右键点击某条消息，选择 "Fork from here"
3. **预期结果**：fork 成功后，新章节的叙述者应该立即出现在左侧 recentTabs 中（无需手动访问）

### 测试 3：从图节点 fork（消息右键菜单）
1. 打开项目的故事网络图（classic 模式）
2. 展开一个章节节点，显示叙述者面板
3. 右键点击某条消息，选择 "Fork from here"
4. **预期结果**：fork 成功后，新章节的叙述者应该立即出现在左侧 recentTabs 中

### 测试 4：从 Fork 模态框 fork
1. 打开一个章节详情页
2. 点击 "Fork" 按钮，打开 fork 模态框
3. 填写标题，选择继承模式，点击 "Fork"
4. **预期结果**：fork 成功后，新章节的叙述者应该立即出现在左侧 recentTabs 中

### 测试 5：从 Ruler 模式 fork
1. 打开项目的故事网络图（ruler 模式）
2. 右键点击某个章节气泡，选择 "Fork"
3. **预期结果**：fork 成功后，新章节的叙述者应该立即出现在左侧 recentTabs 中

## 验证要点
- 新叙述者应该出现在 recentTabs 的**顶部**（最近访问）
- Tab 类型应该是 `chapter`（因为是章节绑定的叙述者）
- Tab 应该显示正确的标题、副标题和状态
- 点击 tab 应该能正确导航到叙述者页面
- **重复访问同一个叙述者不应该创建重复的 tab**（应该更新现有 tab 并移到顶部）

## 技术细节

### `updateOnly` 参数的行为
在 `server/routes/user-preferences.ts` 的 `upsertRecentTab` 端点中：

```typescript
const idx = tabs.findIndex((t) => t.type === tab.type && t.id === tab.id);
if (idx >= 0) {
    // Tab 存在：更新
    if (updateOnly) {
        // 只合并非空字段
        tabs[idx] = { ...tabs[idx], ...patch };
    } else {
        // 完整更新并移到顶部
        tabs[idx] = { ...tabs[idx], ...tab };
        promoteTopLevelTabRespectingPins(tabs, idx);
    }
} else if (updateOnly) {
    // Tab 不存在 + updateOnly=true：直接返回，不添加 ❌
    return c.json(tabs);
} else {
    // Tab 不存在 + updateOnly=false：添加新 tab ✅
    insertTopLevelTabRespectingPins(tabs, tab);
}
```

### 为什么其他地方仍然使用 `updateOnly: true`
其他地方使用 `updateOnly: true` 是合理的，因为它们只是在更新已存在的 tab 的属性：
- **RecentTabs.tsx**: 释放 workspace 时清除子 tab 的 workspaceId
- **projects/$projectId.tsx**: 项目页面访问时更新项目信息
- **workspace/$workspaceId.tsx**: workspace 页面访问和子 narrator 同步

这些场景不需要添加新 tab，只需要更新现有 tab 的属性。
