/** settings.json 的 server.openBrowser。未知值按浏览器处理。 */
export type FrontendOpenMode = "off" | "browser" | "app";

/**
 * 决定这次要不要打开前端，以及用哪种窗口。
 *
 * 「不打开」只约束启动时的自动打开。托盘双击、再次启动、以及「打开 NarraFork」
 * 都是明确要求，回落成浏览器，否则点了没有反应。网页应用是 Chromium 的 --app 窗口。
 */
export function resolveFrontendOpen(options: {
	openBrowser: string;
	explicit?: boolean;
	forceNewWindow?: boolean;
}): "skip" | "browser" | "app" {
	if (options.openBrowser === "off" && !options.forceNewWindow && !options.explicit) return "skip";
	if (options.openBrowser === "app") return "app";
	return "browser";
}
