/**
 * The directory `POST /api/fs/reveal` should open for a file, or null when revealing it
 * is not offered. Reveal runs on the server desktop, never on a remote executor or browser.
 */
export function fileRevealDirectory(
	path: unknown,
	deviceId: string | undefined,
	platform: string,
	localOrigin: boolean,
): string | null {
	if (!localOrigin || !["windows", "macos", "linux"].includes(platform)) return null;
	if ((deviceId ?? "local") !== "local") return null;
	// Never resolve a relative path against the server process cwd. Virtual resources
	// (spec://) are not on-disk files either.
	if (typeof path !== "string" || !(/^[a-z]:[\\/]/i.test(path) || path.startsWith("/")))
		return null;
	// biome-ignore lint/suspicious/noControlCharactersInRegex: reject unsafe filesystem path characters
	if (/[\u0000-\u001f\u007f]/.test(path)) return null;
	// Paths are literal filesystem paths, not Markdown hrefs: `#` and line-looking
	// suffixes must remain part of the directory/file name.
	const windows = /^[a-z]:[\\/]/i.test(path);
	const normalized = windows ? path.replace(/\\/g, "/") : path;
	const rootLength = windows ? 3 : 1;
	let end = normalized.length;
	while (end > rootLength && normalized[end - 1] === "/") end--;
	const slash = normalized.lastIndexOf("/", end - 1);
	return slash < rootLength ? normalized.slice(0, rootLength) : normalized.slice(0, slash);
}
