/** 下载接口拒绝超过 64 KiB 的请求体。面板只能回传身份，不能把发行说明整段带上。 */
export const MANAGER_UPDATE_DOWNLOAD_LIMIT = 64 * 1024;

export interface ManagerUpdateDownloadBody {
	releaseInfo: {
		version?: unknown;
		sha512?: unknown;
		sourceIdentity?: unknown;
	};
}

/** 从检查更新的 releaseInfo 里抽出下载接口要的三个字段。 */
export function managerUpdateDownloadBody(releaseInfo: unknown): ManagerUpdateDownloadBody {
	const info =
		releaseInfo && typeof releaseInfo === "object" && !Array.isArray(releaseInfo)
			? (releaseInfo as Record<string, unknown>)
			: {};
	return {
		releaseInfo: {
			version: info.version,
			sha512: info.sha512,
			sourceIdentity: info.sourceIdentity,
		},
	};
}
