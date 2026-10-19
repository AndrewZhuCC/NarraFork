import { Hono } from "hono";

export type WebDavFixtureRequest = { method: string; path: string; headers: Headers };
/** In-process real Hono HTTP semantics, injected fetch keeps production HTTPS validation intact. */
export function createWebDavFixture(
	options: {
		baseUrl?: string;
		intercept?: (request: Request) => Response | null | Promise<Response | null>;
	} = {},
) {
	const baseUrl = options.baseUrl ?? "https://dav.example.test/private/";
	const base = new URL(baseUrl).pathname;
	const objects = new Map<string, Uint8Array>();
	const collections = new Set<string>([base]);
	const requests: WebDavFixtureRequest[] = [];
	const app = new Hono();
	app.onError(() => new Response(null, { status: 500 }));
	app.all("*", async (c) => {
		const request = c.req.raw;
		const path = new URL(request.url).pathname;
		requests.push({ method: request.method, path, headers: new Headers(request.headers) });
		const intercepted = await options.intercept?.(request);
		if (intercepted) return intercepted;
		if (request.method === "MKCOL") {
			if (collections.has(path)) return new Response(null, { status: 405 });
			const parent = path.replace(/[^/]+\/$/, "");
			if (!collections.has(parent)) return new Response(null, { status: 409 });
			collections.add(path);
			return new Response(null, { status: 201 });
		}
		if (request.method === "PUT") {
			if (objects.has(path) && request.headers.get("if-none-match") === "*")
				return new Response(null, { status: 412 });
			if (!collections.has(path.replace(/[^/]+$/, ""))) return new Response(null, { status: 409 });
			objects.set(path, new Uint8Array(await request.arrayBuffer()));
			return new Response(null, { status: 201 });
		}
		if (request.method === "GET") {
			const bytes = objects.get(path);
			return bytes
				? new Response(new Uint8Array(bytes), {
						headers: { "content-length": String(bytes.length) },
					})
				: new Response(null, { status: 404 });
		}
		if (request.method === "DELETE") {
			if (!collections.has(path)) return new Response(null, { status: 404 });
			for (const name of collections) if (name.startsWith(path)) collections.delete(name);
			for (const name of objects.keys()) if (name.startsWith(path)) objects.delete(name);
			return new Response(null, { status: 204 });
		}
		if (request.method === "PROPFIND") {
			if (request.headers.get("depth") !== "1") return new Response(null, { status: 400 });
			if (!collections.has(path)) return new Response(null, { status: 404 });
			const entries: Array<[string, boolean]> = [[path, true]];
			for (const name of collections)
				if (
					name !== path &&
					name.startsWith(path) &&
					!name.slice(path.length).replace(/\/$/, "").includes("/")
				)
					entries.push([name, true]);
			for (const name of objects.keys())
				if (name.startsWith(path) && !name.slice(path.length).includes("/"))
					entries.push([name, false]);
			const bytes = webDavMultistatus(entries);
			return new Response(bytes, { status: 207, headers: { "content-type": "application/xml" } });
		}
		return new Response(null, { status: 405 });
	});
	const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const request = new Request(input, init);
		return app.fetch(request);
	}) as typeof fetch;
	return { baseUrl, objects, collections, requests, app, fetchImpl };
}
function xmlText(value: string): string {
	return value
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll("'", "&apos;")
		.replaceAll('"', "&quot;");
}
export function webDavMultistatus(entries: Array<[string, boolean]>): string {
	return `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:">${entries.map(([href, collection]) => `<d:response><d:href>${xmlText(href)}</d:href><d:propstat><d:prop><d:resourcetype>${collection ? "<d:collection/>" : ""}</d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`).join("")}</d:multistatus>`;
}
