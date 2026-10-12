import { describe, expect, test } from "bun:test";
import { parseGitIdentityVisibility } from "./useGitIdentityVisibility";

describe("parseGitIdentityVisibility", () => {
	test("缺失或损坏的存储值都回落到全部关闭", () => {
		const off = { contextMenu: false, createModal: false };
		expect(parseGitIdentityVisibility(null)).toEqual(off);
		expect(parseGitIdentityVisibility("")).toEqual(off);
		expect(parseGitIdentityVisibility("{")).toEqual(off);
	});

	test("两个开关互相独立，可以同时开启", () => {
		expect(parseGitIdentityVisibility('{"contextMenu":true}')).toEqual({
			contextMenu: true,
			createModal: false,
		});
		expect(parseGitIdentityVisibility('{"createModal":true}')).toEqual({
			contextMenu: false,
			createModal: true,
		});
		expect(parseGitIdentityVisibility('{"contextMenu":true,"createModal":true}')).toEqual({
			contextMenu: true,
			createModal: true,
		});
	});

	test("非布尔值一律按关闭处理", () => {
		expect(parseGitIdentityVisibility('{"contextMenu":"yes","createModal":1}')).toEqual({
			contextMenu: false,
			createModal: false,
		});
	});
});
