/**
 * 交给系统 shell 打开 URL / 文件（仅 Windows 分支使用）。
 *
 * 用 ShellExecuteW 而不是 `cmd /c start`：后者会多起一个控制台中间进程，个别时序下即使
 * windowsHide 也会闪一下黑窗。非 Windows 平台返回 false，由调用方沿用 open / xdg-open。
 */

import { dlopen, FFIType, type Pointer, ptr } from "bun:ffi";

const SW_SHOWNORMAL = 1;

let shell32:
	| {
			ShellExecuteW: (
				hwnd: Pointer,
				operation: Pointer,
				file: Pointer,
				params: Pointer,
				directory: Pointer,
				show: number,
			) => Pointer;
	  }
	| undefined;

function loadShell32(): typeof shell32 {
	if (shell32) return shell32;
	if (process.platform !== "win32") return undefined;
	try {
		shell32 = dlopen("shell32.dll", {
			ShellExecuteW: {
				args: [FFIType.ptr, FFIType.ptr, FFIType.ptr, FFIType.ptr, FFIType.ptr, FFIType.i32],
				returns: FFIType.ptr,
			},
		}).symbols as unknown as typeof shell32;
	} catch {
		shell32 = undefined;
	}
	return shell32;
}

/** UTF-16 以 NUL 结尾；ShellExecuteW 是同步调用，缓冲在调用期间由形参持有。 */
function wide(text: string): Pointer {
	return ptr(Buffer.from(`${text}\0`, "utf16le")) as Pointer;
}

/** 打开 URL / 文件；返回是否被 shell 受理（返回值 <= 32 皆为错误码）。 */
export function shellExecute(target: string, params?: string): boolean {
	const lib = loadShell32();
	if (!lib) return false;
	const result = lib.ShellExecuteW(
		0n as unknown as Pointer,
		wide("open"),
		wide(target),
		params === undefined ? (null as unknown as Pointer) : wide(params),
		null as unknown as Pointer,
		SW_SHOWNORMAL,
	);
	return Number(result) > 32;
}
