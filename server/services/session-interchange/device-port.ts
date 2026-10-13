/**
 * The production {@link DeviceSessionFsPort}: file RPCs and chunked transfers on a
 * connected remote device.
 *
 * Kept apart from `session-fs.ts` so that module (and its tests) never import the
 * device connection layer. Authorization is NOT done here; the route checks that
 * the caller may manage the device before a port is ever built.
 */

import type { TransferStatResult } from "../../lib/agent/execution/rpc-types";
import { ValidationError } from "../../lib/errors";
import { settings } from "../../lib/settings";
import { getConnectedDeviceHello, isDeviceOnline, sendRpc } from "../device-connection-service";
import { downloadFile, statRemote, uploadFile } from "../device-transfer-service";
import type { DeviceSessionFsPort } from "./session-fs";

/** Per-RPC deadline: a stalled device must not hold an import request open forever. */
const RPC_TIMEOUT_MS = 30_000;

function toBase64(bytes: Uint8Array): string {
	return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).toString("base64");
}

export function createDeviceSessionFsPort(
	deviceId: string,
	signal?: AbortSignal,
): DeviceSessionFsPort {
	if (!isDeviceOnline(deviceId)) {
		throw new ValidationError(`Device ${deviceId} is offline`);
	}
	const hello = getConnectedDeviceHello(deviceId);
	const platformOs = hello?.platform.os;
	if (!platformOs) throw new ValidationError(`Device ${deviceId} has not reported its platform`);
	const rpc = (method: Parameters<typeof sendRpc>[1], params: Record<string, unknown>) =>
		sendRpc(deviceId, method, params, { signal, timeoutMs: RPC_TIMEOUT_MS });

	return {
		deviceId,
		platformOs,
		defaultCwd: hello?.defaultCwd?.trim() || null,
		maxRpcBytes: settings.devices?.maxRpcBytes ?? 10 * 1024 * 1024,
		async stat(path, opts) {
			return (await statRemote(deviceId, path, {
				recursive: opts.recursive,
				maxEntries: opts.maxEntries,
				signal,
				remotePlatformOs: platformOs,
			})) as TransferStatResult;
		},
		async read(path, maxBytes) {
			const res = (await rpc("fs.read", { path, maxBytes, timeoutMs: RPC_TIMEOUT_MS })) as {
				dataB64: string;
				truncated: boolean;
				totalSize: number;
			};
			return {
				bytes: new Uint8Array(Buffer.from(res.dataB64, "base64")),
				truncated: res.truncated,
				totalSize: res.totalSize,
			};
		},
		async write(path, bytes) {
			await rpc("fs.write", { path, dataB64: toBase64(bytes) });
		},
		async mkdirp(path) {
			await rpc("fs.mkdirp", { path });
		},
		async remove(path) {
			// Deliberately NOT bound to the request signal. Removal is how a failed export
			// rolls back, and the commonest failure is the client going away: an aborted
			// signal would reject every rollback RPC and leave half a session behind. The
			// RPC timeout still bounds it.
			await sendRpc(deviceId, "fs.remove", { path }, { timeoutMs: RPC_TIMEOUT_MS });
		},
		async download(path, size, mtimeMs, localDest) {
			await downloadFile({
				deviceId,
				remotePath: path,
				localDest,
				remoteSize: size,
				remoteMtimeMs: mtimeMs,
				remotePlatformOs: platformOs,
				signal,
			});
		},
		async upload(localPath, remoteDest) {
			await uploadFile({
				deviceId,
				localPath,
				remoteDest,
				remotePlatformOs: platformOs,
				signal,
			});
		},
	};
}
