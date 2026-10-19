/**
 * Keep `HKCU\Software\Classes\narrafork` pointing at this process (see reveal-protocol.ts).
 *
 * Per-user registration needs no administrator rights, which suits a portable single-file exe.
 * The command line names both this executable and this server's origin, so it is re-synced right
 * before each token is issued: a moved exe, a new port, or another NarraFork instance that
 * registered itself last are all corrected by the server that is about to hand out the URI.
 */

import { dlopen, FFIType } from "bun:ffi";
import { REVEAL_PROTOCOL_SCHEME, REVEAL_SERVER_FLAG } from "./reveal-protocol";
import { isCompiledRuntime } from "./runtime-target";

/** `(HKEY)(ULONG_PTR)(LONG)0x80000001`, sign-extended to 64 bits. */
const HKEY_CURRENT_USER = 0xffffffff80000001n;
const REG_SZ = 1;
const RRF_RT_REG_SZ = 0x2;
const PROTOCOL_KEY = `Software\\Classes\\${REVEAL_PROTOCOL_SCHEME}`;
const COMMAND_KEY = `${PROTOCOL_KEY}\\shell\\open\\command`;
const MAX_VALUE_BYTES = 8192;

type Advapi32 = ReturnType<typeof loadAdvapi32>;
let advapi32: Advapi32 | undefined;

function loadAdvapi32() {
	return dlopen("advapi32.dll", {
		RegGetValueW: {
			args: [
				FFIType.u64,
				FFIType.ptr,
				FFIType.ptr,
				FFIType.u32,
				FFIType.ptr,
				FFIType.ptr,
				FFIType.ptr,
			],
			returns: FFIType.i32,
		},
		RegSetKeyValueW: {
			args: [FFIType.u64, FFIType.ptr, FFIType.ptr, FFIType.u32, FFIType.ptr, FFIType.u32],
			returns: FFIType.i32,
		},
	}).symbols;
}

function wide(value: string): Buffer {
	return Buffer.from(`${value}\0`, "utf16le");
}

function readDefaultValue(api: Advapi32, subKey: string): string | null {
	const data = Buffer.alloc(MAX_VALUE_BYTES);
	const size = new Uint32Array([data.byteLength]);
	const status = api.RegGetValueW(
		HKEY_CURRENT_USER,
		wide(subKey),
		null,
		RRF_RT_REG_SZ,
		null,
		data,
		size,
	);
	return status === 0 ? data.toString("utf16le", 0, size[0]).replace(/\0+$/, "") : null;
}

function writeValue(api: Advapi32, subKey: string, name: string | null, value: string): boolean {
	const data = wide(value);
	return (
		api.RegSetKeyValueW(
			HKEY_CURRENT_USER,
			wide(subKey),
			name === null ? null : wide(name),
			REG_SZ,
			data,
			data.byteLength,
		) === 0
	);
}

/** `"<exe>" ["<entry script>"] "--reveal-server=<origin>" "%1"`; from source, exe is bun itself. */
export function revealHandlerCommand(
	serverOrigin: string,
	execPath = process.execPath,
	entryScript: string | null = isCompiledRuntime() ? null : Bun.main,
): string {
	return [execPath, entryScript, `${REVEAL_SERVER_FLAG}${serverOrigin}`, "%1"]
		.filter((part) => part !== null)
		.map((part) => `"${part}"`)
		.join(" ");
}

/** Point the protocol at this server; false when the registry could not be written. */
export function ensureRevealProtocolRegistered(serverOrigin: string): boolean {
	advapi32 ??= loadAdvapi32();
	const command = revealHandlerCommand(serverOrigin);
	if (readDefaultValue(advapi32, COMMAND_KEY) === command) return true;
	return (
		writeValue(advapi32, PROTOCOL_KEY, null, "URL:NarraFork Protocol") &&
		writeValue(advapi32, PROTOCOL_KEY, "URL Protocol", "") &&
		writeValue(advapi32, COMMAND_KEY, null, command)
	);
}
