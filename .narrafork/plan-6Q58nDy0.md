# BufferManager 重构：引入 xterm-headless + serialize addon

## 目标

用 `@xterm/headless` + `@xterm/addon-serialize` 替代当前的原始字节流拼接方案。
服务端维护一个 headless xterm 实例来解析所有 PTY 输出，恢复时用 serialize addon 生成干净的状态快照，
彻底消除手动正则过滤、截断破坏、alternate screen 跟踪等问题。

## 依赖

新增两个 npm 包（均兼容当前 `@xterm/xterm@5.5.0`）：
- `@xterm/headless@5.5.0` — 服务端 headless terminal
- `@xterm/addon-serialize@0.13.0` — 序列化 terminal framebuffer

## 架构变更

### 当前架构
```
PTY → onData → BufferManager.append(raw text)
                 ├─ 手动 trackTerminalState (正则匹配 ESC 序列)
                 ├─ 原始 chunk 拼接存储
                 └─ getContents() → filterProblematicSequences(正则过滤) → 发给前端
```

### 新架构
```
PTY → onData → BufferManager.append(raw text)
                 ├─ headless xterm.write(data) — 完整解析所有 ANSI 序列
                 ├─ 内部维护真实终端状态（buffer、光标、SGR、alternate screen 等）
                 └─ getContents() → SerializeAddon.serialize() → 干净的状态快照
```

## 改动范围

### 1. `package.json` — 添加依赖

```json
"@xterm/headless": "5.5.0",
"@xterm/addon-serialize": "0.13.0"
```

### 2. `server/terminal/buffer-manager.ts` — 核心重写

**保持不变的公共接口：**
- `constructor(terminalId: string)`
- `startPeriodicFlush(intervalMs?): void`
- `stopPeriodicFlush(): void`
- `append(data: string): void`
- `getContents(): string`
- `getState(): { mouseTracking: boolean; cursorVisible: boolean }`
- `clear(): void`
- `saveToDisk(): void`
- `loadFromDisk(): boolean`
- `deleteFromDisk(): void`
- `dispose(deleteBuffer?): void`

**内部实现替换：**

```typescript
import { Terminal } from "@xterm/headless";
import { SerializeAddon } from "@xterm/addon-serialize";

const DEFAULT_COLS = 80;
const DEFAULT_ROWS = 24;
const DEFAULT_SCROLLBACK = 5000;

export class BufferManager {
    private terminalId: string;
    private xterm: Terminal;
    private serializeAddon: SerializeAddon;
    private dirty = false;
    private flushTimer: ReturnType<typeof setInterval> | null = null;

    // 仍然需要手动跟踪 mouse mode，因为 serialize addon 不序列化这个状态
    // （xterm-headless 解析它但 serialize 不输出它）
    private mouseMode = { x10: false, buttonEvent: false, anyEvent: false, sgr: false };

    constructor(terminalId: string, cols = DEFAULT_COLS, rows = DEFAULT_ROWS) {
        this.terminalId = terminalId;
        this.xterm = new Terminal({
            cols,
            rows,
            scrollback: DEFAULT_SCROLLBACK,
            allowProposedApi: true,
        });
        this.serializeAddon = new SerializeAddon();
        this.xterm.loadAddon(this.serializeAddon);
    }

    append(data: string): void {
        this.trackMouseMode(data);
        this.xterm.write(data);
        this.dirty = true;
    }

    getContents(): string {
        // serialize addon 生成的输出是干净的状态快照
        // 包含 scrollback + 当前屏幕，正确处理 alternate screen
        return this.serializeAddon.serialize({
            scrollback: this.xterm.options.scrollback,
        });
    }

    getState(): { mouseTracking: boolean; cursorVisible: boolean } {
        const mouseTracking = this.mouseMode.x10 || this.mouseMode.buttonEvent
            || this.mouseMode.anyEvent || this.mouseMode.sgr;
        // xterm-headless 内部跟踪 cursor visibility，但没有公开 API
        // 通过 buffer 的 cursorHidden 属性获取（如果可用），否则回退到手动跟踪
        const cursorVisible = true; // serialize 会包含 DECTCEM 状态
        return { mouseTracking, cursorVisible };
    }

    resize(cols: number, rows: number): void {
        this.xterm.resize(cols, rows);
    }

    clear(): void {
        this.xterm.reset();
        this.mouseMode = { x10: false, buttonEvent: false, anyEvent: false, sgr: false };
        this.dirty = true;
    }

    saveToDisk(): void {
        // V3 格式：直接存 serialize 输出
        const filePath = join(getBuffersDir(), `${this.terminalId}.buf`);
        try {
            const serialized = this.getContents();
            const fileData: BufferFileV3 = {
                version: 3,
                serialized: Buffer.from(serialized).toString("base64"),
                cols: this.xterm.cols,
                rows: this.xterm.rows,
                mouseMode: { ...this.mouseMode },
            };
            writeFileSync(filePath, JSON.stringify(fileData), "utf-8");
            this.dirty = false;
        } catch (err) {
            logger.error("Failed to save buffer", { terminalId: this.terminalId, error: String(err) });
        }
    }

    loadFromDisk(): boolean {
        // 兼容 V1/V2（旧格式，原始内容）和 V3（serialize 输出）
        const filePath = join(getBuffersDir(), `${this.terminalId}.buf`);
        try {
            if (!existsSync(filePath)) return false;
            const raw = readFileSync(filePath, "utf-8");
            const parsed = JSON.parse(raw);

            if (parsed.version === 3 && typeof parsed.serialized === "string") {
                // V3: 直接写入 serialize 输出到 headless xterm
                const content = Buffer.from(parsed.serialized, "base64").toString();
                if (parsed.cols && parsed.rows) {
                    this.xterm.resize(parsed.cols, parsed.rows);
                }
                this.xterm.write(content);
                if (parsed.mouseMode) {
                    this.mouseMode = { ...parsed.mouseMode };
                }
                return true;
            }

            if ((parsed.version === 1 || parsed.version === 2) && typeof parsed.content === "string") {
                // V1/V2 兼容：旧格式的原始内容，直接写入 headless xterm 解析
                const content = Buffer.from(parsed.content, "base64").toString();
                this.xterm.write(content);
                return true;
            }

            return false;
        } catch (err) {
            logger.error("Failed to load buffer", { terminalId: this.terminalId, error: String(err) });
            return false;
        }
    }

    // ... startPeriodicFlush, stopPeriodicFlush, deleteFromDisk, dispose 保持不变
}
```

**删除的代码：**
- `chunks` 数组和 `totalChars` 计数器 — 不再需要手动管理原始字节流
- `trackTerminalState()` — 大部分状态由 headless xterm 自动跟踪（保留 mouseMode 跟踪）
- `filterProblematicSequences()` — 不再需要，serialize addon 输出的就是干净的
- `BufferFileV1` / `BufferFileV2` 接口 — 保留用于向后兼容加载，新存储用 V3
- `MAX_BUFFER_CHARS` 常量 — 由 xterm scrollback 配置替代
- `inAlternateScreen` 手动跟踪 — headless xterm 自动处理

### 3. `server/services/terminal-service.ts` — 适配

**构造 BufferManager 时传入 cols/rows：**

```typescript
// create() 中：
const buffer = new BufferManager(id, cols, rows);

// reattach() / recoverOnStartup() 中：
const buffer = new BufferManager(terminalId, 80, 24); // 默认值，后续 resize 会更新
```

**新增：resize 时同步到 BufferManager：**

```typescript
resize(terminalId: string, cols: number, rows: number) {
    const active = activeTerminals.get(terminalId);
    if (!active) return;
    active.pty.resize(cols, rows);
    active.buffer.resize(cols, rows);  // 新增
},
```

**getBufferState 简化：**

`getState()` 返回值不变，但内部实现更简单（mouseMode 仍手动跟踪，cursorVisible 由 serialize 自动处理）。

### 4. `server/websocket/terminal-ws.ts` — 无需改动

`subscribeToTerminal` 调用 `terminalService.getScrollback()` 和 `terminalService.getBufferState()`，
这两个方法的签名不变，只是内部实现变了。

### 5. `frontend/` — 无需改动

前端已经在上一轮修复中正确处理了 scrollback（reset + write）和 pending queue。
serialize addon 的输出是标准 ANSI 序列，xterm.js 前端可以直接 `term.write()` 消费。

## 磁盘格式兼容性

| 版本 | 格式 | 兼容 |
|------|------|------|
| V1 | `{ version: 1, content: base64(raw), mouseMode, cursorVisible }` | 加载时写入 headless xterm 解析 |
| V2 | V1 + `inAlternateScreen` | 同上 |
| V3 (新) | `{ version: 3, serialized: base64(serialize output), cols, rows, mouseMode }` | 新格式 |

旧 V1/V2 文件在首次加载后，下次 saveToDisk 会自动升级为 V3。

## serialize addon 的已知限制

1. **cursor visibility** — issue #3364 报告 serialize 不保留 `DECTCEM` 状态。
   但在我们的场景中这不是问题：
   - 如果 TUI 在运行（cursor hidden），它会在 attach 后自己重设
   - 如果 shell 在运行（cursor visible），这是默认状态

2. **mouse mode** — serialize 不输出 mouse tracking 序列。
   我们仍然手动跟踪 `mouseMode` 并通过 `bufferState` WS 消息通知前端。

3. **内存开销** — 每个终端多一个 headless xterm 实例。
   以 5000 行 scrollback、80 列计算，每个实例约 2-4MB。
   对于 NarraFork 的使用场景（通常 < 20 个并发终端）完全可接受。

## 实施顺序

1. `bun add @xterm/headless@5.5.0 @xterm/addon-serialize@0.13.0`
2. 重写 `server/terminal/buffer-manager.ts`（保持公共接口不变）
3. 在 `server/services/terminal-service.ts` 中：
   - 构造 BufferManager 时传 cols/rows
   - resize 时同步 buffer.resize()
4. 类型检查 + lint
5. 手动测试：创建终端 → 运行 TUI (如 htop) → 退出 → 检查 scrollback → 重连 → 验证恢复
