import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { isAbsolute } from "node:path";
import type { EditorHelperCapabilities } from "../../shared/editor-provider-contracts";
import type { EditorRpc } from "../lib/agent/execution/rpc-types";

// Explicit native/compiled fixture only. No resolver, download, pool, Host receipt
// store or production consumer. A stage belongs to this one process incarnation.
export class EditorNativeSession implements EditorRpc {
	private readonly child: ChildProcessWithoutNullStreams;
	private pending?: { id: string; resolve(value: unknown): void; reject(error: Error): void };
	private buffer = "";
	private output = 0;
	private input = 0;
	private stderr = 0;
	private fault?: Error;
	private closed = false;
	private ended: Promise<void>;
	private deadline: ReturnType<typeof setTimeout>;
	static async open(binary: string, root: string, state: string, epoch: string) {
		if (![binary, root, state].every(isAbsolute))
			throw new Error("Native fixture requires explicit absolute paths");
		const session = new EditorNativeSession(binary, root, state, epoch);
		try {
			await session.capabilities();
			return session;
		} catch (error) {
			await session.close();
			throw error;
		}
	}
	private constructor(binary: string, root: string, state: string, epoch: string) {
		this.child = spawn(
			binary,
			["editor-stdio", "--root", root, "--state-dir", state, "--epoch", epoch],
			{ stdio: ["pipe", "pipe", "pipe"], windowsHide: true },
		);
		this.deadline = setTimeout(() => this.fail(new Error("Native fixture deadline")), 30_000);
		this.ended = new Promise((resolve) =>
			this.child.once("close", () => {
				this.closed = true;
				clearTimeout(this.deadline);
				this.fail(new Error("Native fixture ended"));
				resolve();
			}),
		);
		this.child.on("error", (error) => this.fail(error));
		this.child.stdin.on("error", (error) => this.fail(error));
		this.child.stdout.setEncoding("utf8");
		this.child.stdout.on("data", (chunk: string) => {
			this.output += Buffer.byteLength(chunk);
			this.buffer += chunk;
			if (this.output > 2 * 1024 * 1024 || this.buffer.length > 256 * 1024)
				return this.fail(new Error("Native fixture output limit"));
			for (;;) {
				const end = this.buffer.indexOf("\n");
				if (end < 0) break;
				const line = this.buffer.slice(0, end);
				this.buffer = this.buffer.slice(end + 1);
				try {
					if (Buffer.byteLength(line) > 128 * 1024) throw new Error("Native fixture frame limit");
					const frame = JSON.parse(line),
						pending = this.pending;
					if (
						!pending ||
						frame.type !== "rpc_result" ||
						frame.id !== pending.id ||
						typeof frame.ok !== "boolean"
					)
						throw new Error("Unexpected native reply");
					this.pending = undefined;
					if (frame.ok) pending.resolve(frame.result);
					else pending.reject(new Error(String(frame.error).slice(0, 512)));
				} catch (error) {
					this.fail(error instanceof Error ? error : new Error(String(error)));
				}
			}
		});
		this.child.stderr.on("data", (chunk: Buffer) => {
			this.stderr += chunk.length;
			if (this.stderr > 16384) this.fail(new Error("Native fixture stderr limit"));
		});
	}
	private fail(error: Error) {
		this.fault ??= error;
		this.pending?.reject(this.fault);
		this.pending = undefined;
		if (!this.closed && this.child.exitCode === null) this.child.kill();
	}
	async call<T>(method: string, params: object, signal?: AbortSignal): Promise<T> {
		signal?.throwIfAborted();
		if (this.fault) throw this.fault;
		if (this.pending) throw new Error("Native fixture accepts sequential RPC only");
		const id = randomUUID(),
			line = `${JSON.stringify({ type: "rpc", id, method, params })}\n`;
		this.input += Buffer.byteLength(line);
		if (Buffer.byteLength(line) > 128 * 1024 || this.input > 40 * 1024 * 1024)
			throw new Error("Native fixture input limit");
		const abort = () => this.fail(new Error("Native fixture RPC cancelled"));
		signal?.addEventListener("abort", abort, { once: true });
		const timer = setTimeout(() => this.fail(new Error("Native fixture RPC deadline")), 10_000);
		try {
			const reply = new Promise<T>((resolve, reject) => {
				this.pending = { id, resolve: (value) => resolve(value as T), reject };
			});
			void reply.catch(() => {});
			if (!this.child.stdin.write(line))
				await Promise.race([once(this.child.stdin, "drain"), reply]);
			return await reply;
		} finally {
			clearTimeout(timer);
			signal?.removeEventListener("abort", abort);
		}
	}
	async capabilities() {
		const capabilities = await this.call<EditorHelperCapabilities>("editor.capabilities", {});
		if (
			capabilities.protocolVersion !== 1 ||
			!capabilities.features.includes("editor.durable-write.v1")
		)
			throw new Error("Native fixture durable-write feature missing");
		return capabilities;
	}
	async close() {
		if (this.closed) return;
		clearTimeout(this.deadline);
		this.child.stdin.end();
		if (!this.closed && this.child.exitCode === null) this.child.kill();
		await this.ended;
	}
}
