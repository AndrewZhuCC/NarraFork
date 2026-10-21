import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

export const PROJECT_DIR_STATUS_QUERY_KEY = ["project-dir-status"] as const;

/**
 * Whether the instance's default project directory is set and still exists.
 *
 * The server never creates it, so a client must not treat `paths.defaultProjectDir` as a
 * usable working directory just because the setting is non-empty: it may name a folder that
 * was deleted or never existed. `usablePath` is null whenever the user has to choose one.
 * Saving settings must invalidate {@link PROJECT_DIR_STATUS_QUERY_KEY}.
 */
export function useProjectDirStatus() {
	const query = useQuery({
		queryKey: PROJECT_DIR_STATUS_QUERY_KEY,
		queryFn: api.fsProjectDirStatus,
		staleTime: 30_000,
	});
	return {
		status: query.data,
		/** The default directory when it exists; null when unset, missing, or still loading. */
		usablePath: query.data?.state === "valid" ? query.data.path : null,
		/** True once the server answered that the user must pick a directory. */
		needsChoice: query.data != null && query.data.state !== "valid",
		isLoading: query.isLoading,
	};
}
