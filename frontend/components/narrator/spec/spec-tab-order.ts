import type { SpecFileMeta } from "../../../lib/api/spec";

/** 固定置顶的内置文档及其展示序；tasks.json 单独作为第一个标签处理。 */
export const FIXED_DOC_PATHS: readonly string[] = ["behavior_fence", "index.md"];

/**
 * 一个叙述者的大纲标签排布。
 *
 * `pinned` 是用户置顶的文档（按展示序），`order` 是其余文档的展示序。
 * 内置文档与 tasks.json 不参与这两个列表。
 */
export interface SpecTabLayout {
	order: string[];
	pinned: string[];
}

export const EMPTY_SPEC_TAB_LAYOUT: SpecTabLayout = { order: [], pinned: [] };

export type SpecTabGroup = "pinned" | "order";

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pathList(value: unknown): string[] {
	if (!Array.isArray(value)) return [];
	return value.filter((item): item is string => typeof item === "string");
}

function isSpecFile(file: SpecFileMeta | undefined): file is SpecFileMeta {
	return file !== undefined;
}

/** 已记录顺序的排在未记录的前面，同等时按路径名稳定排序。 */
function compareRanked(rank: Map<string, number>, a: string, b: string): number {
	const ra = rank.get(a) ?? Number.POSITIVE_INFINITY;
	const rb = rank.get(b) ?? Number.POSITIVE_INFINITY;
	return ra === rb ? a.localeCompare(b) : ra - rb;
}

/** 从用户偏好的整张映射中取出某个叙述者的排布；缺失或损坏时回退为空排布。 */
export function readSpecTabLayout(map: unknown, narratorId: string): SpecTabLayout {
	const entry = isRecord(map) ? map[narratorId] : undefined;
	if (!isRecord(entry)) return EMPTY_SPEC_TAB_LAYOUT;
	return { order: pathList(entry.order), pinned: pathList(entry.pinned) };
}

/**
 * 内置文档在前（顺序固定），其次置顶文档，最后其余文档；
 * 未出现在排布记录里的文档按服务端顺序补在末尾。
 */
export function orderSpecDocFiles(files: SpecFileMeta[], layout: SpecTabLayout): SpecFileMeta[] {
	const byPath = new Map(files.map((file) => [file.path, file]));
	const fixedPaths = new Set(FIXED_DOC_PATHS);
	const fixed = FIXED_DOC_PATHS.map((path) => byPath.get(path)).filter(isSpecFile);
	const pinnedPaths = new Set(layout.pinned);
	const pinned = layout.pinned
		.map((path) => byPath.get(path))
		.filter(isSpecFile)
		.filter((file) => !fixedPaths.has(file.path));
	const rank = new Map(layout.order.map((path, index) => [path, index]));
	const rest = files
		.filter((file) => !fixedPaths.has(file.path) && !pinnedPaths.has(file.path))
		.sort((a, b) => compareRanked(rank, a.path, b.path));
	return [...fixed, ...pinned, ...rest];
}

/** 置顶把文档挪到置顶组最前；取消置顶挪回普通组最前。 */
export function toggleSpecTabPin(
	layout: SpecTabLayout,
	path: string,
	pinned: boolean,
): SpecTabLayout {
	const order = layout.order.filter((item) => item !== path);
	const pinnedPaths = layout.pinned.filter((item) => item !== path);
	return pinned
		? { order, pinned: [path, ...pinnedPaths] }
		: { order: [path, ...order], pinned: pinnedPaths };
}

/** 用一次拖拽后的组内完整顺序替换排布。 */
export function reorderSpecTabGroup(
	layout: SpecTabLayout,
	group: SpecTabGroup,
	paths: string[],
): SpecTabLayout {
	return group === "pinned" ? { ...layout, pinned: paths } : { ...layout, order: paths };
}
