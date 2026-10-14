import { describe, expect, it } from "bun:test";
import type { SpecFileMeta } from "../../../lib/api/spec";
import {
	EMPTY_SPEC_TAB_LAYOUT,
	orderSpecDocFiles,
	readSpecTabLayout,
	reorderSpecTabGroup,
	toggleSpecTabPin,
} from "./spec-tab-order";

function file(path: string): SpecFileMeta {
	return {
		path,
		uri: `spec://${path}`,
		readonly: false,
		uiEditable: true,
		builtin: false,
		revisionId: null,
	};
}

const pathsOf = (...paths: string[]) => paths.map(file);

describe("orderSpecDocFiles", () => {
	it("内置文档固定在最前，且不受命名与置顶状态影响", () => {
		const ordered = orderSpecDocFiles(pathsOf("aaa.md", "index.md", "zzz.md", "behavior_fence"), {
			order: ["aaa.md"],
			pinned: ["index.md", "zzz.md"],
		});
		expect(ordered.map((f) => f.path)).toEqual(["behavior_fence", "index.md", "zzz.md", "aaa.md"]);
	});

	it("置顶文档按置顶顺序排在其余文档之前", () => {
		const ordered = orderSpecDocFiles(
			pathsOf("a.md", "b.md", "c.md", "behavior_fence", "index.md"),
			{
				order: ["a.md", "b.md", "c.md"],
				pinned: ["c.md", "a.md"],
			},
		);
		expect(ordered.map((f) => f.path)).toEqual([
			"behavior_fence",
			"index.md",
			"c.md",
			"a.md",
			"b.md",
		]);
	});

	it("未记录的文档按服务端顺序补在末尾", () => {
		const ordered = orderSpecDocFiles(pathsOf("a.md", "b.md", "c.md"), {
			order: ["b.md"],
			pinned: [],
		});
		expect(ordered.map((f) => f.path)).toEqual(["b.md", "a.md", "c.md"]);
	});

	it("忽略已删除文档的排布记录，且重复记录时置顶优先", () => {
		const ordered = orderSpecDocFiles(pathsOf("a.md", "b.md"), {
			order: ["gone.md", "a.md", "b.md"],
			pinned: ["b.md"],
		});
		expect(ordered.map((f) => f.path)).toEqual(["b.md", "a.md"]);
	});
});

describe("toggleSpecTabPin", () => {
	it("置顶把文档挪到置顶组最前并从普通组移除", () => {
		const next = toggleSpecTabPin({ order: ["a.md", "b.md"], pinned: ["c.md"] }, "b.md", true);
		expect(next).toEqual({ order: ["a.md"], pinned: ["b.md", "c.md"] });
	});

	it("取消置顶挪回普通组最前", () => {
		const next = toggleSpecTabPin({ order: ["a.md"], pinned: ["b.md", "c.md"] }, "b.md", false);
		expect(next).toEqual({ order: ["b.md", "a.md"], pinned: ["c.md"] });
	});
});

describe("reorderSpecTabGroup", () => {
	it("整体替换指定组的顺序", () => {
		const layout = { order: ["a.md", "b.md"], pinned: ["c.md", "d.md"] };
		expect(reorderSpecTabGroup(layout, "order", ["b.md", "a.md"])).toEqual({
			order: ["b.md", "a.md"],
			pinned: ["c.md", "d.md"],
		});
		expect(reorderSpecTabGroup(layout, "pinned", ["d.md", "c.md"])).toEqual({
			order: ["a.md", "b.md"],
			pinned: ["d.md", "c.md"],
		});
	});
});

describe("readSpecTabLayout", () => {
	it("读取指定叙述者的排布", () => {
		const layout = readSpecTabLayout(
			{ n1: { order: ["a.md"], pinned: ["b.md"] }, n2: { order: [], pinned: [] } },
			"n1",
		);
		expect(layout).toEqual({ order: ["a.md"], pinned: ["b.md"] });
	});

	it("缺失或损坏的数据回退为空排布", () => {
		expect(readSpecTabLayout(undefined, "n1")).toEqual(EMPTY_SPEC_TAB_LAYOUT);
		expect(readSpecTabLayout({}, "n1")).toEqual(EMPTY_SPEC_TAB_LAYOUT);
		expect(readSpecTabLayout({ n1: "broken" }, "n1")).toEqual(EMPTY_SPEC_TAB_LAYOUT);
		expect(readSpecTabLayout({ n1: { order: [1, "a.md"], pinned: null } }, "n1")).toEqual({
			order: ["a.md"],
			pinned: [],
		});
	});
});
