import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { DEFAULTS, deepMerge } from "@server/lib/settings";
import { updateSettingsSchema } from "@server/routes/settings";
import { DEFAULT_CONTEXT_THRESHOLDS } from "@shared/context-thresholds";
import {
	DEFAULT_RECORDING_REQUEST_BYTES,
	DEFAULT_RECORDING_RESPONSE_BYTES,
	MAX_RECORDING_REQUEST_BYTES,
	MAX_RECORDING_RESPONSE_BYTES,
	MIN_RECORDING_WINDOW_BYTES,
} from "@shared/request-recording";
import {
	DEFAULT_CONTEXT_THRESHOLDS_DRAFT,
	normalizeContextManagementDraft,
} from "../components/narrator/context-management/types";
import en from "../locales/en/settings.json";
import zh from "../locales/zh-CN/settings.json";
import { readRequestRecordingSettings } from "./useInstanceSettings";

const hook = readFileSync(new URL("./useInstanceSettings.ts", import.meta.url), "utf8");
const page = readFileSync(new URL("../routes/settings/agent.tsx", import.meta.url), "utf8");
const section = readFileSync(
	new URL("../components/settings/AgentSection.tsx", import.meta.url),
	"utf8",
);

describe("request recording settings compatibility", () => {
	test("old configurations and missing agent settings use bounded recording defaults", () => {
		const expected = {
			requestDumpRecordingEnabled: true,
			requestDumpRequestBytes: DEFAULT_RECORDING_REQUEST_BYTES,
			requestDumpResponseBytes: DEFAULT_RECORDING_RESPONSE_BYTES,
			requestDumpEnabled: false,
			requestDumpErrorsOnly: false,
		};
		expect(readRequestRecordingSettings()).toEqual(expected);
		expect(readRequestRecordingSettings({ requestDumpEnabled: false })).toEqual(expected);
		const merged = deepMerge(DEFAULTS, { agent: { requestDumpEnabled: false } });
		expect(readRequestRecordingSettings(merged.agent)).toEqual(expected);
	});
	test("false, zero and old continuous-retention switches remain explicit independent values", () => {
		const config = {
			requestDumpRecordingEnabled: false,
			requestDumpRequestBytes: 0,
			requestDumpResponseBytes: 0,
			requestDumpEnabled: true,
			requestDumpErrorsOnly: true,
		};
		expect(readRequestRecordingSettings(config)).toEqual(config);
		expect(readRequestRecordingSettings(deepMerge(DEFAULTS, { agent: config }).agent)).toEqual(
			config,
		);
		expect(
			readRequestRecordingSettings({ requestDumpEnabled: true, requestDumpErrorsOnly: true })
				.requestDumpRecordingEnabled,
		).toBe(true);
	});
	test("PATCH accepts independent zero/full byte capacities and booleans", () => {
		for (const [requestDumpRequestBytes, requestDumpResponseBytes] of [
			[0, 0],
			[MIN_RECORDING_WINDOW_BYTES, MIN_RECORDING_WINDOW_BYTES],
			[MAX_RECORDING_REQUEST_BYTES, MAX_RECORDING_RESPONSE_BYTES],
		]) {
			const agent = {
				requestDumpRequestBytes,
				requestDumpResponseBytes,
				requestDumpRecordingEnabled: false,
				requestDumpEnabled: true,
				requestDumpErrorsOnly: true,
			};
			expect(updateSettingsSchema.parse({ agent }).agent).toEqual(agent);
		}
	});
	test("PATCH rejects negative, fractional, oversized and non-number byte capacities", () => {
		for (const [field, max] of [
			["requestDumpRequestBytes", MAX_RECORDING_REQUEST_BYTES],
			["requestDumpResponseBytes", MAX_RECORDING_RESPONSE_BYTES],
		] as const) {
			for (const value of [-1, 0.5, 1, MIN_RECORDING_WINDOW_BYTES - 1, max + 1, "16", null]) {
				expect(updateSettingsSchema.safeParse({ agent: { [field]: value } }).success).toBe(false);
			}
		}
		expect(
			updateSettingsSchema.safeParse({ agent: { requestDumpRecordingEnabled: "true" } }).success,
		).toBe(false);
	});
	test("all recording fields roundtrip through controlled settings and bilingual labels", () => {
		for (const field of [
			"requestDumpRecordingEnabled",
			"requestDumpRequestBytes",
			"requestDumpResponseBytes",
		]) {
			expect(hook).toContain(`${field}: state.${field}`);
			expect(page).toContain(`${field}={is.${field}}`);
			const setter = `set${field[0].toUpperCase()}${field.slice(1)}`;
			expect(page).toContain(`${setter}={is.${setter}}`);
			expect(section).toContain(`props.${setter}`);
		}
		for (const locale of [en, zh]) {
			expect(locale.requestDumpRecordingEnabled).toBeTruthy();
			expect(locale.requestDumpRecordingEnabledDesc).toContain("/save_dump");
			expect(locale.requestDumpRequestCapacity).toBeTruthy();
			expect(locale.requestDumpResponseCapacity).toBeTruthy();
			expect(locale.requestDumpCapacityDesc).toContain("0");
			expect(locale.requestDumpEnabledDesc).toContain("512MiB");
			expect(locale.requestDumpErrorsOnlyDesc).toContain("/save_dump");
		}
	});
});

describe("permission rule auto-approval settings wiring", () => {
	test("defaults off, forwards the administrator boundary, and persists independently of danger reflection", () => {
		expect(hook).toContain("permissionRuleAutoApprove: false");
		expect(hook).toContain(
			"permissionRuleAutoApprove: settings.agent?.permissionRuleAutoApprove ?? false",
		);
		expect(hook).toContain("permissionRuleAutoApprove: state.permissionRuleAutoApprove");
		expect(page).toContain('isAdmin={currentUser?.role === "admin"}');
		expect(page).toContain("permissionRuleAutoApprove={is.permissionRuleAutoApprove}");
		expect(page).toContain("setPermissionRuleAutoApprove={is.setPermissionRuleAutoApprove}");
		const control = section.slice(
			section.indexOf('label={t("permissionRuleAutoApprove")}'),
			section.indexOf('label={t("dangerReflectionLevel")}'),
		);
		expect(control).toContain("checked={props.permissionRuleAutoApprove}");
		expect(control).toContain("disabled={!props.isAdmin}");
		expect(control).toContain("props.setPermissionRuleAutoApprove(e.currentTarget.checked)");
		expect(control).not.toContain("setDangerReflectionLevel");
	});
});

describe("default narrator write audience settings wiring", () => {
	test("has an independent type and defaults missing settings to auto", () => {
		expect(hook).toContain(
			'export type DefaultNarratorWriteAudience = "auto" | "owner" | "project" | "public"',
		);
		expect(hook).toContain("defaultNarratorWriteAudience: DefaultNarratorWriteAudience");
		expect(hook).toContain('defaultNarratorWriteAudience: "auto"');
		expect(hook).toContain(
			'defaultNarratorWriteAudience: settings.agent?.defaultNarratorWriteAudience ?? "auto"',
		);
	});

	test("saves independently and wires the controlled selector and setter", () => {
		expect(hook).toContain("defaultNarratorWriteAudience: state.defaultNarratorWriteAudience");
		expect(page).toContain("defaultNarratorWriteAudience={is.defaultNarratorWriteAudience}");
		expect(page).toContain("setDefaultNarratorWriteAudience={is.setDefaultNarratorWriteAudience}");
		expect(section).toContain("value={props.defaultNarratorWriteAudience}");
		expect(section).toContain("props.setDefaultNarratorWriteAudience(");
		expect(section).toContain('v === "owner" || v === "project" || v === "public" ? v : "auto"');
	});

	test("offers four bilingual choices and explains visibility limits and inheritance", () => {
		for (const [value, suffix] of [
			["auto", "Auto"],
			["owner", "Owner"],
			["project", "Project"],
			["public", "Public"],
		]) {
			const key = `defaultNarratorWriteAudience${suffix}` as keyof typeof en;
			expect(section).toContain(`{ value: "${value}", label: t("${key}") }`);
			expect(en[key]).toBeTruthy();
			expect(zh[key]).toBeTruthy();
		}
		for (const text of [
			"newly created",
			"existing sessions are unchanged",
			"Forks inherit",
			"Auto follows visibility",
			"administrators and explicit grants",
			"project members with write access",
			"meet project access requirements",
			"never expands visibility",
			"automatically narrowed at creation",
			"effective visibility",
		])
			expect(en.defaultNarratorWriteAudienceDesc).toContain(text);
		for (const text of [
			"仅对新建叙述者生效",
			"不改变已有会话",
			"分叉继承来源会话的权限",
			"跟随可见范围",
			"管理员和显式授权仍按现有规则",
			"具有项目操作权限的成员",
			"所有符合项目访问条件的登录用户",
			"不会扩大可见范围",
			"创建时会自动收窄",
			"有效可见性",
		])
			expect(zh.defaultNarratorWriteAudienceDesc).toContain(text);
	});
});

describe("compaction-only context settings", () => {
	test("uses shared standard 95 and large 75 defaults in both editors", () => {
		const panel = readFileSync(
			new URL("../components/narrator/NarratorPanel.tsx", import.meta.url),
			"utf8",
		);
		const draftTypes = readFileSync(
			new URL("../components/narrator/context-management/types.ts", import.meta.url),
			"utf8",
		);
		const modal = readFileSync(
			new URL(
				"../components/narrator/context-management/ContextThresholdSettingsModal.tsx",
				import.meta.url,
			),
			"utf8",
		);
		expect(DEFAULT_CONTEXT_THRESHOLDS).toEqual({
			standard: { compactStart: 95 },
			large: { compactStart: 75 },
		});
		expect(DEFAULT_CONTEXT_THRESHOLDS_DRAFT).toBe(DEFAULT_CONTEXT_THRESHOLDS);
		expect(draftTypes).toContain(
			"export const DEFAULT_CONTEXT_THRESHOLDS_DRAFT: ContextThresholdsDraft = DEFAULT_CONTEXT_THRESHOLDS",
		);
		expect(
			normalizeContextManagementDraft({
				contextThresholds: DEFAULT_CONTEXT_THRESHOLDS_DRAFT,
				autoCompactKeepPairs: 2,
			}).contextThresholds,
		).toEqual(DEFAULT_CONTEXT_THRESHOLDS);
		expect(hook).toContain("cloneDefaultContextThresholds()");
		expect(panel).toContain(
			"settingsData?.agent?.contextThresholds ?? DEFAULT_CONTEXT_THRESHOLDS_DRAFT",
		);
		expect(panel).toContain("current={contextThresholdSettings}");
		for (const size of ["standard", "large"] as const) {
			expect(section).toContain(`DEFAULT_CONTEXT_THRESHOLDS.${size}.compactStart`);
			expect(panel).toContain(`DEFAULT_CONTEXT_THRESHOLDS_DRAFT.${size}.compactStart`);
			expect(modal).toContain(`value={draft.contextThresholds.${size}.compactStart}`);
			expect(modal).toContain(`DEFAULT_CONTEXT_THRESHOLDS_DRAFT.${size}.compactStart`);
		}
		expect(panel).toContain("api.triggerCompact(narratorId)");
		expect(modal).toContain('t("contextThresholdSettingsTitle")');
		for (const source of [hook, page, section, panel, modal]) {
			expect(source).not.toMatch(/prun/i);
		}
	});

	test("keeps bilingual compaction labels and unrelated avatar cropping", () => {
		for (const locale of [en, zh]) {
			expect(locale.compactStart).toBeTruthy();
			expect(locale.compactStartDesc).toContain("95%");
			expect(locale.compactStartDesc).toContain("75%");
			expect(locale.avatarCropTitle).toBeTruthy();
			expect(Object.keys(locale).filter((key) => /prun/i.test(key))).toEqual([]);
		}
	});
});

// Source-level wiring guards follow the existing hook contract test convention.
describe("default narrator visibility settings wiring", () => {
	test("defaults to auto and loads older settings with an auto fallback", () => {
		expect(hook).toContain('defaultNarratorVisibility: "auto"');
		expect(hook).toContain(
			'defaultNarratorVisibility: settings.agent?.defaultNarratorVisibility ?? "auto"',
		);
	});

	test("saves the selected value and exposes its generated setter to the section", () => {
		expect(hook).toContain("defaultNarratorVisibility: state.defaultNarratorVisibility");
		expect(hook).toContain("Object.keys(makeDefaults())");
		expect(page).toContain("defaultNarratorVisibility={is.defaultNarratorVisibility}");
		expect(page).toContain("setDefaultNarratorVisibility={is.setDefaultNarratorVisibility}");
		expect(section).toContain("value={props.defaultNarratorVisibility}");
		expect(section).toContain(
			'props.setDefaultNarratorVisibility(v === "private" || v === "public" ? v : "auto")',
		);
	});

	test("offers all three choices with bilingual labels and scope explanations", () => {
		for (const [value, suffix] of [
			["auto", "Auto"],
			["private", "Private"],
			["public", "Public"],
		]) {
			const key = `defaultNarratorVisibility${suffix}` as keyof typeof en;
			expect(section).toContain(`{ value: "${value}", label: t("${key}") }`);
			expect(en[key]).toBeTruthy();
			expect(zh[key]).toBeTruthy();
		}
		expect(en.defaultNarratorVisibilityDesc).toContain("newly created");
		expect(en.defaultNarratorVisibilityDesc).toContain("Forks inherit");
		expect(en.defaultNarratorVisibilityDesc).toContain("separate default write audience");
		expect(en.defaultNarratorVisibilityDesc).not.toContain("who can continue them");
		expect(zh.defaultNarratorVisibilityDesc).toContain("操作权限由独立的默认可操作范围设置决定");
		expect(zh.defaultNarratorVisibilityDesc).not.toContain("并可按现有规则接续操作");
		expect(zh.defaultNarratorVisibilityDesc).toContain("仅对新建叙述者生效");
		expect(zh.defaultNarratorVisibilityDesc).toContain("分叉继承来源会话的权限");
		expect(zh.defaultNarratorVisibilityDesc).toContain("所有登录用户可见");
	});
});
