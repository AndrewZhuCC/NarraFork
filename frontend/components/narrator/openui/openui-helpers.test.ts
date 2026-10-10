import { describe, expect, test } from "bun:test";
import {
	type ActionEvent,
	createParser,
	type ElementNode,
	type ParseResult,
} from "@openuidev/react-lang";
import { installOpenUIUUIDFallback } from "./openui-browser-compat";
import {
	buildOpenUIActionMessage,
	createOpenUIStateCache,
	MAX_OPENUI_MESSAGE_BYTES,
	MAX_OPENUI_SESSIONS,
	MAX_OPENUI_SOURCE_BYTES,
	MAX_OPENUI_STATE_BYTES,
	type OpenUIControlEvidence,
	openUIFieldKey,
	projectOpenUIFormValues,
	truncateUtf8,
	withinUtf8Limit,
} from "./openui-helpers";
import { narratorOpenUIChatLibrary as openuiChatLibrary } from "./openui-library";

const action: ActionEvent = {
	type: "continue_conversation",
	humanFriendlyMessage: "提交选择",
	params: { context: "hidden instruction", secret: "do not forward" },
};

describe("OpenUI bounds and state", () => {
	test("supports citation UUIDs over HTTP without replacing a native implementation", () => {
		const target: Pick<Crypto, "getRandomValues"> & Partial<Pick<Crypto, "randomUUID">> = {
			getRandomValues: crypto.getRandomValues.bind(crypto),
		};
		installOpenUIUUIDFallback(target);
		const id = target.randomUUID?.();
		if (!id) throw new Error("Expected the HTTP UUID fallback");
		expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
		const installed = target.randomUUID;
		installOpenUIUUIDFallback(target);
		expect(target.randomUUID).toBe(installed);
		const native = crypto.randomUUID;
		installOpenUIUUIDFallback(crypto);
		expect(crypto.randomUUID).toBe(native);
	});

	test("enforces the UTF-8 source limit, including the quick giant-string rejection", () => {
		expect(withinUtf8Limit("a".repeat(MAX_OPENUI_SOURCE_BYTES), MAX_OPENUI_SOURCE_BYTES)).toBe(
			true,
		);
		expect(withinUtf8Limit("a".repeat(1_000_000), MAX_OPENUI_SOURCE_BYTES)).toBe(false);
		const chinese = "界".repeat(Math.floor(MAX_OPENUI_SOURCE_BYTES / 3));
		expect(withinUtf8Limit(chinese, MAX_OPENUI_SOURCE_BYTES)).toBe(true);
		expect(withinUtf8Limit(`${chinese}界`, MAX_OPENUI_SOURCE_BYTES)).toBe(false);
	});

	test("truncation stays bounded and never cuts a UTF-8 code point", () => {
		for (const text of ["a界b", "a\u{1f680}b", "界".repeat(100_000)]) {
			for (const limit of [0, 1, 2, 3, 4, 5, MAX_OPENUI_SOURCE_BYTES]) {
				const result = truncateUtf8(text, limit);
				expect(withinUtf8Limit(result, limit)).toBe(true);
				expect(text.startsWith(result)).toBe(true);
				expect(result).not.toContain("\ufffd");
			}
		}
	});

	test("restores independent form-state clones and actual Date values", () => {
		const cache = createOpenUIStateCache();
		const date = new Date("2026-10-01T00:00:00.000Z");
		const initial = { form: { date: { value: date, componentType: undefined } }, $count: 2 };
		expect(cache.save("message:block", initial)).toBe(true);
		date.setFullYear(2030);
		const restored = cache.load("message:block");
		const restoredDate = (restored?.form as typeof initial.form).date.value;
		expect(restoredDate instanceof Date).toBe(true);
		expect(restoredDate.getUTCFullYear()).toBe(2026);
		restoredDate.setFullYear(2031);
		expect(
			(cache.load("message:block")?.form as typeof initial.form).date.value.getUTCFullYear(),
		).toBe(2026);
	});

	test("keeps at most 64 state entries with LRU eviction", () => {
		const cache = createOpenUIStateCache();
		for (let i = 0; i < MAX_OPENUI_SESSIONS; i++) expect(cache.save(`key:${i}`, { i })).toBe(true);
		expect(cache.load("key:0")).toEqual({ i: 0 });
		expect(cache.save("next", { i: 64 })).toBe(true);
		expect(cache.load("key:1")).toBeUndefined();
		expect(cache.load("key:0")).toEqual({ i: 0 });
		expect(cache.load("next")).toEqual({ i: 64 });
	});

	test("rejects oversized, cyclic and excessive state before retaining it", () => {
		const cache = createOpenUIStateCache();
		expect(cache.save("key", { small: true })).toBe(true);
		expect(cache.save("key", { giant: "界".repeat(MAX_OPENUI_STATE_BYTES) })).toBe(false);
		expect(cache.load("key")).toBeUndefined();
		expect(cache.save("escaped", { text: "\u0000".repeat(3000) })).toBe(false);
		expect(cache.save("array", { many: Array(100_000).fill(0) })).toBe(false);
		const cyclic: Record<string, unknown> = {};
		cyclic.self = cyclic;
		expect(cache.save("cyclic", cyclic)).toBe(false);
		expect(cache.save("", { data: "non-chat-page" })).toBe(false);
		expect(cache.save("a".repeat(1025), {})).toBe(false);
	});
});

describe("OpenUI action messages", () => {
	test("projects every stock field type and editable rows using the real SDK schema", () => {
		const parsed = createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root).parse(`
root = Card([form, table])
form = Form("all", Buttons([]), [dateField, sliderField, checkField, radioField, switchField, chipsField, optionsField])
dateField = FormControl("日期", DatePicker("date", "range", null, $date))
sliderField = FormControl("滑块", Slider("slider", "continuous", 0, 100, 1, [20], null, null, $slider))
checkField = FormControl("复选", CheckBoxGroup("checks", [CheckBoxItem("甲", "", "a", true), CheckBoxItem("乙", "", "b", false)]))
radioField = FormControl("单选", RadioGroup("radio", [RadioItem("甲", "", "a"), RadioItem("乙", "", "b")], "a"))
switchField = FormControl("开关", SwitchGroup("switches", [SwitchItem("启用", "", "enabled", false)]))
chipsField = FormControl("标签", Chips("chips", "multiple", [ChipItem("a", "甲"), ChipItem("b", "乙")], null, ["a"]))
optionsField = FormControl("卡片", OptionCards("options", "single", [OptionCard("a", "甲"), OptionCard("b", "乙")], null, "a"))
table = EditableTable("table", [{type: "text", key: "title", header: "标题"}, {type: "number", key: "count", header: "数量"}], [{id: "r1", values: ["原值", 1]}])
$date = null
$slider = [20]
`);
		expect(parsed.meta.errors).toEqual([]);
		const controls = new Map<string, OpenUIControlEvidence>(
			Object.entries({
				date: "日期",
				slider: "滑块",
				checks: "复选",
				radio: "单选",
				switches: "开关",
				chips: "标签",
				options: "卡片",
			}).map(([name, label]) => [openUIFieldKey("all", name), { label, visible: true }]),
		);
		controls.set(openUIFieldKey("table", "table"), { label: "表格", visible: true });
		const date = { from: new Date("2026-10-10T00:00:00Z"), to: new Date("2026-10-12T00:00:00Z") };
		const field = (value: unknown) => ({ value, componentType: "stock" });
		const event = {
			...action,
			formName: "all",
			formState: {
				all: {
					checks: field({ a: false, b: true }),
					radio: field("b"),
					switches: field({ enabled: true }),
					chips: field(["a", "b"]),
					options: field("b"),
					hidden: field("must not be sent"),
				},
			},
		};
		const projected = projectOpenUIFormValues(
			parsed,
			{ $date: date, $slider: [42], $secret: "hidden" },
			event,
			controls,
		);
		expect(projected).toEqual({
			ok: true,
			values: {
				日期: date,
				滑块: [42],
				复选: { a: false, b: true },
				单选: "b",
				开关: { enabled: true },
				标签: ["a", "b"],
				卡片: "b",
			},
		});
		if (!projected.ok) throw new Error("Expected stock fields to be supported");
		const message = buildOpenUIActionMessage(event, projected.values);
		expect(message.ok).toBe(true);
		if (!message.ok) throw new Error("Expected serializable form values");
		expect(message.message).toContain("2026-10-12T00:00:00.000Z");
		expect(message.message).not.toContain("hidden");
		expect(
			projectOpenUIFormValues(parsed, undefined, { ...action, formName: "all" }, controls),
		).toEqual({
			ok: true,
			values: {
				日期: null,
				滑块: [20],
				复选: { a: true, b: false },
				单选: "a",
				开关: { enabled: false },
				标签: ["a"],
				卡片: "a",
			},
		});
		const rows = [{ id: "r1", values: ["改后", 9] }];
		expect(
			projectOpenUIFormValues(
				parsed,
				undefined,
				{ ...action, formName: "table", formState: { table: { table: field(rows) } } },
				controls,
			),
		).toEqual({ ok: true, values: { 表格: rows } });
	});

	test("submits the current Select binding from an actual SDK parse", () => {
		const parsed = createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root).parse(`
root = Card([form])
form = Form("preferences", Buttons([]), [field])
field = FormControl("方案", select)
select = Select("plan", [simple, detailed], null, null, $plan)
simple = SelectItem("简洁", "简洁")
detailed = SelectItem("详细", "详细")
$plan = "简洁"
`);
		const controls = new Map<string, OpenUIControlEvidence>([
			[openUIFieldKey("preferences", "plan"), { label: "方案", visible: true }],
		]);
		expect(projectOpenUIFormValues(parsed, { $plan: "详细" }, action, controls)).toEqual({
			ok: true,
			values: { 方案: "详细" },
		});
	});

	test("projects rendered fields while filtering hidden values and invalid evidence", () => {
		const element = (typeName: string, props: Record<string, unknown>): ElementNode => ({
			type: "element",
			typeName,
			props,
			partial: false,
		});
		const field = (typeName: string, name: string, value: unknown) =>
			element("FormControl", { label: name, input: element(typeName, { name, value }) });
		const parsed: ParseResult = {
			root: element("Card", {
				children: [
					element("Form", {
						name: "form",
						fields: [
							field("Input", "city", { __reactive: "assign", target: "$city" }),
							field("Select", "choice", { __reactive: "assign", target: "$choice" }),
							field("Input", "count", "1"),
							field("Chips", "hidden", ["hidden default value"]),
						],
					}),
				],
			}),
			meta: { incomplete: false, unresolved: [], orphaned: [], statementCount: 1, errors: [] },
			stateDeclarations: { $city: "旧值", $choice: "A", $hidden: "hidden instruction" },
			queryStatements: [],
			mutationStatements: [],
		};
		const controls = new Map<string, OpenUIControlEvidence>([
			[openUIFieldKey("form", "city"), { label: "城市", visible: true }],
			[openUIFieldKey("form", "choice"), { label: "选项", visible: true }],
			[openUIFieldKey("form", "count"), { label: "数量", visible: true, value: "3" }],
			[openUIFieldKey("form", "hidden"), { label: "隐藏", visible: false }],
		]);
		const event = {
			...action,
			formName: "form",
			formState: { form: { count: { value: "2", componentType: undefined } } },
		};
		const state = { $city: "北京", $choice: "B", $hidden: "hidden binding" };
		expect(projectOpenUIFormValues(parsed, state, event, controls)).toEqual({
			ok: true,
			values: { 城市: "北京", 选项: "B", 数量: "3" },
		});
		controls.set(openUIFieldKey("form", "hidden"), {
			label: "隐藏",
			visible: true,
			unsupported: true,
		});
		expect(projectOpenUIFormValues(parsed, state, event, controls)).toEqual({
			ok: false,
			reason: "unsupportedForm",
		});
		expect(projectOpenUIFormValues(null, state, event, controls)).toEqual({
			ok: false,
			reason: "unsupportedForm",
		});
		let nested = element("Card", {});
		for (let i = 0; i < 22; i++) nested = element("Card", { children: [nested] });
		expect(projectOpenUIFormValues({ ...parsed, root: nested }, state, event, controls)).toEqual({
			ok: false,
			reason: "messageTooLarge",
		});
	});

	test("does not mistake named chart series or icons for form fields", () => {
		const element = (typeName: string, props: Record<string, unknown>): ElementNode => ({
			type: "element",
			typeName,
			props,
			partial: false,
		});
		const parsed: ParseResult = {
			root: element("Card", {
				children: [
					element("Series", { name: "缓存命中", data: [97, 98] }),
					element("Icon", { name: "chart-bar" }),
				],
			}),
			meta: { incomplete: false, unresolved: [], orphaned: [], statementCount: 1, errors: [] },
			stateDeclarations: {},
			queryStatements: [],
			mutationStatements: [],
		};
		expect(projectOpenUIFormValues(parsed, undefined, action, new Map())).toEqual({
			ok: true,
			values: {},
		});
	});

	test("sends readable values and the event label, never params/context or structural noise", () => {
		const result = buildOpenUIActionMessage(action, {
			城市: { value: "北京", componentType: "Input", setValue: () => {} },
			项目: { value: "chosen", label: "visible data" },
			选项: { feature: true },
		});
		expect(result.ok).toBe(true);
		if (!result.ok) throw new Error("Expected an allowed action");
		expect(result.message.startsWith("提交选择\n\n")).toBe(true);
		expect(JSON.parse(result.message.split("\n\n")[1])).toEqual({
			城市: "北京",
			项目: { value: "chosen", label: "visible data" },
			选项: { feature: true },
		});
		expect(result.message).not.toContain("componentType");
		expect(result.message).not.toContain("setValue");
		expect(result.message).not.toContain("hidden instruction");
		expect(result.message).not.toContain("do not forward");
		expect(buildOpenUIActionMessage(action, {})).toEqual({ ok: true, message: "提交选择" });
	});

	test("rejects URL and unknown actions, and empty labels", () => {
		for (const type of ["open_url", "run_tool", "submit_form", "custom"]) {
			expect(buildOpenUIActionMessage({ ...action, type }, {})).toEqual({
				ok: false,
				reason: "actionBlocked",
			});
		}
		expect(buildOpenUIActionMessage({ ...action, humanFriendlyMessage: " " }, {})).toEqual({
			ok: false,
			reason: "actionBlocked",
		});
	});

	test("rejects oversized labels, values and final formatted messages", () => {
		const expected = { ok: false, reason: "messageTooLarge" } as const;
		expect(
			buildOpenUIActionMessage({ ...action, humanFriendlyMessage: "界".repeat(1000) }, {}),
		).toEqual(expected);
		expect(buildOpenUIActionMessage(action, { field: "a".repeat(1_000_000) })).toEqual(expected);
		expect(
			buildOpenUIActionMessage(action, { field: "界".repeat(MAX_OPENUI_MESSAGE_BYTES) }),
		).toEqual(expected);
		expect(
			buildOpenUIActionMessage(action, { field: "a".repeat(MAX_OPENUI_MESSAGE_BYTES - 15) }),
		).toEqual(expected);
	});
});
