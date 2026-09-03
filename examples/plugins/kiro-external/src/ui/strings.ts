/**
 * Panel copy, in the two languages the host ships.
 *
 * The dictionaries stay here because only this plugin knows its own copy. What it no longer owns
 * is deciding which language is active: `narrafork.i18n` reports the host's locale and applies
 * the host's fallback rules, so this file holds tables and nothing else.
 *
 * It used to read `navigator.language`, on the strength of a comment claiming
 * `context.host.locale` was a `"unknown"` placeholder. That was wrong — the field carried the
 * real value — and the cost was concrete: a user running the app in Chinese with an
 * English-language browser got an English panel, silently overriding a preference they had
 * explicitly set.
 *
 * Wording is copied from the host's `settings` namespace deliberately — the point of this
 * panel is to be indistinguishable from the built-in one, and paraphrasing would show up as
 * the two pages disagreeing about what the same button does. That is a one-time human
 * comparison; the host's translation keys are internal and are never read at runtime.
 */

/**
 * `en` is the source of truth for the key set; `zhCN` must cover it exactly.
 *
 * Both tables used to be typed `Record<string, string>`, which makes the two independent:
 * adding a key to `en` and forgetting `zhCN` compiles, and `t()` then falls back to the
 * English string. That fallback is the problem — a missing translation looks like a
 * deliberate choice to leave one label in English, so nobody reports it.
 *
 * `Record<keyof typeof en, string>` turns the omission into a compile error, and the
 * `satisfies` on `en` keeps its literal keys instead of widening them to `string` (which
 * would make the constraint vacuous).
 */
type Strings = Record<string, string>;

const en = {
	credentials: "Credentials",
	credentialsDesc: "Manage Kiro credentials. Credentials are grouped by account type.",
	loadBalancing: "Load Balancing",
	modePriority: "Priority",
	modeBalanced: "Balanced",
	refreshAllModels: "Refresh Models",
	refreshAllModelsConfirm: "This will re-fetch model lists for all credentials. Continue?",
	tierFree: "Free",
	tierPaid: "Paid",
	tierEnterprise: "Enterprise",
	tierFreeDesc: "Limited model access",
	tierPaidDesc: "All models available",
	tierEnterpriseDesc: "Claude models only",
	importTitle: "Import Credentials",
	importPlaceholder: "Paste credential JSON, or one ksk_... API key per line...",
	importHint:
		"Supports refresh tokens and static API keys. Paste bare ksk_... keys directly (one per line or comma-separated), or credential JSON. Values are sent to the plugin backend and never displayed back here.",
	importBtn: "Import",
	importSuccess: "Imported {added} credential(s), {duplicates} duplicate(s) skipped",
	importInvalid: "No valid credential JSON or ksk_... API key found",
	importEmpty: "Nothing to import.",
	noCredentials: "No credentials. Import some to get started.",
	colPriority: "Priority",
	colStatus: "Status",
	colExpires: "Expires",
	colStats: "Stats",
	colUsage: "Usage",
	colLastUsed: "Last Used",
	colActions: "Actions",
	colDisplayName: "Display Name",
	colRegion: "Region",
	statSuccess: "OK",
	statFail: "Fail",
	apiKeyBadge: "api_key",
	apiKeyNoRefresh: "Static API keys are never refreshed",
	apiKeyNeverExpires: "Never expires",
	refreshToken: "Refresh Token",
	queryUsage: "Usage",
	reset: "Reset",
	edit: "Edit",
	models: "Models",
	deleteConfirm: "Are you sure you want to delete this credential?",
	batchDelete: "Delete Selected",
	batchDeleteConfirm: "Are you sure you want to delete {count} selected credentials?",
	batchDeleteSuccess: "Deleted {count} credentials",
	deleteUnhealthy: "Delete Failed/Banned",
	deleteUnhealthyConfirm:
		"Delete all {count} credentials disabled for too many failures or banned status? This applies across all pages.",
	deleteUnhealthySuccess: "Deleted {count} failed/banned credentials",
	selectAll: "Select All",
	pageInfo: "Page {current} / {total} ({count} total)",
	enabled: "enabled",
	disabled: "disabled",
	editTitle: "Edit Kiro credential {id}",
	editSecretHint:
		"Secrets left empty stay unchanged. Other fields cleared to empty remove the stored value.",
	editSecretStored: "Stored — leave empty to keep",
	editSave: "Save",
	editCancel: "Cancel",
	editEmail: "Email",
	editDisplayName: "Display Name",
	editRegion: "Region",
	editAuthRegion: "Auth Region",
	editApiRegion: "API Region",
	editProfileArn: "Profile ARN",
	editSubscriptionTitle: "Subscription Title",
	editStartUrl: "Start URL",
	editMachineId: "Machine ID",
	editProxyUrl: "Proxy URL",
	editProxyUsername: "Proxy username",
	editAuthMethod: "Auth Method",
	editPriority: "Priority",
	editPriorityDesc: "Lower value is used first",
	editRefreshToken: "Refresh Token",
	editApiKey: "API Key",
	editClientId: "Client ID",
	editClientSecret: "Client secret",
	editProxyPassword: "Proxy password",
	editNoChanges: "No changes to save.",
	confirmTitle: "Confirm",
	confirmOk: "Confirm",
	confirmCancel: "Cancel",
	usageLoading: "Loading usage…",
	usageNone: "No usage data available.",
	usagePlan: "Plan",
	usageResets: "Resets",
	usageRemaining: "Remaining",
	usageOverage: "Overage",
	modelsLoading: "Fetching models…",
	modelsNone: "No models available.",
	modelsCount: "{count} models",
	modelsRefresh: "Refresh models",
	runtimeMissingTitle: "The credential page could not start",
	busyStatus: "{label}…",
	doneStatus: "{label} done.",
	failedStatus: "{label} failed: {error}",
	actionEnable: "Enable",
	actionDisable: "Disable",
	actionImport: "Import",
	actionRefresh: "Refresh",
	actionRemove: "Remove",
	actionUpdate: "Update credential",
	actionSetPriority: "Set priority",
	actionSwitchMode: "Switch to {mode}",
	actionRefreshAllModels: "Refresh all models",
	actionDeleteUnhealthy: "Delete failed/banned",
	actionBatchDelete: "Delete selected",
} satisfies Strings;

const zhCN: Record<keyof typeof en, string> = {
	credentials: "凭据管理",
	credentialsDesc: "管理 Kiro 凭据，按账户类型分组展示。",
	loadBalancing: "负载均衡",
	modePriority: "优先级",
	modeBalanced: "均衡",
	refreshAllModels: "刷新模型",
	refreshAllModelsConfirm: "将重新获取所有凭据的模型列表，是否继续？",
	tierFree: "免费版",
	tierPaid: "付费版",
	tierEnterprise: "企业版",
	tierFreeDesc: "仅可使用部分模型",
	tierPaidDesc: "可使用全部模型",
	tierEnterpriseDesc: "仅可使用 Claude 系列模型",
	importTitle: "导入凭据",
	importPlaceholder: "粘贴凭据 JSON，或每行一个 ksk_... API Key...",
	importHint:
		"支持 refreshToken 与静态 API Key。可直接粘贴多行 ksk_... 明文密钥（每行一个或用逗号分隔），也可粘贴凭据 JSON。内容会发送给插件后端，不会在此回显。",
	importBtn: "导入",
	importSuccess: "已导入 {added} 个凭据，跳过 {duplicates} 个重复",
	importInvalid: "未找到有效的凭据 JSON 或 ksk_... API Key",
	importEmpty: "没有可导入的内容。",
	noCredentials: "暂无凭据，请导入凭据以开始使用。",
	colPriority: "优先级",
	colStatus: "状态",
	colExpires: "过期",
	colStats: "统计",
	colUsage: "用量",
	colLastUsed: "最后使用",
	colActions: "操作",
	colDisplayName: "展示名称",
	colRegion: "区域",
	statSuccess: "成功",
	statFail: "失败",
	apiKeyBadge: "api_key",
	apiKeyNoRefresh: "静态 API Key 无需刷新",
	apiKeyNeverExpires: "永不过期",
	refreshToken: "刷新 Token",
	queryUsage: "查询用量",
	reset: "重置",
	edit: "编辑",
	models: "模型",
	deleteConfirm: "确定要删除这个凭据吗？",
	batchDelete: "删除选中",
	batchDeleteConfirm: "确定要删除选中的 {count} 个凭据吗？",
	batchDeleteSuccess: "已删除 {count} 个凭据",
	deleteUnhealthy: "删除错误/封禁",
	deleteUnhealthyConfirm:
		"确定要删除所有因错误过多或封禁而禁用的 {count} 个凭据吗？此操作会跨所有分页生效。",
	deleteUnhealthySuccess: "已删除 {count} 个错误过多/封禁凭据",
	selectAll: "全选",
	pageInfo: "第 {current} / {total} 页（共 {count} 条）",
	enabled: "已启用",
	disabled: "已禁用",
	editTitle: "编辑 Kiro 凭据 {id}",
	editSecretHint: "敏感字段留空不会修改；其他字段清空则会移除已存储的值。",
	editSecretStored: "已存储，留空则保持不变",
	editSave: "保存",
	editCancel: "取消",
	editEmail: "邮箱",
	editDisplayName: "展示名称",
	editRegion: "区域",
	editAuthRegion: "认证区域",
	editApiRegion: "API 区域",
	editProfileArn: "Profile ARN",
	editSubscriptionTitle: "订阅名称",
	editStartUrl: "Start URL",
	editMachineId: "Machine ID",
	editProxyUrl: "代理地址",
	editProxyUsername: "代理用户名",
	editAuthMethod: "认证方式",
	editPriority: "优先级",
	editPriorityDesc: "数值越小越优先使用",
	editRefreshToken: "Refresh Token",
	editApiKey: "API Key",
	editClientId: "Client ID",
	editClientSecret: "Client Secret",
	editProxyPassword: "代理密码",
	editNoChanges: "没有需要保存的修改。",
	confirmTitle: "确认",
	confirmOk: "确定",
	confirmCancel: "取消",
	usageLoading: "正在加载用量…",
	usageNone: "暂无用量数据。",
	usagePlan: "套餐",
	usageResets: "重置时间",
	usageRemaining: "剩余",
	usageOverage: "超额",
	modelsLoading: "正在获取模型…",
	modelsNone: "暂无可用模型。",
	modelsCount: "{count} 个模型",
	modelsRefresh: "刷新模型",
	runtimeMissingTitle: "凭据页无法启动",
	busyStatus: "{label}…",
	doneStatus: "{label}完成。",
	failedStatus: "{label}失败：{error}",
	actionEnable: "启用",
	actionDisable: "禁用",
	actionImport: "导入",
	actionRefresh: "刷新",
	actionRemove: "删除",
	actionUpdate: "更新凭据",
	actionSetPriority: "设置优先级",
	actionSwitchMode: "切换为 {mode}",
	actionRefreshAllModels: "刷新全部模型",
	actionDeleteUnhealthy: "删除错误/封禁凭据",
	actionBatchDelete: "删除选中凭据",
};

/** The tables in the shape the host SDK expects: locale tag → key → text. */
const TABLES: Record<string, Record<string, string>> = { en, "zh-CN": zhCN };

interface HostI18n {
	locale: string;
	t(tables: Record<string, Record<string, string>>, key: string, params?: unknown): string;
	onChange(listener: (locale: string) => void): () => void;
}

function hostI18n(): HostI18n | undefined {
	return (globalThis as { narrafork?: { i18n?: HostI18n } }).narrafork?.i18n;
}

/**
 * Look up a string, filling `{name}` placeholders.
 *
 * Delegates to the host, which owns both the active locale and the fallback rules. The local
 * branch below is only for a host too old to provide `i18n`.
 *
 * Resolved per call rather than against a table chosen at module load: the host reports language
 * changes while the panel is open, and a table captured once could never reflect them.
 */
export function t(key: keyof typeof en, params?: Record<string, string | number>): string {
	const host = hostI18n();
	if (host) return host.t(TABLES, key as string, params);
	return interpolate(en[key] ?? String(key), params);
}

/**
 * The host's active locale, for `Intl` formatters.
 *
 * Not `navigator.language`: that is the BROWSER's language, which is a different thing
 * from the language the user chose in this app. Reading it produces a panel whose text
 * is Chinese (via the host tables) while its dates read "2 hours ago" — the kind of
 * split that looks like a translation gap rather than a wrong locale source.
 *
 * Falls back to the browser only for a host too old to report a locale.
 */
export function activeLocale(): string {
	const host = hostI18n();
	if (host?.locale) return host.locale;
	return typeof navigator === "undefined" ? "en" : (navigator.language ?? "en");
}

/**
 * Subscribe to host language changes.
 *
 * Text cannot follow the host on its own — the `--nf-*` tokens let the browser recolour this
 * panel with no code here, but a string already in the DOM stays until the panel rewrites it.
 * Returns a no-op unsubscribe on an older host so callers need no capability check.
 */
export function onLocaleChange(listener: (locale: string) => void): () => void {
	return hostI18n()?.onChange(listener) ?? (() => {});
}

function interpolate(template: string, params?: Record<string, string | number>): string {
	if (!params) return template;
	return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
		name in params ? String(params[name]) : whole,
	);
}
