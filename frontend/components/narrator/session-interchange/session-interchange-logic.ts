/**
 * The decisions behind the import modal and export fields, kept free of React so
 * they can be tested directly: which sources/destinations to offer, which devices
 * count, and how a write result becomes what the user sees.
 *
 * The UI only hides what the server would refuse; the server re-checks every
 * request, so these are about not offering dead ends, not about security.
 */

import type { RemoteDevice } from "../../../lib/api/devices";
import type {
	SessionExportDownload,
	SessionExportWriteResult,
	SessionFormat,
	SessionWarning,
} from "../../../lib/api/session-interchange";

export type SessionTarget = "local" | "device";
export type ImportSource = "upload" | SessionTarget;
export type ExportDestination = "download" | SessionTarget;

export interface DeviceUseContext {
	/** The signed-in user; a private device is usable only by its registrar. */
	userId: string | null;
	/**
	 * The project the operation acts for. Import creates standalone narrators, so
	 * it passes nothing and only global devices qualify; export passes the
	 * narrator's project, which admits that project's devices too.
	 */
	projectId?: string | null;
}

/**
 * Devices the server will accept for session import/export, mirroring
 * `resolveSessionLocation`: online and unrevoked (`GET /devices` already limits
 * the list to devices the caller manages), usable by this user on the owner axis,
 * and in scope on the project axis.
 *
 * Without this, an admin (who manages every device) was offered other users'
 * private devices and project devices that the server then refused.
 */
export function usableSessionDevices(
	devices: readonly RemoteDevice[] | undefined,
	context: DeviceUseContext,
): RemoteDevice[] {
	return (devices ?? []).filter((device) => {
		if (device.status !== "online" || device.revokedAt) return false;
		const ownerScope = device.ownerScope ?? "shared";
		if (ownerScope === "private" && (!context.userId || device.createdBy !== context.userId)) {
			return false;
		}
		if (device.scope === "global") return true;
		return !!context.projectId && device.projectId === context.projectId;
	});
}

/**
 * The selected device, or null when it is no longer usable. Never falls back to
 * another device: a list refresh must not quietly retarget a write to a machine
 * the user did not pick.
 */
export function selectedSessionDevice(
	devices: readonly RemoteDevice[],
	selectedId: string | null,
): RemoteDevice | null {
	if (selectedId) return devices.find((device) => device.id === selectedId) ?? null;
	// Nothing chosen yet: a single candidate is unambiguous.
	return devices.length === 1 ? (devices[0] ?? null) : null;
}

/** Upload always; the server only for admins; a device only when one is usable. */
export function availableImportSources(isAdmin: boolean, deviceCount: number): ImportSource[] {
	return [
		"upload",
		...(isAdmin ? ["local" as const] : []),
		...(deviceCount > 0 ? ["device" as const] : []),
	];
}

/** Download always; the server only for admins; a device only when one is usable. */
export function availableExportDestinations(
	isAdmin: boolean,
	deviceCount: number,
): ExportDestination[] {
	return [
		"download",
		...(isAdmin ? ["local" as const] : []),
		...(deviceCount > 0 ? ["device" as const] : []),
	];
}

/** The active source, falling back to upload when it has disappeared (e.g. devices went offline). */
export function effectiveImportSource(
	current: ImportSource,
	available: readonly ImportSource[],
): ImportSource {
	return available.includes(current) ? current : "upload";
}

export interface ExportOutcome {
	resumeCommand: string | null;
	paths: string[];
	/** The cwd the server wrote into the session, not the input box's current value. */
	cwd: string | null;
	cwdExists: boolean;
	warnings: SessionWarning[];
	/** Download only: where the file(s) must be placed for the CLI to find them. */
	placement: { configDir: string; relativePath: string | null; isArchive: boolean } | null;
}

/**
 * A missing cwd gets its own alert with an explanation, so it is taken out of the
 * generic warning list rather than shown twice.
 */
export function writeOutcome(written: SessionExportWriteResult): ExportOutcome {
	return {
		resumeCommand: written.resumeCommand,
		paths: written.paths,
		cwd: written.cwd,
		cwdExists: written.cwdExists,
		warnings: written.warnings.filter((warning) => warning.kind !== "target_cwd_missing"),
		placement: null,
	};
}

export function sessionConfigDir(format: SessionFormat): string {
	return format === "claude-code" ? "~/.claude" : "~/.codex";
}

/**
 * A browser download lands in the Downloads folder with its directories stripped,
 * so the outcome says where it has to go before `--resume` can find it: the exact
 * path for a single file, or "extract into the config dir" for a zip (whose
 * entries already carry the layout).
 */
export function downloadOutcome(
	format: SessionFormat,
	download: Pick<SessionExportDownload, "resumeCommand" | "warnings" | "placement">,
): ExportOutcome {
	return {
		resumeCommand: download.resumeCommand,
		paths: [],
		cwd: null,
		// Not checked for a download: the file is placed on a machine we cannot see.
		cwdExists: true,
		warnings: download.warnings,
		placement: {
			configDir: sessionConfigDir(format),
			relativePath: download.placement,
			isArchive: download.placement === null,
		},
	};
}

/**
 * Upload list after a pick: new files are appended and a same-named file replaces
 * the earlier one. Claude Code keeps a session's sidechains in a different folder
 * from its transcript, and a native file picker selects within one folder, so the
 * files have to be collected over several picks.
 */
export function mergeUploadFiles(current: readonly File[], picked: readonly File[]): File[] {
	const byName = new Map(current.map((file) => [file.name, file]));
	for (const file of picked) byName.set(file.name, file);
	return [...byName.values()];
}

export function formatBytes(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
