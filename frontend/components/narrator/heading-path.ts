export type HeadingPathHeading = {
	level: number;
	text: string;
};

const MIN_HEADING_LEVEL = 1;
const MAX_HEADING_LEVEL = 6;
const MAX_DOM_HEADINGS = 512;
const MAX_DOM_NODES = 20_000;

function normalizeLevel(level: number): number | null {
	return Number.isInteger(level) && level >= MIN_HEADING_LEVEL && level <= MAX_HEADING_LEVEL
		? level
		: null;
}

/** Resolve the active heading stack from headings in document order. */
export function resolveHeadingPath(headings: readonly HeadingPathHeading[]): string[] {
	const stack: Array<HeadingPathHeading & { level: number }> = [];
	for (const heading of headings) {
		const level = normalizeLevel(heading.level);
		if (level == null) continue;
		while (stack.length > 0 && stack[stack.length - 1].level >= level) stack.pop();
		stack.push({ level, text: heading.text });
	}
	return stack.map(({ text }) => text);
}

function elementLevel(element: Element): number | null {
	const dataLevel = Number(element.getAttribute("data-level"));
	if (Number.isInteger(dataLevel)) return normalizeLevel(dataLevel);
	const match = /^H([1-6])$/u.exec(element.tagName.toUpperCase());
	return match ? Number(match[1]) : null;
}

/**
 * Resolve headings at or before a selection anchor, bounded to one Spec container.
 * The DOM walk is intentionally shallow in scope and capped to avoid scanning an
 * accidentally large surrounding document.
 */
export function resolveHeadingPathFromDom(
	anchorNode: Node | null,
	container: Element | null,
): string[] {
	if (!anchorNode || !container?.contains(anchorNode)) return [];
	const headings: HeadingPathHeading[] = [];
	let visitedNodes = 0;

	const visit = (node: Node): boolean => {
		visitedNodes += 1;
		if (visitedNodes > MAX_DOM_NODES) return true;
		if (node.nodeType === 1 && headings.length < MAX_DOM_HEADINGS) {
			const element = node as Element;
			const level = elementLevel(element);
			if (level != null) {
				headings.push({
					level,
					text: element.getAttribute("data-heading") ?? element.textContent ?? "",
				});
			}
		}
		if (node === anchorNode) return true;
		for (const child of Array.from(node.childNodes)) {
			if (visit(child)) return true;
		}
		return false;
	};

	visit(container);
	return resolveHeadingPath(headings);
}
