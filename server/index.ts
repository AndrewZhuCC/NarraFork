import { DB_INTEGRITY_WORKER_FLAG } from "./db/integrity-protocol";
import { neutralizeAmbientProxyEnv } from "./lib/net/proxy-env";
import { waitForPreviousServerShutdown } from "./lib/restart-handoff";
import { WATCHER_WORKER_FLAG } from "./lib/watcher/worker-protocol";
import { PLUGIN_RUNTIME_WORKER_FLAG } from "./plugin-runtime-worker";

// Must run before ANY outbound fetch. Bun's fetch honours ambient HTTP(S)_PROXY
// even when a request explicitly asks for a direct connection, so the ambient
// values are snapshotted and blanked here to make the configured proxy policy
// the single authority. See lib/net/proxy-env.ts.
neutralizeAmbientProxyEnv();

if (process.argv.includes(DB_INTEGRITY_WORKER_FLAG)) {
	// Read-only integrity probe subprocess. Must be checked before anything else: importing ./main
	// (or the watcher worker) would open the database read-write and take the instance lock.
	await import("./db/integrity-probe-worker");
} else if (process.argv.includes(WATCHER_WORKER_FLAG)) {
	await import("./lib/watcher/parcel-watcher-worker");
} else if (process.argv.includes(PLUGIN_RUNTIME_WORKER_FLAG)) {
	// Self-hosted plugin runtime: the host binary embeds the Bun runtime, so
	// local-process plugins are spawned as this same exe in plugin mode —
	// before ./main, so no instance lock / DB / HTTP server. Loads the plugin
	// entry, which owns the stdio RPC channel.
	await import("./plugin-runtime-worker").then((m) => m.runPluginRuntimeWorker());
} else if (process.argv.includes("--serve") || process.platform !== "win32") {
	// 服务模式：管理器用它拉起后端，脚本调用与其它平台也走这里。
	const handoffOk = await waitForPreviousServerShutdown();
	if (!handoffOk) {
		process.exit(1);
	}
	await import("./main");
} else {
	// 默认进入图形化管理器。它自己不监听端口，所以不做"上一次服务退出等待"；
	// 真正的服务由管理器以 `--serve` 子进程形式拉起，那一步会做该等待。
	await import("./manager/main").then((manager) => manager.runManager());
}
