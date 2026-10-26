import { BUILD_GITHUB_REPOSITORY } from "./build-repository";

export type UpdateSource = "github" | "update-server";

export interface UpdateSourceSettings {
	source?: UpdateSource;
	githubRepository?: string;
	serverUrl?: string;
	product?: string;
	channel?: "stable" | "beta";
}

const DEFAULT_UPDATE_SERVER_URL = "https://narrafork-update.b.domexie.cn";

export function normalizeUpdateServerUrl(value?: string): string {
	const url = value?.trim() || DEFAULT_UPDATE_SERVER_URL;
	try {
		const parsed = new URL(url);
		parsed.pathname = parsed.pathname.replace(/\/+$/, "");
		return parsed.toString().replace(/\/+$/, "");
	} catch {
		return url.replace(/\/+$/, "");
	}
}

/** Shared check identity and atomic settings-write precondition; no secrets are included. */
export function updateSettingsKey(update?: UpdateSourceSettings): string {
	const source = update?.source ?? "github";
	return JSON.stringify([
		source,
		update?.channel ?? "stable",
		(update?.githubRepository ?? BUILD_GITHUB_REPOSITORY).trim().toLowerCase(),
		...(source === "github"
			? []
			: [normalizeUpdateServerUrl(update?.serverUrl), update?.product ?? "narrafork"]),
	]);
}
