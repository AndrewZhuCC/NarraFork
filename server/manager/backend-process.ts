/**
 * 后端服务子进程：启动、停止、重启，并把它的输出逐行交给调用方。
 *
 * 子进程就是同一个 exe 的 `--serve` 模式。编译产物里 `process.execPath` 指向 exe 自身；
 * 开发环境下指向 bun，此时用 `bun server/index.ts --serve` 的等价形式。
 *
 * 采用子进程而不是同进程双角色：重启必须是一次干净的重初始化（数据库、单实例锁、
 * 迁移、监听端口），子进程模型能直接表达它，同进程"重启"则要把状态全部拆掉再装回来。
 */

import { homedir } from "node:os";
import { join } from "node:path";

export type BackendState = "stopped" | "starting" | "running" | "stopping" | "error";

export interface BackendProcessOptions {
	host: string;
	port: number;
	/** 子进程的每一行输出（已去掉行尾）。 */
	onLine: (line: string) => void;
	/** 状态变化；`detail` 用于异常退出等需要说明的情形。 */
	onState: (state: BackendState, detail?: string) => void;
}

/** 健康检查间隔与启动超时。 */
const READY_POLL_MS = 700;
const READY_TIMEOUT_MS = 90_000;

/**
 * 健康检查用的本地探测地址。
 *
 * settings 里的 host 可以是 0.0.0.0 / ::（监听所有网卡），它们不能作为请求目标；
 * IPv6 字面量还要加方括号。修正后才能得到能真正连通的回环地址。
 */
function probeHost(host: string): string {
	if (host === "0.0.0.0" || host === "::" || host === "") return "127.0.0.1";
	if (host.includes(":") && !host.startsWith("[")) return `[${host}]`;
	return host;
}

/** 探测给定地址上是否已有 NarraFork 服务在响应（供识别外部启动的实例）。 */
export async function probeServiceHealth(host: string, port: number): Promise<boolean> {
	try {
		const response = await fetch(`http://${probeHost(host)}:${port}/api/health`, {
			signal: AbortSignal.timeout(1500),
		});
		return response.ok;
	} catch {
		return false;
	}
}

/** 当前是否运行在编译后的单文件产物里。 */
function isCompiledRuntime(): boolean {
	const url = import.meta.url;
	return url.includes("$bunfs/") || url.includes("%7EBUN/");
}

/** 数据目录：与 server 侧同一套环境变量约定。 */
export function resolveNarraforkHome(): string {
	const configured = process.env.NARRAFORK_HOME?.trim();
	return configured && configured.length > 0 ? configured : join(homedir(), ".narrafork");
}

export class BackendProcess {
	private child?: Bun.Subprocess<"pipe", "pipe", "pipe">;
	private state: BackendState = "stopped";
	private readyTimer?: ReturnType<typeof setTimeout>;
	private disposed = false;

	constructor(private readonly options: BackendProcessOptions) {}

	get current(): BackendState {
		return this.state;
	}

	get pid(): number | undefined {
		return this.child?.pid;
	}

	private setState(state: BackendState, detail?: string): void {
		this.state = state;
		this.options.onState(state, detail);
	}

	/** 启动后端；已在运行时直接返回。 */
	async start(): Promise<void> {
		if (this.child || this.disposed) return;
		this.setState("starting");
		const args = isCompiledRuntime() ? ["--serve"] : ["server/index.ts", "--serve"];
		try {
			this.child = Bun.spawn([process.execPath, ...args], {
				cwd: process.cwd(),
				windowsHide: true,
				stdout: "pipe",
				stderr: "pipe",
				env: { ...process.env },
			});
		} catch (error) {
			this.setState("error", error instanceof Error ? error.message : String(error));
			return;
		}

		const child = this.child;
		void this.pipeLines(child.stdout, "stdout");
		void this.pipeLines(child.stderr, "stderr");

		void child.exited.then((code) => {
			if (this.child !== child) return;
			this.child = undefined;
			clearTimeout(this.readyTimer);
			if (this.disposed) return;
			// 主动停止走的是 stop() 的路径；这里只处理"自己退出"。
			this.setState("stopped", code === 0 ? undefined : `退出码 ${code}`);
		});

		await this.waitUntilReady();
	}

	/** 轮询健康检查直到就绪；超时只报状态，不杀进程（服务可能仍在慢启动）。 */
	private async waitUntilReady(): Promise<void> {
		const deadline = Date.now() + READY_TIMEOUT_MS;
		while (Date.now() < deadline) {
			if (!this.child) return;
			try {
				const response = await fetch(
					`http://${probeHost(this.options.host)}:${this.options.port}/api/health`,
					{
						signal: AbortSignal.timeout(1500),
					},
				);
				if (response.ok) {
					this.setState("running");
					return;
				}
			} catch {
				// 还没起来，继续等。
			}
			await new Promise((resolve) => {
				this.readyTimer = setTimeout(resolve, READY_POLL_MS);
			});
		}
		if (this.child) this.setState("error", "启动超时：健康检查未通过");
	}

	/** 逐块解码子进程输出并按行回调；流结束时冲掉最后的不完整行。 */
	private async pipeLines(
		stream: ReadableStream<Uint8Array> | undefined,
		channel: "stdout" | "stderr",
	): Promise<void> {
		if (!stream) return;
		const decoder = new TextDecoder();
		let buffer = "";
		const reader = stream.getReader();
		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				const lines = buffer.split(/\r?\n/);
				buffer = lines.pop() ?? "";
				for (const line of lines) {
					if (line.length > 0) this.options.onLine(channel === "stderr" ? `! ${line}` : line);
				}
			}
			if (buffer.length > 0) this.options.onLine(buffer);
		} catch {
			// 子进程被终止时流会中断，属正常路径。
		} finally {
			reader.releaseLock();
		}
	}

	/** 停止后端并等待进程真正退出。 */
	async stop(): Promise<void> {
		const child = this.child;
		if (!child) {
			this.setState("stopped");
			return;
		}
		this.setState("stopping");
		this.child = undefined;
		clearTimeout(this.readyTimer);
		child.kill();
		await child.exited.catch(() => undefined);
		this.setState("stopped");
	}

	async restart(): Promise<void> {
		await this.stop();
		await this.start();
	}

	/** 管理器退出时调用：不等待、不回报状态。 */
	dispose(): void {
		this.disposed = true;
		clearTimeout(this.readyTimer);
		this.child?.kill();
		this.child = undefined;
	}
}
