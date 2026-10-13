/**
 * Turn a scan/import/export-to location into a filesystem and a CLI config root,
 * enforcing who may touch which machine.
 *
 * The rules, and why:
 *  - **Server**: admins only. `~/.claude` and `~/.codex` belong to the OS account
 *    NarraFork runs as, so they hold every session anyone ran on this host.
 *  - **Device**: the caller must be able to manage the device (its registrar or an
 *    admin) AND be authorized to use it (the owner/scope axes). Management alone is
 *    not enough: it does not imply the device is meant for this caller's work.
 *
 * A device that is unknown or not the caller's reports NotFound either way, so the
 * endpoint cannot be used to probe device ids.
 */

import { isAbsolute } from "node:path";
import type { InterchangeFormat } from "@shared/session-interchange/model";
import { ForbiddenError, NotFoundError, ValidationError } from "../../lib/errors";
import type { SessionLocationInput } from "../../lib/validators";
import { canManageDevice, getDevice, isDeviceAuthorized } from "../device-service";
import {
	createDeviceSessionFs,
	createLocalSessionFs,
	type DeviceSessionFsPort,
	inferRemoteHome,
	isAbsoluteFor,
	localClaudeCodeRoot,
	localCodexRoot,
	type SessionFs,
} from "./session-fs";

export interface SessionPrincipal {
	userId: string;
	isAdmin: boolean;
}

export interface ResolvedLocation {
	fs: SessionFs;
	root: string;
}

/** Injectable so tests can supply an in-memory device instead of a live connection. */
export type DevicePortFactory = (deviceId: string, signal?: AbortSignal) => DeviceSessionFsPort;

let devicePortOverride: DevicePortFactory | null = null;

/**
 * Replace how device ports are built, for tests that exercise routes without a live
 * executor connection (the same seam as `setRemoteBackendResolver`). Pass null to
 * restore the real connection layer.
 */
export function setSessionDevicePortFactory(factory: DevicePortFactory | null): void {
	devicePortOverride = factory;
}

async function defaultDevicePortFactory(): Promise<DevicePortFactory> {
	if (devicePortOverride) return devicePortOverride;
	// Deferred: the device connection layer is heavy and irrelevant to local use.
	const { createDeviceSessionFsPort } = await import("./device-port");
	return createDeviceSessionFsPort;
}

function configDirName(format: InterchangeFormat): string {
	return format === "claude-code" ? ".claude" : ".codex";
}

export async function resolveSessionLocation(
	location: SessionLocationInput,
	format: InterchangeFormat,
	principal: SessionPrincipal,
	opts: {
		signal?: AbortSignal;
		devicePorts?: DevicePortFactory;
		/**
		 * Project the operation acts for. Export passes the narrator's project so a
		 * project-scoped device of that project qualifies; scan and import create
		 * standalone narrators and pass nothing, which admits global devices only.
		 */
		projectId?: string | null;
	} = {},
): Promise<ResolvedLocation> {
	if (location.target === "local") {
		if (!principal.isAdmin) {
			throw new ForbiddenError("Only administrators can access session files on the server");
		}
		const root =
			location.root ?? (format === "claude-code" ? localClaudeCodeRoot() : localCodexRoot());
		if (!isAbsolute(root)) throw new ValidationError("Session directory must be an absolute path");
		return { fs: createLocalSessionFs(), root };
	}

	const device = await getDevice(location.deviceId);
	const manageable =
		device && canManageDevice(device, { userId: principal.userId, isAdmin: principal.isAdmin });
	if (!device || !manageable) throw new NotFoundError("Device", location.deviceId);
	if (
		!isDeviceAuthorized(device, { projectId: opts.projectId ?? null, userId: principal.userId })
	) {
		throw new ForbiddenError("This device is not authorized for this operation");
	}

	const factory = opts.devicePorts ?? (await defaultDevicePortFactory());
	const port = factory(device.id, opts.signal);
	const fs = createDeviceSessionFs(port);
	let root = location.root;
	if (!root) {
		const home = inferRemoteHome(port.defaultCwd, port.platformOs);
		if (!home) {
			throw new ValidationError(
				"Could not infer the device's home directory; enter the session directory explicitly",
			);
		}
		root = fs.join(home, configDirName(format));
	}
	if (!isAbsoluteFor(fs.flavor, root)) {
		throw new ValidationError("Session directory must be an absolute path on the device");
	}
	return { fs, root };
}
