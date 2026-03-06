# Terminal Buffer 修复计划

## 问题总结

Terminal 显示异常的根本原因是 buffer 回放机制存在多个结构性缺陷，尤其在"更新光标前面的行"（进度条、spinner、TUI 刷新）场景下表现明显。

---

## 问题 1：重连后 scrollback 重复叠加（最严重）

**现状：**
- WS 重连时 `TerminalWSManager.onopen` 自动重新 subscribe 所有 terminal（`useTerminalWS.ts:66-70`）
- 服务端 `subscribeToTerminal` 每次都发全量 scrollback（`terminal-ws.ts:81-86`）
- 前端 `onScrollback` 直接 `term.write(data)`，不清屏（`TerminalPanel.tsx:190-193`）
- 结果：旧内容 + 新回放叠加，光标相对移动序列在错误位置执行

**修复：**

### 文件：`frontend/components/terminal/TerminalPanel.tsx`

在 `onScrollback` 回调中，写入前先 reset 终端：

```typescript
onScrollback: (data) => {
    const term = termRef.current;
    if (!term) return;
    writingRef.current = true;
    term.reset();    // 清除所有内容和状态
    term.write(data);
    writingRef.current = false;
},
```

这是最小改动，语义正确：scrollback 是"完整的历史快照"，应该替换而非追加。

---

## 问题 2：订阅竞态 — xterm 未就绪时丢包

**现状：**
- `useTerminalWS` 的 subscribe effect 依赖 `[terminalId]`（`useTerminalWS.ts:256-283`）
- xterm 初始化 effect 依赖 `[write, resize, handleSelectionChange]`（`TerminalPanel.tsx:232-501`）
- 两个 effect 执行顺序不确定，subscribe 可能先于 xterm 创建完成
- 此时 `termRef.current` 为 null，`?.write()` 静默丢弃数据

**修复：**

### 文件：`frontend/components/terminal/TerminalPanel.tsx`

引入一个 pending queue ref，在 xterm 未就绪时缓存收到的数据：

```typescript
// 在组件顶部添加
const pendingDataRef = useRef<string[]>([]);
const termReadyRef = useRef(false);
```

修改 WS 回调，在 term 未就绪时缓存：

```typescript
onOutput: (data) => {
    if (!termReadyRef.current) {
        pendingDataRef.current.push(data);
        return;
    }
    writingRef.current = true;
    termRef.current?.write(data);
    writingRef.current = false;
},
onScrollback: (data) => {
    if (!termReadyRef.current) {
        // scrollback 是全量快照，替换之前所有 pending
        pendingDataRef.current = [data];
        return;
    }
    writingRef.current = true;
    termRef.current!.reset();
    termRef.current!.write(data);
    writingRef.current = false;
},
```

在 xterm 初始化 effect 中，创建完成后 flush pending：

```typescript
// 在 termRef.current = term; 之后
termReadyRef.current = true;

// Flush any data received before xterm was ready
if (pendingDataRef.current.length > 0) {
    writingRef.current = true;
    for (const chunk of pendingDataRef.current) {
        term.write(chunk);
    }
    writingRef.current = false;
    pendingDataRef.current = [];
}
```

在 cleanup 中重置：

```typescript
// 在 return () => { 的开头
termReadyRef.current = false;
pendingDataRef.current = [];
```

---

## 问题 3：`filterProblematicSequences` 正则缺陷

**现状（`buffer-manager.ts:102-115`）：**

### 3a. DECRQSS 正则缺少 ESC 前缀
```typescript
.replace(/\d+;\d+\$y/g, "")
```
这会匹配正文中的 `123;456$y` 这样的字符串，误删正常内容。

### 3b. DA 响应正则字符类错误
```typescript
.replace(new RegExp(`${ESC}\\[[\\?>\\d;]*c`, "g"), "")
```
`[\\?>\\d;]` 中 `\\d` 在字符类里不是数字简写，而是字面 `d`。应该是 `[\\?0-9;>]`。

### 3c. 删除 alternate screen 切换但保留其内容
删除 `1049h/l` 但不删除 alternate screen 内的内容，回放时这些内容会写入 normal buffer，造成混乱。

**修复：**

### 文件：`server/terminal/buffer-manager.ts`

#### 3a. 修正 DECRQSS 正则，加上 ESC 前缀：
```typescript
// DECRQSS responses (DCS ... $y ST)
.replace(new RegExp(`${ESC}P[^${ESC}]*\\$y${ESC}\\\\`, "g"), "")
```
实际上 DECRQSS 响应格式是 `DCS Ps $ y ST`，完整匹配更安全。但考虑到这个过滤本身就是防御性的，最简单的修复是给原正则加 ESC 前缀约束：
```typescript
.replace(new RegExp(`${ESC}\\[\\d+;\\d+\\$y`, "g"), "")
```

#### 3b. 修正 DA 响应正则：
```typescript
.replace(new RegExp(`${ESC}\\[[?>=0-9;]*c`, "g"), "")
```

#### 3c. 跟踪 alternate screen 状态，回放时丢弃 alternate screen 内容

在 `BufferManager` 中添加 alternate screen 跟踪：

```typescript
private inAlternateScreen = false;
```

在 `trackTerminalState` 中添加：
```typescript
// Alternate screen tracking (last occurrence wins)
if (data.includes(`${ESC}[?1049h`) || data.includes(`${ESC}[?47h`) || data.includes(`${ESC}[?1047h`)) {
    this.inAlternateScreen = true;
}
if (data.includes(`${ESC}[?1049l`) || data.includes(`${ESC}[?47l`) || data.includes(`${ESC}[?1047l`)) {
    this.inAlternateScreen = false;
}
```

在 `getContents` 中，如果当前在 alternate screen，只返回空（或一个 reset 序列），因为 alternate screen 的内容在回放时没有意义（TUI 已退出或会重绘）：

```typescript
getContents(): string {
    // If currently in alternate screen (TUI running), don't replay raw buffer
    // — the TUI will redraw on attach. Send a minimal reset instead.
    if (this.inAlternateScreen) {
        let output = `${ESC}[?1049h${ESC}[H${ESC}[2J`; // Enter alt screen, home, clear
        if (!this.cursorVisible) output += `${ESC}[?25l`;
        return output;
    }
    
    const raw = this.chunks.map((c) => c.data).join("");
    let output = this.filterProblematicSequences(raw);
    if (!this.cursorVisible) {
        output = `${ESC}[?25l${output}`;
    }
    return output;
}
```

同时在 `saveToDisk` 和 `loadFromDisk` 中持久化 `inAlternateScreen` 状态。需要升级文件格式到 V2（向后兼容）：

```typescript
interface BufferFileV2 extends BufferFileV1 {
    version: 2;
    inAlternateScreen: boolean;
}
```

加载时兼容 V1：
```typescript
this.inAlternateScreen = parsed.version >= 2 ? !!parsed.inAlternateScreen : false;
```

---

## 问题 4：Buffer 截断破坏终端上下文

**现状（`buffer-manager.ts:77-84`）：**
```typescript
while (this.totalChars > MAX_BUFFER_CHARS && this.chunks.length > 1) {
    const removed = this.chunks.shift();
    if (removed) this.totalChars -= removed.data.length;
}
```
直接从头部删除 chunk，可能在转义序列中间截断，导致回放时后续序列解析错误。

**修复：**

### 文件：`server/terminal/buffer-manager.ts`

在 `getContents` 中，回放前先发一个 SGR reset + 光标归位，确保即使前面的状态被截断，后续渲染也从干净状态开始：

```typescript
getContents(): string {
    if (this.inAlternateScreen) {
        let output = `${ESC}[?1049h${ESC}[H${ESC}[2J`;
        if (!this.cursorVisible) output += `${ESC}[?25l`;
        return output;
    }
    
    const raw = this.chunks.map((c) => c.data).join("");
    let output = this.filterProblematicSequences(raw);
    
    // Prepend a state reset to handle truncated escape sequences at buffer start
    const stateReset = `${ESC}[0m${ESC}[H${ESC}[2J`;
    output = stateReset + output;
    
    if (!this.cursorVisible) {
        output = `${ESC}[?25l${output}`;
    }
    return output;
}
```

`ESC[0m` 重置 SGR 属性，`ESC[H` 光标归位，`ESC[2J` 清屏。这确保回放从干净状态开始，即使 buffer 头部被截断。

---

## 问题 5：`writingRef` 防回灌可能遗漏异步响应

**现状（`TerminalPanel.tsx:186-188`）：**
```typescript
writingRef.current = true;
termRef.current?.write(data);
writingRef.current = false;
```

xterm.js 的 `write()` 是异步的（内部有 write buffer），但 `onData` 回调可能在 write 的微任务中触发。当前同步设置 flag 的方式在大多数情况下工作，但不是 100% 可靠。

**修复：**

使用 xterm.js 的 `write(data, callback)` 重载，在回调中才关闭 flag：

```typescript
onOutput: (data) => {
    const term = termRef.current;
    if (!term || !termReadyRef.current) {
        pendingDataRef.current.push(data);
        return;
    }
    writingRef.current = true;
    term.write(data, () => {
        writingRef.current = false;
    });
},
```

但这有一个问题：如果多个 output 快速到达，第二个 write 开始时第一个的 callback 可能还没执行。所以改用计数器更安全：

```typescript
const writeDepthRef = useRef(0);

// 在 onData 中：
if (writeDepthRef.current > 0) return;

// 在 onOutput/onScrollback 中：
writeDepthRef.current++;
term.write(data, () => {
    writeDepthRef.current--;
});
```

实际上，经过仔细分析，xterm.js 5.x 的 `onData` 在 `write()` 期间的触发是同步的（在 parser 处理输入时立即触发）。当前的 `writingRef` 方案对同步触发是有效的。真正的风险是 `write()` 内部的异步调度，但 xterm.js 的 parser 是同步执行的，`onData` 也是同步触发的。

所以当前方案实际上是正确的，不需要改。保留 `writingRef` 即可。

**结论：问题 5 不需要修复，当前实现对 xterm.js 5.x 是正确的。**

---

## 修改文件清单

| 文件 | 改动 |
|------|------|
| `server/terminal/buffer-manager.ts` | 修正正则、添加 alternate screen 跟踪、回放前 state reset、文件格式 V2 |
| `frontend/components/terminal/TerminalPanel.tsx` | scrollback 先 reset、pending queue 解决竞态 |

---

## 实施顺序

1. **`server/terminal/buffer-manager.ts`** — 修正 filterProblematicSequences 正则 + 添加 alternate screen 跟踪 + getContents 加 state reset + 文件格式升级
2. **`frontend/components/terminal/TerminalPanel.tsx`** — onScrollback 先 reset + pending queue 机制

---

## 不改动的部分

- `useTerminalWS.ts` — 不需要改，WS 管理器本身逻辑正确
- `terminal-ws.ts` — 服务端 subscribe 发全量 scrollback 的行为是正确的，问题在前端处理方式
- `terminal-service.ts` — 不需要改
- 中期重构（headless xterm + serialize addon 做服务端状态快照）— 这是更彻底的方案，但改动量大，不在本次范围内
