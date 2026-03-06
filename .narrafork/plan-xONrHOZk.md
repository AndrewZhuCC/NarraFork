# 终端系统重构计划 — 参考 VSCode 分层架构

## 设计理念

参考 VSCode 终端系统的三层解耦架构（Process → Instance → Service），将当前混杂在 `terminal-service.ts` 中的 PTY 管理、缓冲区管理、生命周期管理拆分为独立的类，每层职责清晰。

**核心原则：**
- TerminalProcess 只管 PTY I/O 和进程生命周期
- TerminalInstance 持有 Process + Buffer，是单个终端的完整抽象
- TerminalService 是全局管理器，持有所有 Instance，对外暴露 API
- 外部接口（路由、WS、事件总线）保持 100% 兼容，不改前端

## 当前问题

1. `terminal-service.ts` 是 647 行的巨型对象字面量，混合了 PTY 创建、缓冲区管理、dtach 恢复、进程监控、DB 操作
2. `activeTerminals` Map 直接存储 `{ process, pty, buffer, useDtach }` 原始字段，没有封装
3. dtach attach/detach 的退出监听逻辑重复了 3 次（create、reattach、recoverOnStartup）
4. resize 链路中 buffer.resize() 是 async 但被 fire-and-forget 调用
5. BufferManager 的 write 异步链增加了不必要的复杂度

## 新架构

```
server/terminal/
  terminal-process.ts     — PTY 进程封装（直接模式 + dtach 模式）
  terminal-instance.ts    — 单个终端实例（Process + Buffer + 状态）
  buffer-manager.ts       — xterm-headless 缓冲区（保留，微调）
  dtach-service.ts        — dtach 工具函数（保留不动）

server/services/
  terminal-service.ts     — 全局终端管理（瘦身为编排层）
  terminal-tab-service.ts — 不动
  terminal-view-service.ts — 不动
```

## 详细设计

### 第 1 层：TerminalProcess（`server/terminal/terminal-process.ts`）

封装 PTY 进程的创建和 I/O，屏蔽直接模式 vs dtach 模式的差异。

```typescript
interface TerminalProcessEvents {
  data: (text: string) => void;
  exit: (code: number) => void;
}

interface TerminalProcessOptions {
  terminalId: string;
  cwd: string;
  cols: number;
  rows: number;
  env?: Record<string, string>;
}

class TerminalProcess extends TypedEmitter<TerminalProcessEvents> {
  readonly pid: number | null;
  readonly useDtach: boolean;

  // 工厂方法
  static create(opts: TerminalProcessOptions): Promise<TerminalProcess>;
  static attach(terminalId: string, cols: number, rows: number): TerminalProcess;

  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(): void;
  dispose(): void;
}
```

**关键设计：**
- `create()` 自动选择 dtach 或直接模式
- `attach()` 用于 reattach dtach 会话
- dtach 模式下 exit 事件只在 socket 真正死亡时触发（不是 attach 进程退出时）
- 退出监听逻辑只写一次，在 TerminalProcess 内部

### 第 2 层：TerminalInstance（`server/terminal/terminal-instance.ts`）

单个终端的完整抽象，持有 Process 和 Buffer。

```typescript
interface TerminalInstanceEvents {
  data: (text: string) => void;
  exit: (code: number) => void;
  disposed: () => void;
}

interface TerminalInstanceOptions {
  id: string;
  cwd: string;
  cols: number;
  rows: number;
  chapterId: string | null;
  narratorId: string | null;
  name: string;
}

class TerminalInstance extends TypedEmitter<TerminalInstanceEvents> {
  readonly id: string;
  readonly chapterId: string | null;
  readonly narratorId: string | null;
  name: string;

  private process: TerminalProcess | null;
  private buffer: BufferManager;

  // 工厂方法
  static async create(opts: TerminalInstanceOptions): Promise<TerminalInstance>;
  static async restore(terminalId: string, cols: number, rows: number): Promise<TerminalInstance | null>;

  write(data: string): void;
  resize(cols: number, rows: number): void;
  async getScrollback(): Promise<{ data: string; cols: number; rows: number } | null>;
  getBufferState(): { mouseTracking: boolean; cursorVisible: boolean };
  dispose(deleteBuffer?: boolean): void;

  // 进程管理
  get isAttached(): boolean;
  attachProcess(process: TerminalProcess): void;
  detachProcess(): void;
}
```

**关键设计：**
- `create()` 内部创建 TerminalProcess + BufferManager，连接数据流
- `restore()` 从磁盘加载 buffer，尝试 reattach dtach
- 数据流：Process.onData → buffer.append() + emit('data')
- Process.onExit → emit('exit')
- dispose() 清理 process + buffer

### 第 3 层：TerminalService（`server/services/terminal-service.ts`）

瘦身为编排层，管理所有 TerminalInstance 的生命周期 + DB 操作。

```typescript
// 保持完全相同的公共 API 签名
const terminalService = {
  // 创建 — 解析 cwd，创建 Instance，写 DB，发事件
  async create(opts): Promise<Terminal>;

  // I/O — 委托给 Instance
  write(terminalId, data): void;
  resize(terminalId, cols, rows): void;

  // 生命周期 — 委托给 Instance + DB 操作
  async kill(terminalId): Promise<void>;
  async rename(terminalId, name): Promise<void>;

  // 查询 — 纯 DB
  async getById(id): Promise<Terminal>;
  async listByChapter(chapterId): Promise<Terminal[]>;
  async listByNarrator(narratorId): Promise<Terminal[]>;
  async listAll(): Promise<Terminal[]>;

  // 缓冲区 — 委托给 Instance
  async getScrollback(terminalId): Promise<{data, cols, rows} | null>;
  getBufferState(terminalId): {mouseTracking, cursorVisible} | null;

  // dtach 管理 — 使用 Instance.restore()
  async reattach(terminalId): Promise<boolean>;
  async reattachOrphan(terminalId): Promise<unknown>;
  listOrphanSockets(): {socketPath, terminalId}[];
  killOrphanSocket(terminalId): void;
  isAttached(terminalId): boolean;
  getAttachedSet(): Set<string>;

  // 进程查询
  getShellPid(terminalId): number | null;
  getProcesses(terminalId): TerminalProcessInfo[];

  // 清理
  async cleanupForChapter(chapterId): Promise<void>;
  async cleanupForNarrator(narratorId): Promise<void>;
  async recoverOnStartup(): Promise<void>;
};
```

**关键：公共 API 签名完全不变**，所有外部调用者（routes、ws、admin、chapter-service 等）零改动。

### BufferManager 调整

保留现有实现，但简化异步模型：
- `append()` 保持同步（fire-and-forget write），因为 PTY 输出是连续流，不需要等每次 write 完成
- `getContents()` 保持 async（await writeChain）
- `resize()` 保持 async（await writeChain）
- 移除 `flush()` 公共方法（内部使用即可）

### TypedEmitter 工具

新增一个轻量的类型化事件发射器，替代 Node EventEmitter 的 string 类型：

```typescript
// server/terminal/typed-emitter.ts
class TypedEmitter<Events extends Record<string, (...args: any[]) => void>> {
  on<K extends keyof Events>(event: K, handler: Events[K]): this;
  off<K extends keyof Events>(event: K, handler: Events[K]): this;
  emit<K extends keyof Events>(event: K, ...args: Parameters<Events[K]>): void;
  removeAllListeners(): void;
}
```

## 实施步骤

### Step 1: 创建 TypedEmitter 工具
- 新建 `server/terminal/typed-emitter.ts`
- 基于 Node EventEmitter 的薄封装，提供类型安全

### Step 2: 创建 TerminalProcess
- 新建 `server/terminal/terminal-process.ts`
- 从 terminal-service.ts 中提取 PTY 创建、dtach attach、退出监听逻辑
- 统一直接模式和 dtach 模式的退出处理（消除 3 处重复代码）

### Step 3: 创建 TerminalInstance
- 新建 `server/terminal/terminal-instance.ts`
- 组合 TerminalProcess + BufferManager
- 实现 create/restore 工厂方法
- 实现数据流连接和生命周期管理

### Step 4: 重写 TerminalService
- 将 terminal-service.ts 瘦身为编排层
- 用 `Map<string, TerminalInstance>` 替代 `Map<string, ActiveTerminal>`
- 所有 I/O 和缓冲区操作委托给 Instance
- 保持完全相同的公共 API

### Step 5: 微调 BufferManager
- 简化：移除不需要的公共方法
- 确保 getContents() 和 resize() 的异步正确性

### Step 6: 验证
- `bunx tsc --noEmit` 类型检查
- `bunx @biomejs/biome check .` lint
- 确认所有外部调用者无需改动

## 不改动的文件

- `server/terminal/dtach-service.ts` — 保持不动
- `server/services/terminal-tab-service.ts` — 保持不动
- `server/services/terminal-view-service.ts` — 保持不动
- `server/routes/terminals.ts` — 保持不动
- `server/routes/admin.ts` — 保持不动
- `server/websocket/terminal-ws.ts` — 保持不动
- `server/lib/event-bus.ts` — 保持不动
- 所有前端文件 — 保持不动
- DB schema — 保持不动
