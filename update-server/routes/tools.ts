/** Historical helper files remain readable; unsigned binary publishing is retired. */
import { Hono } from "hono";
import { requireAuth } from "../lib/auth";
import type { StorageBackend } from "../storage/types";

export const MAX_TOOL_UPLOAD_BYTES = 64 * 1024 * 1024;
export function isSafeToolFilename(filename: string | undefined): boolean {
	if (!filename || filename.length > 255 || filename === "." || filename === "..") return false;
	return /^[A-Za-z0-9._-]+$/.test(filename);
}
export function createToolRoutes(storage: StorageBackend) {
	const routes = new Hono();
	routes.get("/:filename", async (c) => {
		const filename = c.req.param("filename");
		if (!isSafeToolFilename(filename)) return c.json({ error: "Invalid filename" }, 400);
		const path = `tools/${filename}`;
		const size = await storage.getFileSize(path);
		if (size === null) return c.json({ error: "Tool not found" }, 404);
		const stream = await storage.getFileStream(path);
		if (!stream) return c.json({ error: "Tool not found" }, 404);
		return new Response(stream, {
			headers: {
				"Content-Type": "application/octet-stream",
				"Content-Disposition": `attachment; filename="${filename}"`,
				"Content-Length": String(size),
			},
		});
	});
	routes.put("/:filename", requireAuth("upload"), (c) => {
		if (!isSafeToolFilename(c.req.param("filename")))
			return c.json({ error: "Invalid filename" }, 400);
		return c.json(
			{
				error:
					"Unsigned helper/executor uploads are deprecated; publish binaries to GitHub and mirror metadata via /api/v3/products/:product/mirror/tools/:kind/:tag",
			},
			410,
		);
	});
	return routes;
}
