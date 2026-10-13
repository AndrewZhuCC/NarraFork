import { useQuery } from "@tanstack/react-query";
import { useCurrentUser } from "../../../hooks/useAuth";
import { api } from "../../../lib/api";
import type { RemoteDevice } from "../../../lib/api/devices";
import { usableSessionDevices } from "./session-interchange-logic";

export interface SessionTargets {
	isAdmin: boolean;
	/** See `usableSessionDevices`; the server still re-checks every request. */
	devices: RemoteDevice[];
}

/**
 * Where the caller may scan, import from, or export to.
 *
 * `projectId` is the project the operation acts for: omit it for import (which
 * creates standalone narrators), pass the narrator's project for export.
 */
export function useSessionTargets(enabled: boolean, projectId?: string | null): SessionTargets {
	const { data: user } = useCurrentUser();
	const { data: devices } = useQuery({
		queryKey: ["devices"],
		queryFn: () => api.listDevices(),
		enabled,
		staleTime: 10_000,
	});
	const userId = typeof user?.id === "string" ? user.id : null;
	return {
		isAdmin: user?.role === "admin",
		devices: usableSessionDevices(devices, { userId, projectId }),
	};
}
