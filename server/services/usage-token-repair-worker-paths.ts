import { isCompiledRuntime } from "@server/lib/runtime-target";

export type TokenRepairWorkerKind =
	| "usage-token-repair-worker"
	| "usage-token-repair-evidence-worker";
export function tokenRepairWorkerSpecifiers(
	kind: TokenRepairWorkerKind,
	compiled = isCompiledRuntime(),
	moduleUrl = import.meta.url,
): string[] {
	return (
		compiled
			? [`./services/${kind}.js`, `./server/services/${kind}.js`, `./${kind}.js`]
			: [`./${kind}.ts`]
	).map((specifier) => new URL(specifier, moduleUrl).href);
}
export function tokenRepairWorkerEntry(specifier: string): URL | string {
	const url = new URL(specifier);
	return url.protocol === "file:" && /^\/[a-z]:\/(?:~BUN|%7eBUN)\/root\//i.test(url.pathname)
		? decodeURIComponent(url.pathname.slice(1))
		: url;
}
