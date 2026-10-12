import { describe, expect, it } from "bun:test";
import type { DockviewApi, IDockviewPanel } from "dockview-react";
import { closeSurfaceTabs, getTabCloseTargets } from "./tab-close";

function fixture() {
	const closed: string[] = [];
	const panels: IDockviewPanel[] = [];
	for (const entries of [
		[
			["chat", "chat"],
			["a", "terminal"],
			["current", "file"],
			["n1", "narrator"],
			["s1", "subagent"],
		],
		[
			["n2", "narrator"],
			["b", "webview"],
			["s2", "subagent"],
		],
	]) {
		const group = { panels: [] as IDockviewPanel[] };
		for (const [id, component] of entries) {
			const panel = {
				id,
				group,
				api: {
					component,
					close: () => {
						closed.push(id);
						panels.splice(panels.indexOf(panel), 1);
						group.panels.splice(group.panels.indexOf(panel), 1);
					},
				},
			} as unknown as IDockviewPanel;
			group.panels.push(panel);
			panels.push(panel);
		}
	}
	const api = { panels, getPanel: (id: string) => panels.find((p) => p.id === id) } as DockviewApi;
	return { api, closed };
}

const ids = (panels: IDockviewPanel[]) => panels.map((p) => p.id);
describe("surface tab closing", () => {
	it("closes left/right by current group order, excluding current and protected chat", () => {
		const { api } = fixture();
		expect(ids(getTabCloseTargets(api, "current", "left"))).toEqual(["a"]);
		expect(ids(getTabCloseTargets(api, "current", "right"))).toEqual(["n1", "s1"]);
		expect(getTabCloseTargets(api, "chat", "left")).toEqual([]);
		expect(getTabCloseTargets(api, "s1", "right")).toEqual([]);
	});
	it("uses live reordered group order rather than surface panel order", () => {
		const { api } = fixture();
		api.getPanel("current")?.group.panels.reverse();
		expect(ids(getTabCloseTargets(api, "current", "left"))).toEqual(["s1", "n1"]);
	});
	it("close all retains chat and all workspace narrators across groups", () => {
		const { api, closed } = fixture();
		closeSurfaceTabs(api, "current", "all");
		expect(closed).toEqual(["a", "current", "s1", "b", "s2"]);
		expect(ids(api.panels)).toEqual(["chat", "n1", "n2"]);
	});
	for (const panelId of ["current", "s1"]) {
		it(`close others retains the clicked ${panelId} tab and protected narrators across groups`, () => {
			const { api, closed } = fixture();
			Object.defineProperty(api, "activePanel", { value: api.getPanel("a") });
			closeSurfaceTabs(api, panelId, "others");
			expect(closed).toEqual(["a", "current", "s1", "b", "s2"].filter((id) => id !== panelId));
			expect(ids(api.panels).sort()).toEqual(["chat", panelId, "n1", "n2"].sort());
			expect(getTabCloseTargets(api, panelId, "others")).toEqual([]);
		});
	}
	it("close auxiliary retains subagents too, even when invoked from protected chat", () => {
		const { api, closed } = fixture();
		closeSurfaceTabs(api, "chat", "auxiliary");
		expect(closed).toEqual(["a", "current", "b"]);
		expect(ids(api.panels)).toEqual(["chat", "n1", "s1", "n2", "s2"]);
	});
	it("ignores a tab that disappeared while its menu was open", () => {
		const { api, closed } = fixture();
		closeSurfaceTabs(api, "missing", "all");
		expect(closed).toEqual([]);
	});
});
