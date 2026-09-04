(() => {
  var __defProp = Object.defineProperty;
  var __returnValue = (v) => v;
  function __exportSetter(name, newValue) {
    this[name] = __returnValue.bind(null, newValue);
  }
  var __export = (target, all) => {
    for (var name in all)
      __defProp(target, name, {
        get: all[name],
        enumerable: true,
        configurable: true,
        set: __exportSetter.bind(all, name)
      });
  };
  var __esm = (fn, res) => () => (fn && (res = fn(fn = 0)), res);

  // examples/plugins/kiro-external/src/ui/panel-types.ts
  function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
  }
  async function runCommand(sdk, commandId, input) {
    const raw = await sdk.request("commands.execute", {
      commandId,
      ...input === undefined ? {} : { input }
    });
    if (isRecord(raw) && "output" in raw)
      return raw.output;
    return raw;
  }
  function errorMessage(error) {
    if (error instanceof Error)
      return error.message;
    if (isRecord(error) && typeof error.message === "string")
      return error.message;
    return String(error);
  }

  // examples/plugins/kiro-external/src/ui/strings.ts
  function hostI18n() {
    return globalThis.narrafork?.i18n;
  }
  function t(key, params) {
    const host = hostI18n();
    if (host)
      return host.t(TABLES, key, params);
    return interpolate(en[key] ?? String(key), params);
  }
  function activeLocale() {
    const host = hostI18n();
    if (host?.locale)
      return host.locale;
    return typeof navigator === "undefined" ? "en" : navigator.language ?? "en";
  }
  function onLocaleChange(listener) {
    return hostI18n()?.onChange(listener) ?? (() => {});
  }
  function interpolate(template, params) {
    if (!params)
      return template;
    return template.replace(/\{(\w+)\}/g, (whole, name) => (name in params) ? String(params[name]) : whole);
  }
  var en, zhCN, TABLES;
  var init_strings = __esm(() => {
    en = {
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
      importHint: "Supports refresh tokens and static API keys. Paste bare ksk_... keys directly (one per line or comma-separated), or credential JSON. Values are sent to the plugin backend and never displayed back here.",
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
      deleteUnhealthyConfirm: "Delete all {count} credentials disabled for too many failures or banned status? This applies across all pages.",
      deleteUnhealthySuccess: "Deleted {count} failed/banned credentials",
      selectAll: "Select All",
      pageInfo: "Page {current} / {total} ({count} total)",
      enabled: "enabled",
      disabled: "disabled",
      editTitle: "Edit Kiro credential {id}",
      editSecretHint: "Secrets left empty stay unchanged. Other fields cleared to empty remove the stored value.",
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
      actionBatchDelete: "Delete selected"
    };
    zhCN = {
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
      importHint: "支持 refreshToken 与静态 API Key。可直接粘贴多行 ksk_... 明文密钥（每行一个或用逗号分隔），也可粘贴凭据 JSON。内容会发送给插件后端，不会在此回显。",
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
      deleteUnhealthyConfirm: "确定要删除所有因错误过多或封禁而禁用的 {count} 个凭据吗？此操作会跨所有分页生效。",
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
      actionBatchDelete: "删除选中凭据"
    };
    TABLES = { en, "zh-CN": zhCN };
  });

  // examples/plugins/kiro-external/src/ui/format.ts
  function getTierMeta(tier) {
    switch (tier) {
      case "paid":
        return { color: "blue", label: t("tierPaid"), desc: t("tierPaidDesc") };
      case "enterprise":
        return { color: "violet", label: t("tierEnterprise"), desc: t("tierEnterpriseDesc") };
      default:
        return { color: "gray", label: t("tierFree"), desc: t("tierFreeDesc") };
    }
  }
  function isApiKeyEntry(entry) {
    if (entry.isApiKey === true)
      return true;
    const method = entry.authMethod?.trim().toLowerCase();
    return method === "api_key" || method === "apikey";
  }
  function containsApiKey(text) {
    return API_KEY_PATTERN.test(text);
  }
  function parseApiKeyLines(text) {
    const results = [];
    for (const line of text.split(`
`)) {
      const trimmed = line.trim();
      if (trimmed.startsWith("ksk_"))
        results.push({ authMethod: "api_key", apiKey: trimmed });
    }
    return results;
  }
  function relativeTime(value) {
    if (!value)
      return "-";
    const date = new Date(typeof value === "number" ? value < 10000000000 ? value * 1000 : value : value);
    if (Number.isNaN(date.getTime()))
      return String(value);
    const deltaMs = date.getTime() - Date.now();
    const formatter = new Intl.RelativeTimeFormat(activeLocale(), { numeric: "auto" });
    for (const [unit, ms] of RELATIVE_UNITS) {
      if (Math.abs(deltaMs) >= ms)
        return formatter.format(Math.round(deltaMs / ms), unit);
    }
    return formatter.format(Math.round(deltaMs / 1000), "second");
  }
  function formatRegions(entry) {
    const parts = [];
    if (entry.region)
      parts.push(entry.region);
    if (entry.authRegion)
      parts.push(`auth ${entry.authRegion}`);
    if (entry.apiRegion)
      parts.push(`api ${entry.apiRegion}`);
    return parts.length > 0 ? parts.join(" · ") : "-";
  }
  function formatUsageNumber(value) {
    return value.toFixed(2);
  }
  var API_KEY_PATTERN, RELATIVE_UNITS;
  var init_format = __esm(() => {
    init_strings();
    API_KEY_PATTERN = /ksk[_-][A-Za-z0-9._-]+/;
    RELATIVE_UNITS = [
      ["year", 365 * 24 * 60 * 60 * 1000],
      ["month", 30 * 24 * 60 * 60 * 1000],
      ["day", 24 * 60 * 60 * 1000],
      ["hour", 60 * 60 * 1000],
      ["minute", 60 * 1000]
    ];
  });

  // examples/plugins/kiro-external/src/ui/host-runtime.ts
  function readRuntime() {
    const candidate = globalThis.__nfPluginRuntime;
    if (!candidate || typeof candidate !== "object") {
      throw new HostRuntimeUnavailableError('The host UI runtime is missing. This view declares `runtime: "host-react"`, so the host should have injected React and Mantine before loading it.');
    }
    const runtime = candidate;
    if (runtime.version !== SUPPORTED_RUNTIME_VERSION) {
      throw new HostRuntimeUnavailableError(`The host UI runtime is version ${String(runtime.version)}, but this view was built for version ${SUPPORTED_RUNTIME_VERSION}. Update the plugin to match the host.`);
    }
    if (!runtime.React || !runtime.MantineCore || !runtime.ReactDOMClient) {
      throw new HostRuntimeUnavailableError("The host UI runtime is incomplete.");
    }
    return runtime;
  }
  var SUPPORTED_RUNTIME_VERSION = 1, HostRuntimeUnavailableError, hostRuntime, React, MantineCore, MantineHooks, hostTheme, createRoot, jsx, jsxs, Fragment, useState, useEffect, useCallback, useMemo, useRef;
  var init_host_runtime = __esm(() => {
    HostRuntimeUnavailableError = class HostRuntimeUnavailableError extends Error {
      constructor(message) {
        super(message);
        this.name = "HostRuntimeUnavailableError";
      }
    };
    hostRuntime = readRuntime();
    React = hostRuntime.React;
    MantineCore = hostRuntime.MantineCore;
    MantineHooks = hostRuntime.MantineHooks;
    hostTheme = hostRuntime.theme;
    createRoot = hostRuntime.ReactDOMClient.createRoot;
    jsx = hostRuntime.JsxRuntime.jsx;
    jsxs = hostRuntime.JsxRuntime.jsxs;
    Fragment = hostRuntime.JsxRuntime.Fragment;
    useState = hostRuntime.React.useState;
    useEffect = hostRuntime.React.useEffect;
    useCallback = hostRuntime.React.useCallback;
    useMemo = hostRuntime.React.useMemo;
    useRef = hostRuntime.React.useRef;
  });

  // examples/plugins/kiro-external/src/ui/shim/jsx-dev-runtime.ts
  var init_jsx_dev_runtime = __esm(() => {
    init_host_runtime();
  });

  // examples/plugins/kiro-external/src/ui/CredentialList.tsx
  function usePanelWidth() {
    const [width, setWidth] = useState(() => typeof document === "undefined" ? NARROW_PANEL_WIDTH : document.body.clientWidth);
    useEffect(() => {
      if (typeof ResizeObserver === "undefined")
        return;
      const observer = new ResizeObserver((entries) => {
        const next = entries[0]?.contentRect.width;
        if (typeof next === "number")
          setWidth(next);
      });
      observer.observe(document.body);
      return () => observer.disconnect();
    }, []);
    return width;
  }
  function UsageDisplay({ entry, usage }) {
    const current = entry.meterUsage ?? usage?.currentUsage;
    const limit = usage?.usageLimit;
    const title = entry.subscriptionTitle ?? usage?.subscriptionTitle;
    const tier = entry.tier ?? "free";
    const meta = getTierMeta(tier);
    const percent = limit && limit > 0 && current != null ? current / limit * 100 : null;
    return /* @__PURE__ */ jsx(Stack, {
      gap: 2,
      children: [
        /* @__PURE__ */ jsx(Group, {
          gap: 4,
          children: /* @__PURE__ */ jsx(Badge, {
            size: "xs",
            variant: "light",
            color: meta.color,
            children: title || meta.label
          }, undefined, false, undefined, this)
        }, undefined, false, undefined, this),
        current != null ? /* @__PURE__ */ jsx(Fragment, {
          children: [
            /* @__PURE__ */ jsx(Text, {
              size: "xs",
              children: [
                formatUsageNumber(current),
                limit != null ? ` / ${formatUsageNumber(limit)}` : ""
              ]
            }, undefined, true, undefined, this),
            percent != null ? /* @__PURE__ */ jsx(Progress, {
              value: Math.min(percent, 100),
              size: "xs",
              color: percent > 90 ? "red" : percent > 70 ? "yellow" : "blue",
              style: { width: 80 }
            }, undefined, false, undefined, this) : null
          ]
        }, undefined, true, undefined, this) : /* @__PURE__ */ jsx(Text, {
          size: "xs",
          c: "dimmed",
          children: "-"
        }, undefined, false, undefined, this),
        usage?.queriedAt ? /* @__PURE__ */ jsx(Text, {
          size: "xs",
          c: "dimmed",
          children: relativeTime(usage.queriedAt)
        }, undefined, false, undefined, this) : null
      ]
    }, undefined, true, undefined, this);
  }
  function StatusBadges({ entry }) {
    return /* @__PURE__ */ jsx(Group, {
      gap: 4,
      wrap: "wrap",
      children: [
        isApiKeyEntry(entry) ? /* @__PURE__ */ jsx(Badge, {
          color: "teal",
          size: "sm",
          variant: "light",
          children: t("apiKeyBadge")
        }, undefined, false, undefined, this) : null,
        entry.disabled ? /* @__PURE__ */ jsx(Badge, {
          color: "red",
          size: "sm",
          children: entry.disabledReason ?? t("disabled")
        }, undefined, false, undefined, this) : /* @__PURE__ */ jsx(Badge, {
          color: "green",
          size: "sm",
          children: t("enabled")
        }, undefined, false, undefined, this)
      ]
    }, undefined, true, undefined, this);
  }
  function ExpiryText({ entry }) {
    return /* @__PURE__ */ jsx(Text, {
      size: "xs",
      c: "dimmed",
      children: isApiKeyEntry(entry) ? t("apiKeyNeverExpires") : entry.expiresAt ? relativeTime(entry.expiresAt) : "-"
    }, undefined, false, undefined, this);
  }
  function CredentialActions({
    entry,
    busy,
    handlers
  }) {
    const isApiKey = isApiKeyEntry(entry);
    return /* @__PURE__ */ jsx(Group, {
      gap: "xs",
      wrap: "wrap",
      children: [
        /* @__PURE__ */ jsx(Switch, {
          size: "xs",
          checked: !entry.disabled,
          disabled: busy,
          onChange: () => handlers.onToggleEnabled(entry)
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Tooltip, {
          label: isApiKey ? t("apiKeyNoRefresh") : t("refreshToken"),
          withinPortal: false,
          children: /* @__PURE__ */ jsx(ActionIcon, {
            variant: "subtle",
            size: "sm",
            disabled: busy || isApiKey,
            onClick: () => handlers.onRefreshToken(entry),
            "aria-label": t("refreshToken"),
            children: "↻"
          }, undefined, false, undefined, this)
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Button, {
          variant: "subtle",
          size: "compact-xs",
          disabled: busy,
          onClick: () => handlers.onQueryUsage(entry),
          children: t("queryUsage")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Button, {
          variant: "subtle",
          size: "compact-xs",
          disabled: busy,
          onClick: () => handlers.onShowModels(entry),
          children: t("models")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Button, {
          variant: "subtle",
          size: "compact-xs",
          disabled: busy,
          onClick: () => handlers.onEdit(entry),
          children: t("edit")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Button, {
          variant: "subtle",
          size: "compact-xs",
          color: "orange",
          disabled: busy,
          onClick: () => handlers.onReset(entry),
          children: t("reset")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(ActionIcon, {
          variant: "subtle",
          color: "red",
          size: "sm",
          disabled: busy,
          onClick: () => handlers.onDelete(entry),
          "aria-label": t("actionRemove"),
          children: "✕"
        }, undefined, false, undefined, this)
      ]
    }, undefined, true, undefined, this);
  }
  function CredentialTable(props) {
    const ids = props.entries.map((entry) => entry.id);
    const selectedCount = ids.filter((id) => props.selectedIds.has(id)).length;
    return /* @__PURE__ */ jsx(Table, {
      striped: true,
      highlightOnHover: true,
      children: [
        /* @__PURE__ */ jsx(Table.Thead, {
          children: /* @__PURE__ */ jsx(Table.Tr, {
            children: [
              /* @__PURE__ */ jsx(Table.Th, {
                w: 40,
                children: /* @__PURE__ */ jsx(Checkbox, {
                  size: "xs",
                  checked: ids.length > 0 && selectedCount === ids.length,
                  indeterminate: selectedCount > 0 && selectedCount < ids.length,
                  onChange: () => props.onToggleSelectPage(ids),
                  "aria-label": t("selectAll")
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: "ID"
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colDisplayName")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: "Email"
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colPriority")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colRegion")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colStatus")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colExpires")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colStats")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colUsage")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colLastUsed")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Th, {
                children: t("colActions")
              }, undefined, false, undefined, this)
            ]
          }, undefined, true, undefined, this)
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Table.Tbody, {
          children: props.entries.map((entry) => /* @__PURE__ */ jsx(Table.Tr, {
            children: [
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Checkbox, {
                  size: "xs",
                  checked: props.selectedIds.has(entry.id),
                  onChange: () => props.onToggleSelect(entry.id)
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "xs",
                  ff: "monospace",
                  fw: entry.id === props.currentId ? 700 : 400,
                  children: entry.id
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "sm",
                  children: entry.displayName || "-"
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "sm",
                  children: entry.email ?? "-"
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "sm",
                  children: entry.priority
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "xs",
                  c: "dimmed",
                  children: formatRegions(entry)
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(StatusBadges, {
                  entry
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(ExpiryText, {
                  entry
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "xs",
                  children: [
                    t("statSuccess"),
                    ": ",
                    entry.successCount ?? 0,
                    " / ",
                    t("statFail"),
                    ":",
                    " ",
                    entry.failureCount ?? 0
                  ]
                }, undefined, true, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(UsageDisplay, {
                  entry,
                  usage: props.usageCache[entry.id]
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(Text, {
                  size: "xs",
                  c: "dimmed",
                  children: entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Table.Td, {
                children: /* @__PURE__ */ jsx(CredentialActions, {
                  entry,
                  busy: props.busy,
                  handlers: props
                }, undefined, false, undefined, this)
              }, undefined, false, undefined, this)
            ]
          }, entry.id, true, undefined, this))
        }, undefined, false, undefined, this)
      ]
    }, undefined, true, undefined, this);
  }
  function CredentialCards(props) {
    return /* @__PURE__ */ jsx(Stack, {
      gap: "xs",
      children: props.entries.map((entry) => {
        const isCurrent = entry.id === props.currentId;
        return /* @__PURE__ */ jsx(Paper, {
          withBorder: true,
          p: "sm",
          style: isCurrent ? { borderColor: "var(--mantine-color-indigo-5)", borderWidth: 2 } : undefined,
          children: /* @__PURE__ */ jsx(Stack, {
            gap: "xs",
            children: [
              /* @__PURE__ */ jsx(Group, {
                justify: "space-between",
                wrap: "wrap",
                children: [
                  /* @__PURE__ */ jsx(Group, {
                    gap: "xs",
                    children: [
                      /* @__PURE__ */ jsx(Checkbox, {
                        size: "xs",
                        checked: props.selectedIds.has(entry.id),
                        onChange: () => props.onToggleSelect(entry.id)
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        ff: "monospace",
                        children: entry.id
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "sm",
                        fw: isCurrent ? 700 : 500,
                        truncate: true,
                        style: { maxWidth: 200 },
                        children: entry.displayName || entry.email || entry.id
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this),
                  /* @__PURE__ */ jsx(StatusBadges, {
                    entry
                  }, undefined, false, undefined, this)
                ]
              }, undefined, true, undefined, this),
              /* @__PURE__ */ jsx(Stack, {
                gap: 4,
                children: [
                  entry.email ? /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: "Email"
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        children: entry.email
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this) : null,
                  /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: t("colRegion")
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        children: formatRegions(entry)
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this),
                  /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: t("colPriority")
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        children: entry.priority
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this),
                  /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: t("colExpires")
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(ExpiryText, {
                        entry
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this),
                  /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: t("colStats")
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        children: [
                          t("statSuccess"),
                          ": ",
                          entry.successCount ?? 0,
                          " / ",
                          t("statFail"),
                          ":",
                          " ",
                          entry.failureCount ?? 0
                        ]
                      }, undefined, true, undefined, this)
                    ]
                  }, undefined, true, undefined, this),
                  /* @__PURE__ */ jsx(Group, {
                    justify: "space-between",
                    wrap: "nowrap",
                    children: [
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: t("colLastUsed")
                      }, undefined, false, undefined, this),
                      /* @__PURE__ */ jsx(Text, {
                        size: "xs",
                        c: "dimmed",
                        children: entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"
                      }, undefined, false, undefined, this)
                    ]
                  }, undefined, true, undefined, this)
                ]
              }, undefined, true, undefined, this),
              /* @__PURE__ */ jsx(Stack, {
                gap: 4,
                children: [
                  /* @__PURE__ */ jsx(Text, {
                    size: "xs",
                    c: "dimmed",
                    children: t("colUsage")
                  }, undefined, false, undefined, this),
                  /* @__PURE__ */ jsx(UsageDisplay, {
                    entry,
                    usage: props.usageCache[entry.id]
                  }, undefined, false, undefined, this)
                ]
              }, undefined, true, undefined, this),
              /* @__PURE__ */ jsx(CredentialActions, {
                entry,
                busy: props.busy,
                handlers: props
              }, undefined, false, undefined, this)
            ]
          }, undefined, true, undefined, this)
        }, entry.id, false, undefined, this);
      })
    }, undefined, false, undefined, this);
  }
  function CredentialList(props) {
    const width = usePanelWidth();
    const narrow = width < NARROW_PANEL_WIDTH;
    const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize));
    const ids = props.entries.map((entry) => entry.id);
    const selectedCount = ids.filter((id) => props.selectedIds.has(id)).length;
    return /* @__PURE__ */ jsx(Stack, {
      gap: "xs",
      children: [
        narrow ? /* @__PURE__ */ jsx(Checkbox, {
          size: "xs",
          checked: ids.length > 0 && selectedCount === ids.length,
          indeterminate: selectedCount > 0 && selectedCount < ids.length,
          onChange: () => props.onToggleSelectPage(ids),
          label: `${t("selectAll")} (${ids.length})`
        }, undefined, false, undefined, this) : null,
        narrow ? /* @__PURE__ */ jsx(CredentialCards, {
          ...props
        }, undefined, false, undefined, this) : /* @__PURE__ */ jsx(CredentialTable, {
          ...props
        }, undefined, false, undefined, this),
        totalPages > 1 ? /* @__PURE__ */ jsx(Group, {
          justify: "center",
          gap: "sm",
          children: [
            /* @__PURE__ */ jsx(Pagination, {
              size: "sm",
              total: totalPages,
              value: props.page,
              onChange: props.onPageChange
            }, undefined, false, undefined, this),
            /* @__PURE__ */ jsx(Text, {
              size: "xs",
              c: "dimmed",
              children: t("pageInfo", { current: props.page, total: totalPages, count: props.total })
            }, undefined, false, undefined, this)
          ]
        }, undefined, true, undefined, this) : null
      ]
    }, undefined, true, undefined, this);
  }
  var ActionIcon, Badge, Button, Checkbox, Group, Pagination, Paper, Progress, Stack, Switch, Table, Text, Tooltip, NARROW_PANEL_WIDTH = 900;
  var init_CredentialList = __esm(() => {
    init_format();
    init_host_runtime();
    init_strings();
    init_jsx_dev_runtime();
    ({
      ActionIcon,
      Badge,
      Button,
      Checkbox,
      Group,
      Pagination,
      Paper,
      Progress,
      Stack,
      Switch,
      Table,
      Text,
      Tooltip
    } = MantineCore);
  });

  // examples/plugins/kiro-external/src/ui/Overlay.tsx
  function PanelOverlay({ title, onClose, children, footer }) {
    useEffect(() => {
      const onKeyDown = (event) => {
        if (event.key === "Escape")
          onClose();
      };
      window.addEventListener("keydown", onKeyDown);
      return () => window.removeEventListener("keydown", onKeyDown);
    }, [onClose]);
    return /* @__PURE__ */ jsx(Box, {
      style: {
        position: "absolute",
        inset: 0,
        zIndex: 200,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "var(--mantine-spacing-md)",
        background: "rgba(0, 0, 0, 0.6)"
      },
      onClick: onClose,
      children: /* @__PURE__ */ jsx(Paper2, {
        withBorder: true,
        radius: "md",
        shadow: "md",
        onClick: (event) => event.stopPropagation(),
        style: {
          width: "100%",
          maxWidth: 620,
          maxHeight: "100%",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden"
        },
        children: [
          /* @__PURE__ */ jsx(Box, {
            p: "sm",
            style: { borderBottom: "1px solid var(--mantine-color-dark-4)" },
            children: /* @__PURE__ */ jsx(Text2, {
              fw: 600,
              size: "sm",
              children: title
            }, undefined, false, undefined, this)
          }, undefined, false, undefined, this),
          /* @__PURE__ */ jsx(ScrollArea.Autosize, {
            mah: "100%",
            style: { flex: 1 },
            children: /* @__PURE__ */ jsx(Box, {
              p: "sm",
              children
            }, undefined, false, undefined, this)
          }, undefined, false, undefined, this),
          footer ? /* @__PURE__ */ jsx(Box, {
            p: "sm",
            style: { borderTop: "1px solid var(--mantine-color-dark-4)" },
            children: footer
          }, undefined, false, undefined, this) : null
        ]
      }, undefined, true, undefined, this)
    }, undefined, false, undefined, this);
  }
  function ConfirmOverlay({
    message,
    confirmLabel,
    confirmColor = "red",
    loading,
    onConfirm,
    onCancel
  }) {
    return /* @__PURE__ */ jsx(PanelOverlay, {
      title: t("confirmTitle"),
      onClose: onCancel,
      footer: /* @__PURE__ */ jsx(Group2, {
        justify: "flex-end",
        gap: "xs",
        children: [
          /* @__PURE__ */ jsx(Button2, {
            size: "xs",
            variant: "default",
            onClick: onCancel,
            children: t("confirmCancel")
          }, undefined, false, undefined, this),
          /* @__PURE__ */ jsx(Button2, {
            size: "xs",
            color: confirmColor,
            loading,
            onClick: onConfirm,
            children: confirmLabel ?? t("confirmOk")
          }, undefined, false, undefined, this)
        ]
      }, undefined, true, undefined, this),
      children: /* @__PURE__ */ jsx(Stack2, {
        gap: "xs",
        children: /* @__PURE__ */ jsx(Text2, {
          size: "sm",
          children: message
        }, undefined, false, undefined, this)
      }, undefined, false, undefined, this)
    }, undefined, false, undefined, this);
  }
  var Box, Button2, Group2, Paper2, ScrollArea, Stack2, Text2;
  var init_Overlay = __esm(() => {
    init_host_runtime();
    init_strings();
    init_jsx_dev_runtime();
    ({ Box, Button: Button2, Group: Group2, Paper: Paper2, ScrollArea, Stack: Stack2, Text: Text2 } = MantineCore);
  });

  // examples/plugins/kiro-external/src/ui/EditForm.tsx
  function normalizeAuthMethod(method) {
    const lower = method?.trim().toLowerCase();
    if (lower === "api_key" || lower === "apikey")
      return "api_key";
    if (lower === "idc" || lower === "iam" || lower === "builder-id")
      return "idc";
    return "social";
  }
  function EditForm({ entry, loading, error, onSubmit, onCancel }) {
    const [text, setText] = useState(() => {
      const initial = {};
      for (const { key } of TEXT_FIELDS) {
        const value = entry[key];
        initial[key] = typeof value === "string" ? value : "";
      }
      return initial;
    });
    const [secrets, setSecrets] = useState({
      refreshToken: "",
      apiKey: "",
      clientId: "",
      clientSecret: "",
      proxyPassword: ""
    });
    const [authMethod, setAuthMethod] = useState(normalizeAuthMethod(entry.authMethod));
    const [priority, setPriority] = useState(entry.priority ?? 0);
    const [localError, setLocalError] = useState();
    const isApiKey = authMethod === "api_key";
    const secretFields = [
      { key: "refreshToken", label: t("editRefreshToken"), stored: !entry.isApiKey },
      { key: "apiKey", label: t("editApiKey"), stored: Boolean(entry.isApiKey) },
      { key: "clientId", label: t("editClientId"), stored: Boolean(entry.hasClientId) },
      { key: "clientSecret", label: t("editClientSecret"), stored: Boolean(entry.hasClientSecret) },
      {
        key: "proxyPassword",
        label: t("editProxyPassword"),
        stored: Boolean(entry.hasProxyPassword)
      }
    ];
    const submit = () => {
      const patch = {};
      for (const { key } of TEXT_FIELDS) {
        const next = text[key].trim();
        const current = typeof entry[key] === "string" ? entry[key] : "";
        if (next !== current)
          patch[key] = next;
      }
      for (const { key } of secretFields) {
        const next = secrets[key].trim();
        if (next.length > 0)
          patch[key] = next;
      }
      if (authMethod !== normalizeAuthMethod(entry.authMethod))
        patch.authMethod = authMethod;
      if (priority !== (entry.priority ?? 0))
        patch.priority = priority;
      if (Object.keys(patch).length === 0) {
        setLocalError(t("editNoChanges"));
        return;
      }
      setLocalError(undefined);
      onSubmit(patch);
    };
    return /* @__PURE__ */ jsx(PanelOverlay, {
      title: t("editTitle", { id: entry.id }),
      onClose: onCancel,
      footer: /* @__PURE__ */ jsx(Group3, {
        justify: "flex-end",
        gap: "xs",
        children: [
          /* @__PURE__ */ jsx(Button3, {
            size: "xs",
            variant: "default",
            onClick: onCancel,
            children: t("editCancel")
          }, undefined, false, undefined, this),
          /* @__PURE__ */ jsx(Button3, {
            size: "xs",
            loading,
            onClick: submit,
            children: t("editSave")
          }, undefined, false, undefined, this)
        ]
      }, undefined, true, undefined, this),
      children: /* @__PURE__ */ jsx(Stack3, {
        gap: "xs",
        children: [
          error ?? localError ? /* @__PURE__ */ jsx(Alert, {
            color: "red",
            variant: "light",
            children: error ?? localError
          }, undefined, false, undefined, this) : null,
          /* @__PURE__ */ jsx(Text3, {
            size: "xs",
            c: "dimmed",
            children: t("editSecretHint")
          }, undefined, false, undefined, this),
          /* @__PURE__ */ jsx(Stack3, {
            gap: 4,
            children: [
              /* @__PURE__ */ jsx(Text3, {
                size: "xs",
                fw: 500,
                children: t("editAuthMethod")
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(SegmentedControl, {
                size: "xs",
                value: authMethod,
                onChange: (value) => setAuthMethod(value),
                data: [
                  { value: "social", label: "social" },
                  { value: "idc", label: "idc" },
                  { value: "api_key", label: "api_key" }
                ]
              }, undefined, false, undefined, this)
            ]
          }, undefined, true, undefined, this),
          /* @__PURE__ */ jsx(NumberInput, {
            size: "xs",
            label: t("editPriority"),
            description: t("editPriorityDesc"),
            value: priority,
            min: 0,
            max: 1e4,
            onChange: (value) => setPriority(Number(value) || 0)
          }, undefined, false, undefined, this),
          TEXT_FIELDS.map(({ key, label }) => /* @__PURE__ */ jsx(TextInput, {
            size: "xs",
            label,
            value: text[key],
            onChange: (event) => setText((prev) => ({ ...prev, [key]: event.currentTarget.value }))
          }, key, false, undefined, this)),
          secretFields.map(({ key, label, stored }) => /* @__PURE__ */ jsx(PasswordInput, {
            size: "xs",
            label: stored ? `${label} ●` : label,
            placeholder: stored ? t("editSecretStored") : undefined,
            value: secrets[key],
            disabled: isApiKey ? key === "refreshToken" || key === "clientId" || key === "clientSecret" : key === "apiKey",
            onChange: (event) => setSecrets((prev) => ({ ...prev, [key]: event.currentTarget.value }))
          }, key, false, undefined, this))
        ]
      }, undefined, true, undefined, this)
    }, undefined, false, undefined, this);
  }
  var Alert, Button3, Group3, NumberInput, SegmentedControl, Stack3, Text3, TextInput, PasswordInput, TEXT_FIELDS;
  var init_EditForm = __esm(() => {
    init_host_runtime();
    init_Overlay();
    init_strings();
    init_jsx_dev_runtime();
    ({
      Alert,
      Button: Button3,
      Group: Group3,
      NumberInput,
      SegmentedControl,
      Stack: Stack3,
      Text: Text3,
      TextInput,
      PasswordInput
    } = MantineCore);
    TEXT_FIELDS = [
      { key: "displayName", label: t("editDisplayName") },
      { key: "email", label: t("editEmail") },
      { key: "region", label: t("editRegion") },
      { key: "authRegion", label: t("editAuthRegion") },
      { key: "apiRegion", label: t("editApiRegion") },
      { key: "profileArn", label: t("editProfileArn") },
      { key: "subscriptionTitle", label: t("editSubscriptionTitle") },
      { key: "startUrl", label: t("editStartUrl") },
      { key: "machineId", label: t("editMachineId") },
      { key: "proxyUrl", label: t("editProxyUrl") },
      { key: "proxyUsername", label: t("editProxyUsername") }
    ];
  });

  // examples/plugins/kiro-external/src/ui/panel.tsx
  var exports_panel = {};
  __export(exports_panel, {
    mount: () => mount
  });
  function CredentialPanel({ sdk }) {
    const [status, setStatus] = useState();
    const [loadError, setLoadError] = useState();
    const [message, setMessage] = useState();
    const [busy, setBusy] = useState(false);
    const [pages, setPages] = useState({
      paid: 1,
      enterprise: 1,
      free: 1
    });
    const [selectedIds, setSelectedIds] = useState(() => new Set);
    const [importText, setImportText] = useState("");
    const [editing, setEditing] = useState();
    const [editError, setEditError] = useState();
    const [confirming, setConfirming] = useState();
    const [modelsDialog, setModelsDialog] = useState();
    const [usageDialog, setUsageDialog] = useState();
    const [, bumpLocale] = useState(0);
    useEffect(() => onLocaleChange(() => bumpLocale((value) => value + 1)), []);
    const unmounted = useRef(false);
    useEffect(() => {
      return () => {
        unmounted.current = true;
      };
    }, []);
    const command = useCallback((commandId, input) => runCommand(sdk, commandId, input), [sdk]);
    const refresh = useCallback(async (nextPages) => {
      const active = nextPages ?? pages;
      const output = await command("status", {
        pageSize: PAGE_SIZE,
        paidPage: active.paid,
        enterprisePage: active.enterprise,
        freePage: active.free
      });
      if (!output || !Array.isArray(output.entries)) {
        throw new Error("The status command returned no credential information");
      }
      if (unmounted.current)
        return output;
      setStatus(output);
      setLoadError(undefined);
      return output;
    }, [command, pages]);
    const modelsChangedLegacyRef = useRef(false);
    const notifyModelsChanged = useCallback(async () => {
      if (!modelsChangedLegacyRef.current) {
        try {
          await sdk.request("provider.modelsChanged");
          return;
        } catch {
          modelsChangedLegacyRef.current = true;
        }
      }
      sdk.notify("providerSettings.catalogInvalidated", { providerId: "kiro" });
    }, [sdk]);
    const run = useCallback((label, action, options) => {
      if (busy)
        return;
      setBusy(true);
      setMessage({ text: t("busyStatus", { label }), tone: "neutral" });
      action().then(async () => {
        if (options?.affectsCatalog)
          await notifyModelsChanged();
        await refresh();
        if (unmounted.current)
          return;
        setMessage({ text: t("doneStatus", { label }), tone: "good" });
      }).catch((error) => {
        if (unmounted.current)
          return;
        setMessage({
          text: t("failedStatus", { label, error: errorMessage(error) }),
          tone: "bad"
        });
      }).finally(() => {
        if (unmounted.current)
          return;
        setBusy(false);
      });
    }, [busy, refresh, notifyModelsChanged]);
    useEffect(() => {
      let cancelled = false;
      refresh().then(() => {
        if (cancelled)
          return;
        sdk.notify("kiro-external.settings.ready", {});
      }).catch((error) => {
        if (cancelled)
          return;
        setLoadError(errorMessage(error));
      });
      return () => {
        cancelled = true;
      };
    }, []);
    const changePage = useCallback((tier, page) => {
      const next = { ...pages, [tier]: page };
      setPages(next);
      refresh(next).catch((error) => {
        if (unmounted.current)
          return;
        setMessage({
          text: t("failedStatus", { label: t("actionRefresh"), error: errorMessage(error) }),
          tone: "bad"
        });
      });
    }, [pages, refresh]);
    const toggleSelect = useCallback((id) => {
      setSelectedIds((prev) => {
        const next = new Set(prev);
        if (next.has(id))
          next.delete(id);
        else
          next.add(id);
        return next;
      });
    }, []);
    const toggleSelectPage = useCallback((ids) => {
      setSelectedIds((prev) => {
        const next = new Set(prev);
        const allSelected = ids.length > 0 && ids.every((id) => next.has(id));
        for (const id of ids) {
          if (allSelected)
            next.delete(id);
          else
            next.add(id);
        }
        return next;
      });
    }, []);
    const handleImport = useCallback(() => {
      const raw = importText.trim();
      if (raw.length === 0) {
        setMessage({ text: t("importEmpty"), tone: "bad" });
        return;
      }
      let parsed;
      try {
        parsed = JSON.parse(raw);
      } catch {
        const keys = parseApiKeyLines(raw);
        if (keys.length > 0) {
          parsed = keys;
        } else if (containsApiKey(raw)) {
          parsed = [raw];
        } else {
          setMessage({ text: t("importInvalid"), tone: "bad" });
          return;
        }
      }
      setImportText("");
      run(t("actionImport"), async () => {
        const result = await command("credentials.import", { credentials: parsed });
        if (isRecord(result)) {
          setMessage({
            text: t("importSuccess", {
              added: Number(result.added ?? 0),
              duplicates: Number(result.duplicates ?? 0)
            }),
            tone: "good"
          });
        }
      }, { affectsCatalog: true });
    }, [importText, run, command]);
    const handleSubmitEdit = useCallback((patch) => {
      const entry = editing;
      if (!entry)
        return;
      setBusy(true);
      setEditError(undefined);
      command("credentials.update", { credentialId: entry.id, fields: patch }).then(async () => {
        if (unmounted.current)
          return;
        setEditing(undefined);
        await refresh();
        if (unmounted.current)
          return;
        setMessage({ text: t("doneStatus", { label: t("actionUpdate") }), tone: "good" });
      }).catch((error) => {
        if (unmounted.current)
          return;
        setEditError(errorMessage(error));
      }).finally(() => {
        if (unmounted.current)
          return;
        setBusy(false);
      });
    }, [editing, command, refresh]);
    const showModels = useCallback((entry) => {
      setModelsDialog({ entry });
      command("models.list", { credentialId: entry.id }).then((result) => {
        if (unmounted.current)
          return;
        const models = isRecord(result) && Array.isArray(result.models) ? result.models : [];
        setModelsDialog({ entry, models });
      }).catch((error) => {
        if (unmounted.current)
          return;
        setModelsDialog({ entry, error: errorMessage(error) });
      });
    }, [command]);
    const showUsage = useCallback((entry) => {
      setUsageDialog({ entry });
      command("credentials.usage", { credentialId: entry.id }).then(async (result) => {
        if (unmounted.current)
          return;
        const usage = isRecord(result) && isRecord(result.usage) ? result.usage : undefined;
        setUsageDialog({ entry, usage });
        await refresh().catch(() => {});
      }).catch((error) => {
        if (unmounted.current)
          return;
        setUsageDialog({ entry, error: errorMessage(error) });
      });
    }, [command, refresh]);
    const groups = useMemo(() => {
      if (!status)
        return [];
      const byTier = {
        paid: { entries: status.paidEntries ?? [], total: status.paidTotal ?? 0 },
        enterprise: {
          entries: status.enterpriseEntries ?? [],
          total: status.enterpriseTotal ?? 0
        },
        free: { entries: status.freeEntries ?? [], total: status.freeTotal ?? 0 }
      };
      return TIER_ORDER.filter((tier) => byTier[tier].total > 0).map((tier) => ({
        tier,
        ...byTier[tier]
      }));
    }, [status]);
    if (loadError) {
      return /* @__PURE__ */ jsx(Alert2, {
        color: "red",
        variant: "light",
        title: t("runtimeMissingTitle"),
        children: loadError
      }, undefined, false, undefined, this);
    }
    if (!status) {
      return /* @__PURE__ */ jsx(Group4, {
        justify: "center",
        p: "lg",
        children: /* @__PURE__ */ jsx(Loader, {
          size: "sm"
        }, undefined, false, undefined, this)
      }, undefined, false, undefined, this);
    }
    const usageCache = status.usageCache ?? {};
    const unhealthyTotal = status.unhealthyTotal ?? 0;
    return /* @__PURE__ */ jsx(Stack4, {
      gap: "md",
      children: [
        /* @__PURE__ */ jsx(Group4, {
          justify: "space-between",
          children: [
            /* @__PURE__ */ jsx(Text4, {
              fw: 500,
              size: "sm",
              children: t("credentials")
            }, undefined, false, undefined, this),
            /* @__PURE__ */ jsx(Badge2, {
              size: "sm",
              color: status.available > 0 ? "green" : "gray",
              children: [
                status.available,
                " / ",
                status.total
              ]
            }, undefined, true, undefined, this)
          ]
        }, undefined, true, undefined, this),
        /* @__PURE__ */ jsx(Text4, {
          size: "xs",
          c: "dimmed",
          children: t("credentialsDesc")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Group4, {
          gap: "xs",
          align: "center",
          children: [
            /* @__PURE__ */ jsx(Text4, {
              size: "sm",
              children: [
                t("loadBalancing"),
                ":"
              ]
            }, undefined, true, undefined, this),
            /* @__PURE__ */ jsx(SegmentedControl2, {
              size: "xs",
              value: status.loadBalancingMode,
              disabled: busy,
              onChange: (value) => run(t("actionSwitchMode", { mode: value }), () => command("config.setLoadBalancing", { mode: value })),
              data: [
                { value: "priority", label: t("modePriority") },
                { value: "balanced", label: t("modeBalanced") }
              ]
            }, undefined, false, undefined, this)
          ]
        }, undefined, true, undefined, this),
        /* @__PURE__ */ jsx(Divider, {}, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Text4, {
          fw: 500,
          size: "sm",
          children: t("importTitle")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Textarea, {
          size: "xs",
          placeholder: t("importPlaceholder"),
          value: importText,
          onChange: (event) => setImportText(event.currentTarget.value),
          minRows: 2,
          autosize: true,
          styles: { input: { fontFamily: "monospace", fontSize: 12 } }
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Text4, {
          size: "xs",
          c: "dimmed",
          children: t("importHint")
        }, undefined, false, undefined, this),
        /* @__PURE__ */ jsx(Button4, {
          size: "xs",
          variant: "light",
          style: { alignSelf: "flex-start" },
          disabled: busy || importText.trim().length === 0,
          onClick: handleImport,
          children: t("importBtn")
        }, undefined, false, undefined, this),
        status.total === 0 ? /* @__PURE__ */ jsx(Text4, {
          c: "dimmed",
          size: "sm",
          children: t("noCredentials")
        }, undefined, false, undefined, this) : null,
        status.total > 0 ? /* @__PURE__ */ jsx(Group4, {
          gap: "xs",
          justify: "flex-end",
          align: "center",
          wrap: "wrap",
          children: [
            /* @__PURE__ */ jsx(Button4, {
              size: "compact-xs",
              variant: "light",
              disabled: busy,
              onClick: () => setConfirming({
                message: t("refreshAllModelsConfirm"),
                label: t("actionRefreshAllModels"),
                run: () => command("models.refreshAll"),
                affectsCatalog: true
              }),
              children: t("refreshAllModels")
            }, undefined, false, undefined, this),
            selectedIds.size > 0 ? /* @__PURE__ */ jsx(Button4, {
              size: "compact-xs",
              color: "red",
              variant: "light",
              disabled: busy,
              onClick: () => setConfirming({
                message: t("batchDeleteConfirm", { count: selectedIds.size }),
                label: t("actionBatchDelete"),
                run: async () => {
                  const ids = [...selectedIds];
                  const result = await command("credentials.removeMany", {
                    credentialIds: ids
                  });
                  setSelectedIds(new Set);
                  const removed = isRecord(result) && Array.isArray(result.removed) ? result.removed.length : ids.length;
                  setMessage({ text: t("batchDeleteSuccess", { count: removed }), tone: "good" });
                },
                affectsCatalog: true
              }),
              children: [
                t("batchDelete"),
                " (",
                selectedIds.size,
                ")"
              ]
            }, undefined, true, undefined, this) : null,
            /* @__PURE__ */ jsx(Button4, {
              size: "compact-xs",
              color: "red",
              variant: "outline",
              disabled: busy || unhealthyTotal === 0,
              onClick: () => setConfirming({
                message: t("deleteUnhealthyConfirm", { count: unhealthyTotal }),
                label: t("actionDeleteUnhealthy"),
                run: async () => {
                  const result = await command("credentials.removeUnhealthy");
                  const removed = isRecord(result) && Array.isArray(result.removed) ? result.removed.length : 0;
                  setMessage({
                    text: t("deleteUnhealthySuccess", { count: removed }),
                    tone: "good"
                  });
                },
                affectsCatalog: true
              }),
              children: [
                t("deleteUnhealthy"),
                " (",
                unhealthyTotal,
                ")"
              ]
            }, undefined, true, undefined, this)
          ]
        }, undefined, true, undefined, this) : null,
        groups.map(({ tier, entries, total }) => {
          const meta = getTierMeta(tier);
          return /* @__PURE__ */ jsx(Stack4, {
            gap: "xs",
            children: [
              /* @__PURE__ */ jsx(Group4, {
                gap: "xs",
                children: [
                  /* @__PURE__ */ jsx(Badge2, {
                    color: meta.color,
                    size: "sm",
                    variant: "filled",
                    children: meta.label
                  }, undefined, false, undefined, this),
                  /* @__PURE__ */ jsx(Text4, {
                    size: "xs",
                    c: "dimmed",
                    children: [
                      meta.desc,
                      " · ",
                      total
                    ]
                  }, undefined, true, undefined, this)
                ]
              }, undefined, true, undefined, this),
              /* @__PURE__ */ jsx(CredentialList, {
                entries,
                total,
                page: pages[tier],
                pageSize: PAGE_SIZE,
                onPageChange: (page) => changePage(tier, page),
                currentId: status.currentId,
                usageCache,
                selectedIds,
                onToggleSelect: toggleSelect,
                onToggleSelectPage: toggleSelectPage,
                busy,
                onToggleEnabled: (entry) => run(entry.disabled ? t("actionEnable") : t("actionDisable"), () => command("credentials.setState", {
                  credentialId: entry.id,
                  state: entry.disabled ? "enabled" : "disabled"
                }), { affectsCatalog: true }),
                onRefreshToken: (entry) => run(t("actionRefresh"), () => command("credentials.refresh", { credentialId: entry.id })),
                onReset: (entry) => run(t("reset"), () => command("credentials.setState", { credentialId: entry.id, state: "reset" }), { affectsCatalog: true }),
                onEdit: (entry) => {
                  setEditError(undefined);
                  setEditing(entry);
                },
                onDelete: (entry) => setConfirming({
                  message: t("deleteConfirm"),
                  label: t("actionRemove"),
                  run: () => command("credentials.remove", { credentialId: entry.id }),
                  affectsCatalog: true
                }),
                onQueryUsage: showUsage,
                onShowModels: showModels
              }, undefined, false, undefined, this)
            ]
          }, tier, true, undefined, this);
        }),
        message ? /* @__PURE__ */ jsx(Text4, {
          size: "xs",
          c: message.tone === "bad" ? "red" : message.tone === "good" ? "green" : "dimmed",
          children: message.text
        }, undefined, false, undefined, this) : null,
        editing ? /* @__PURE__ */ jsx(EditForm, {
          entry: editing,
          loading: busy,
          error: editError,
          onSubmit: handleSubmitEdit,
          onCancel: () => {
            setEditing(undefined);
            setEditError(undefined);
          }
        }, undefined, false, undefined, this) : null,
        confirming ? /* @__PURE__ */ jsx(ConfirmOverlay, {
          message: confirming.message,
          loading: busy,
          onCancel: () => setConfirming(undefined),
          onConfirm: () => {
            const pending = confirming;
            setConfirming(undefined);
            run(pending.label, pending.run, { affectsCatalog: pending.affectsCatalog });
          }
        }, undefined, false, undefined, this) : null,
        modelsDialog ? /* @__PURE__ */ jsx(PanelOverlay, {
          title: `${t("models")} · ${modelsDialog.entry.displayName || modelsDialog.entry.id}`,
          onClose: () => setModelsDialog(undefined),
          children: modelsDialog.error ? /* @__PURE__ */ jsx(Alert2, {
            color: "red",
            variant: "light",
            children: modelsDialog.error
          }, undefined, false, undefined, this) : !modelsDialog.models ? /* @__PURE__ */ jsx(Group4, {
            justify: "center",
            p: "md",
            children: [
              /* @__PURE__ */ jsx(Loader, {
                size: "xs"
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Text4, {
                size: "xs",
                c: "dimmed",
                children: t("modelsLoading")
              }, undefined, false, undefined, this)
            ]
          }, undefined, true, undefined, this) : modelsDialog.models.length === 0 ? /* @__PURE__ */ jsx(Text4, {
            size: "sm",
            c: "dimmed",
            children: t("modelsNone")
          }, undefined, false, undefined, this) : /* @__PURE__ */ jsx(Stack4, {
            gap: 4,
            children: [
              /* @__PURE__ */ jsx(Text4, {
                size: "xs",
                fw: 500,
                children: t("modelsCount", { count: modelsDialog.models.length })
              }, undefined, false, undefined, this),
              modelsDialog.models.map((model) => /* @__PURE__ */ jsx(Group4, {
                justify: "space-between",
                wrap: "nowrap",
                children: [
                  /* @__PURE__ */ jsx(Text4, {
                    size: "xs",
                    ff: "monospace",
                    children: model.id
                  }, undefined, false, undefined, this),
                  model.contextWindow ? /* @__PURE__ */ jsx(Text4, {
                    size: "xs",
                    c: "dimmed",
                    children: [
                      Math.round(model.contextWindow / 1024),
                      "k"
                    ]
                  }, undefined, true, undefined, this) : null
                ]
              }, model.id, true, undefined, this))
            ]
          }, undefined, true, undefined, this)
        }, undefined, false, undefined, this) : null,
        usageDialog ? /* @__PURE__ */ jsx(PanelOverlay, {
          title: `${t("colUsage")} · ${usageDialog.entry.displayName || usageDialog.entry.id}`,
          onClose: () => setUsageDialog(undefined),
          children: usageDialog.error ? /* @__PURE__ */ jsx(Alert2, {
            color: "red",
            variant: "light",
            children: usageDialog.error
          }, undefined, false, undefined, this) : !usageDialog.usage ? /* @__PURE__ */ jsx(Group4, {
            justify: "center",
            p: "md",
            children: [
              /* @__PURE__ */ jsx(Loader, {
                size: "xs"
              }, undefined, false, undefined, this),
              /* @__PURE__ */ jsx(Text4, {
                size: "xs",
                c: "dimmed",
                children: t("usageLoading")
              }, undefined, false, undefined, this)
            ]
          }, undefined, true, undefined, this) : /* @__PURE__ */ jsx(Stack4, {
            gap: 4,
            children: [
              usageDialog.usage.subscriptionTitle ? /* @__PURE__ */ jsx(Text4, {
                size: "sm",
                children: [
                  t("usagePlan"),
                  ": ",
                  usageDialog.usage.subscriptionTitle
                ]
              }, undefined, true, undefined, this) : null,
              /* @__PURE__ */ jsx(Text4, {
                size: "sm",
                children: [
                  t("colUsage"),
                  ": ",
                  usageDialog.usage.currentUsage ?? 0,
                  " /",
                  " ",
                  usageDialog.usage.usageLimit ?? "-",
                  usageDialog.usage.usagePercentage != null ? ` (${usageDialog.usage.usagePercentage}%)` : ""
                ]
              }, undefined, true, undefined, this),
              /* @__PURE__ */ jsx(Text4, {
                size: "sm",
                children: [
                  t("usageRemaining"),
                  ": ",
                  usageDialog.usage.remaining ?? "-"
                ]
              }, undefined, true, undefined, this),
              usageDialog.usage.overageStatus ? /* @__PURE__ */ jsx(Text4, {
                size: "sm",
                children: [
                  t("usageOverage"),
                  ": ",
                  usageDialog.usage.overageStatus
                ]
              }, undefined, true, undefined, this) : null
            ]
          }, undefined, true, undefined, this)
        }, undefined, false, undefined, this) : null
      ]
    }, undefined, true, undefined, this);
  }
  function startHeightReporting(sdk) {
    let supported = true;
    let inFlight = false;
    let lastReported = 0;
    const report = () => {
      if (!supported || inFlight)
        return;
      const height = Math.ceil(document.documentElement.scrollHeight);
      if (height <= 0 || height === lastReported)
        return;
      inFlight = true;
      sdk.request("panel.setHeight", { height }).then(() => {
        lastReported = height;
      }).catch((error) => {
        const code = error?.code;
        if (code === "NOT_SUPPORTED" || code === "METHOD_NOT_FOUND")
          supported = false;
      }).finally(() => {
        inFlight = false;
        const current = Math.ceil(document.documentElement.scrollHeight);
        if (current > 0 && current !== lastReported)
          report();
      });
    };
    const observer = new ResizeObserver(report);
    observer.observe(document.body);
    report();
  }
  function mount(sdk) {
    const root = document.createElement("div");
    root.style.position = "relative";
    root.style.minHeight = "100%";
    root.style.padding = "12px";
    root.style.boxSizing = "border-box";
    document.body.style.margin = "0";
    document.body.appendChild(root);
    createRoot(root).render(/* @__PURE__ */ jsx(MantineProvider, {
      theme: hostTheme,
      children: /* @__PURE__ */ jsx(Box2, {
        children: /* @__PURE__ */ jsx(PanelErrorBoundary, {
          children: /* @__PURE__ */ jsx(CredentialPanel, {
            sdk
          }, undefined, false, undefined, this)
        }, undefined, false, undefined, this)
      }, undefined, false, undefined, this)
    }, undefined, false, undefined, this));
    startHeightReporting(sdk);
  }
  var Alert2, Badge2, Box2, Button4, Divider, Group4, Loader, MantineProvider, SegmentedControl2, Stack4, Text4, Textarea, PAGE_SIZE = 20, TIER_ORDER, HostComponent, PanelErrorBoundary;
  var init_panel = __esm(() => {
    init_CredentialList();
    init_EditForm();
    init_format();
    init_host_runtime();
    init_Overlay();
    init_strings();
    init_jsx_dev_runtime();
    ({
      Alert: Alert2,
      Badge: Badge2,
      Box: Box2,
      Button: Button4,
      Divider,
      Group: Group4,
      Loader,
      MantineProvider,
      SegmentedControl: SegmentedControl2,
      Stack: Stack4,
      Text: Text4,
      Textarea
    } = MantineCore);
    TIER_ORDER = ["paid", "enterprise", "free"];
    HostComponent = React.Component;
    PanelErrorBoundary = class PanelErrorBoundary extends HostComponent {
      state = {};
      static getDerivedStateFromError(error) {
        return { message: errorMessage(error) };
      }
      render() {
        const { message } = this.state;
        if (message) {
          return /* @__PURE__ */ jsx(Alert2, {
            color: "red",
            variant: "light",
            title: t("runtimeMissingTitle"),
            children: message
          }, undefined, false, undefined, this);
        }
        return this.props.children;
      }
    };
  });

  // examples/plugins/kiro-external/src/ui/provider-settings.tsx
  init_strings();
  function renderFatal(detail) {
    const box = document.createElement("div");
    box.style.padding = "12px";
    box.style.font = "13px/1.5 var(--nf-font, system-ui, sans-serif)";
    box.style.color = "var(--nf-color-error, #ffa8a8)";
    box.style.background = "var(--nf-color-surface, #2e1a1a)";
    box.style.borderRadius = "6px";
    box.style.margin = "12px";
    box.style.whiteSpace = "pre-wrap";
    box.style.wordBreak = "break-word";
    const title = document.createElement("div");
    title.style.fontWeight = "600";
    title.style.marginBottom = "4px";
    title.textContent = t("runtimeMissingTitle");
    box.appendChild(title);
    const body = document.createElement("div");
    body.textContent = detail;
    box.appendChild(body);
    document.body.style.margin = "0";
    document.body.appendChild(box);
  }
  var sdk = globalThis.narrafork;
  if (sdk) {
    Promise.resolve().then(() => (init_panel(), exports_panel)).then(({ mount: mount2 }) => {
      try {
        mount2(sdk);
      } catch (error) {
        renderFatal(errorMessage(error));
      }
    }, (error) => renderFatal(errorMessage(error)));
  } else {
    renderFatal("The host did not provide the plugin UI SDK (globalThis.narrafork).");
  }
})();
