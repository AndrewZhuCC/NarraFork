(() => {
  // examples/plugins/kiro-external/src/ui/provider-settings.ts
  var COLORS = {
    text: "#e6e6e6",
    muted: "#8b8b8b",
    bad: "#ff8a8a",
    good: "#7ee0a2",
    warn: "#c9a227",
    panel: "#2a2a2e",
    border: "#3a3a40",
    highlight: "#3a3a5e"
  };
  (() => {
    const sdk = globalThis.narrafork;
    if (!sdk)
      return;
    const root = document.body;
    const style = (element2, css) => {
      Object.assign(element2.style, css);
    };
    function element(tag, css = {}, text) {
      const node = document.createElement(tag);
      if (text !== undefined)
        node.textContent = text;
      style(node, css);
      return node;
    }
    function button(label, onClick, tone = "normal") {
      const node = element("button", {
        padding: "4px 10px",
        font: "12px system-ui, sans-serif",
        color: tone === "bad" ? COLORS.bad : COLORS.text,
        background: COLORS.panel,
        border: `1px solid ${COLORS.border}`,
        borderRadius: "5px",
        cursor: "pointer",
        marginRight: "6px"
      });
      node.textContent = label;
      node.addEventListener("click", onClick);
      return node;
    }
    function isRecord(value) {
      return typeof value === "object" && value !== null && !Array.isArray(value);
    }
    let busy = false;
    async function command(commandId, input) {
      const raw = await sdk.request("commands.execute", {
        commandId,
        ...input === undefined ? {} : { input }
      });
      if (isRecord(raw) && "output" in raw)
        return raw.output;
      return raw;
    }
    const header = element("div", {
      font: "600 14px/1.9 system-ui, sans-serif",
      color: COLORS.text
    });
    header.textContent = "Kiro credentials";
    root.appendChild(header);
    const summary = element("div", { font: "13px/1.7 system-ui, sans-serif", color: COLORS.muted });
    root.appendChild(summary);
    const toolbar = element("div", { margin: "10px 0" });
    root.appendChild(toolbar);
    const list = element("div", {});
    root.appendChild(list);
    const modelsSection = element("div", { marginTop: "14px" });
    root.appendChild(modelsSection);
    const importArea = element("div", { marginTop: "14px" });
    root.appendChild(importArea);
    const status = element("div", {
      font: "12px/1.7 system-ui, sans-serif",
      color: COLORS.muted,
      marginTop: "10px",
      minHeight: "18px"
    });
    root.appendChild(status);
    function setStatus(message, tone = "normal") {
      status.textContent = message;
      status.style.color = tone === "bad" ? COLORS.bad : tone === "good" ? COLORS.good : COLORS.muted;
    }
    function setBusy(isBusy) {
      busy = isBusy;
      for (const btn of root.querySelectorAll("button")) {
        btn.disabled = isBusy;
        btn.style.opacity = isBusy ? "0.5" : "1";
      }
    }
    function run(label, action) {
      if (busy)
        return;
      setBusy(true);
      setStatus(`${label}…`);
      action().then(() => {
        setStatus(`${label} done.`, "good");
        return refresh();
      }).catch((error) => {
        setStatus(`${label} failed: ${String(error)}`, "bad");
      }).finally(() => setBusy(false));
    }
    function tierLabel(tier) {
      if (!tier)
        return "";
      if (tier === "enterprise")
        return "Enterprise";
      if (tier === "paid")
        return "Paid";
      return "Free";
    }
    function formatTimestamp(iso) {
      if (!iso)
        return "";
      const date = new Date(typeof iso === "number" ? iso < 10000000000 ? iso * 1000 : iso : iso);
      if (Number.isNaN(date.getTime()))
        return String(iso);
      return date.toLocaleString();
    }
    function renderUsageInline(container, credId) {
      const usageBox = element("div", {
        marginTop: "6px",
        padding: "6px 10px",
        background: COLORS.panel,
        borderRadius: "5px",
        font: "12px/1.6 system-ui, sans-serif",
        color: COLORS.muted
      });
      usageBox.textContent = "Loading usage…";
      container.appendChild(usageBox);
      command("credentials.usage", { credentialId: credId }).then((result) => {
        const usage = isRecord(result) ? result.usage : undefined;
        if (!usage || !isRecord(usage)) {
          usageBox.textContent = "No usage data available.";
          return;
        }
        usageBox.replaceChildren();
        const lines = [];
        if (usage.subscriptionTitle)
          lines.push(`Plan: ${usage.subscriptionTitle}`);
        lines.push(`Usage: ${usage.currentUsage} / ${usage.usageLimit} (${usage.usagePercentage}%)`);
        lines.push(`Remaining: ${usage.remaining}`);
        if (usage.nextResetAt)
          lines.push(`Resets: ${formatTimestamp(usage.nextResetAt)}`);
        if (usage.overageStatus) {
          lines.push(`Overage: ${usage.overageStatus}${usage.currentOverages ? ` ($${(usage.currentOverages / 100).toFixed(2)})` : ""}`);
        }
        for (const line of lines) {
          const p = element("div", {});
          p.textContent = line;
          usageBox.appendChild(p);
        }
      }).catch((error) => {
        usageBox.textContent = `Usage failed: ${String(error)}`;
        usageBox.style.color = COLORS.bad;
      });
    }
    function renderEditForm(container, entry) {
      const form = element("div", {
        marginTop: "8px",
        padding: "8px 10px",
        background: COLORS.panel,
        borderRadius: "5px",
        border: `1px solid ${COLORS.border}`
      });
      const formTitle = element("div", {
        font: "600 12px/1.6 system-ui, sans-serif",
        color: COLORS.text,
        marginBottom: "6px"
      });
      formTitle.textContent = "Edit credential";
      form.appendChild(formTitle);
      const hint = element("div", {
        font: "11px/1.5 system-ui, sans-serif",
        color: COLORS.muted,
        marginBottom: "8px"
      });
      hint.textContent = "Secret fields: leave blank to keep existing value. Non-secret fields: blank clears.";
      form.appendChild(hint);
      const textFields = [
        { key: "displayName", label: "Display name", current: entry.displayName },
        { key: "email", label: "Email", current: entry.email },
        { key: "region", label: "Region", current: entry.region },
        { key: "authRegion", label: "Auth region", current: entry.authRegion },
        { key: "apiRegion", label: "API region", current: entry.apiRegion },
        { key: "profileArn", label: "Profile ARN", current: entry.profileArn },
        { key: "subscriptionTitle", label: "Subscription title", current: entry.subscriptionTitle },
        { key: "startUrl", label: "Start URL", current: entry.startUrl },
        { key: "machineId", label: "Machine ID", current: entry.machineId },
        { key: "proxyUrl", label: "Proxy URL", current: entry.proxyUrl },
        { key: "proxyUsername", label: "Proxy username", current: entry.proxyUsername }
      ];
      const secretFields = [
        { key: "refreshToken", label: "Refresh token", hasValue: !entry.isApiKey },
        { key: "apiKey", label: "API key (ksk_…)", hasValue: entry.isApiKey },
        { key: "clientId", label: "Client ID", hasValue: entry.hasClientId },
        { key: "clientSecret", label: "Client secret", hasValue: entry.hasClientSecret },
        { key: "proxyPassword", label: "Proxy password", hasValue: entry.hasProxyPassword }
      ];
      const inputs = {};
      for (const { key, label, current } of textFields) {
        const row = element("div", { marginBottom: "4px" });
        const lbl = element("label", {
          font: "11px system-ui, sans-serif",
          color: COLORS.muted,
          display: "inline-block",
          width: "120px"
        });
        lbl.textContent = label;
        row.appendChild(lbl);
        const inp = element("input", {
          padding: "2px 6px",
          font: "12px ui-monospace, monospace",
          color: COLORS.text,
          background: "#1a1a1e",
          border: `1px solid ${COLORS.border}`,
          borderRadius: "3px",
          width: "220px"
        });
        inp.value = current ?? "";
        inputs[key] = inp;
        row.appendChild(inp);
        form.appendChild(row);
      }
      const authRow = element("div", { marginBottom: "4px" });
      const authLbl = element("label", {
        font: "11px system-ui, sans-serif",
        color: COLORS.muted,
        display: "inline-block",
        width: "120px"
      });
      authLbl.textContent = "Auth method";
      authRow.appendChild(authLbl);
      const authSelect = element("select", {
        padding: "2px 6px",
        font: "12px system-ui, sans-serif",
        color: COLORS.text,
        background: "#1a1a1e",
        border: `1px solid ${COLORS.border}`,
        borderRadius: "3px"
      });
      for (const opt of ["", "social", "idc", "api_key"]) {
        const o = document.createElement("option");
        o.value = opt;
        o.textContent = opt || "(keep current)";
        if (opt === entry.authMethod)
          o.selected = true;
        authSelect.appendChild(o);
      }
      authRow.appendChild(authSelect);
      form.appendChild(authRow);
      const secretHeader = element("div", {
        font: "11px system-ui, sans-serif",
        color: COLORS.warn,
        marginTop: "8px",
        marginBottom: "4px"
      });
      secretHeader.textContent = "Secrets (leave blank to keep existing)";
      form.appendChild(secretHeader);
      for (const { key, label, hasValue } of secretFields) {
        const row = element("div", { marginBottom: "4px" });
        const lbl = element("label", {
          font: "11px system-ui, sans-serif",
          color: COLORS.muted,
          display: "inline-block",
          width: "120px"
        });
        lbl.textContent = `${label}${hasValue ? " ●" : ""}`;
        row.appendChild(lbl);
        const inp = element("input", {
          padding: "2px 6px",
          font: "12px ui-monospace, monospace",
          color: COLORS.text,
          background: "#1a1a1e",
          border: `1px solid ${COLORS.border}`,
          borderRadius: "3px",
          width: "220px"
        });
        inp.type = "password";
        inp.placeholder = hasValue ? "(stored)" : "(none)";
        inputs[key] = inp;
        row.appendChild(inp);
        form.appendChild(row);
      }
      const btnRow = element("div", { marginTop: "8px" });
      btnRow.appendChild(button("Save", () => {
        const fields = {};
        for (const { key } of textFields) {
          const val = inputs[key].value;
          if (val !== (entry[key] ?? "")) {
            fields[key] = val;
          }
        }
        for (const { key } of secretFields) {
          const val = inputs[key].value;
          if (val.trim())
            fields[key] = val;
        }
        if (authSelect.value && authSelect.value !== entry.authMethod) {
          fields.authMethod = authSelect.value;
        }
        if (Object.keys(fields).length === 0) {
          setStatus("No changes to save.", "normal");
          return;
        }
        run("Update credential", () => command("credentials.update", { credentialId: entry.id, fields }));
      }));
      btnRow.appendChild(button("Cancel", () => {
        form.remove();
      }));
      form.appendChild(btnRow);
      container.appendChild(form);
    }
    function renderEntry(entry, currentId) {
      const isCurrent = entry.id === currentId;
      const row = element("div", {
        border: `1px solid ${isCurrent ? COLORS.highlight : COLORS.border}`,
        borderRadius: "6px",
        padding: "8px 10px",
        marginBottom: "8px",
        background: isCurrent ? "#282838" : "#232327"
      });
      const title = element("div", {
        font: "600 13px/1.6 system-ui, sans-serif",
        color: entry.disabled ? COLORS.warn : COLORS.text
      });
      const name = entry.displayName || entry.email || entry.id;
      const currentMark = isCurrent ? " ◆" : "";
      const tierStr = entry.tier ? ` [${tierLabel(entry.tier)}]` : "";
      title.textContent = `${entry.disabled ? "○" : "●"}  ${name}${tierStr}${currentMark}`;
      row.appendChild(title);
      const facts = [
        `id ${entry.id}`,
        `priority ${entry.priority}`,
        entry.authMethod ? `auth ${entry.authMethod}` : undefined,
        entry.isApiKey ? "API key (never expires)" : undefined,
        entry.disabled ? `disabled${entry.disabledReason ? ` (${entry.disabledReason})` : ""}` : undefined,
        entry.failureCount ? `failures: ${entry.failureCount}` : undefined,
        entry.successCount ? `successes: ${entry.successCount}` : undefined,
        entry.lastUsedAt ? `last used ${formatTimestamp(entry.lastUsedAt)}` : undefined,
        entry.meterUsage !== undefined ? `${entry.meterUsage} credits used` : undefined,
        entry.hasProfileArn ? "profile ARN set" : undefined,
        entry.region ? `region ${entry.region}` : undefined,
        entry.expiresAt ? `expires ${formatTimestamp(entry.expiresAt)}` : undefined,
        entry.availableModels?.length ? `${entry.availableModels.length} models` : undefined
      ].filter((value) => Boolean(value));
      row.appendChild(element("div", { font: "12px/1.7 ui-monospace, monospace", color: COLORS.muted }, facts.join("  ·  ")));
      const actions = element("div", { marginTop: "6px" });
      actions.appendChild(button(entry.disabled ? "Enable" : "Disable", () => run(entry.disabled ? "Enable" : "Disable", () => command("credentials.setState", {
        credentialId: entry.id,
        state: entry.disabled ? "enabled" : "disabled"
      }))));
      actions.appendChild(button("Reset", () => run("Reset", () => command("credentials.setState", { credentialId: entry.id, state: "reset" }))));
      actions.appendChild(button("Refresh token", () => run("Refresh", () => command("credentials.refresh", { credentialId: entry.id }))));
      actions.appendChild(button("Usage", () => {
        const existing = row.querySelector("[data-usage]");
        if (existing) {
          existing.remove();
          return;
        }
        const box = element("div", {});
        box.setAttribute("data-usage", "1");
        renderUsageInline(box, entry.id);
        row.appendChild(box);
      }));
      actions.appendChild(button("Edit", () => {
        const existing = row.querySelector("[data-edit-form]");
        if (existing) {
          existing.remove();
          return;
        }
        const wrap = element("div", {});
        wrap.setAttribute("data-edit-form", "1");
        renderEditForm(wrap, entry);
        row.appendChild(wrap);
      }));
      actions.appendChild(button("Models", () => {
        const existing = row.querySelector("[data-models]");
        if (existing) {
          existing.remove();
          return;
        }
        const box = element("div", {
          marginTop: "6px",
          padding: "6px 10px",
          background: COLORS.panel,
          borderRadius: "5px",
          font: "12px/1.6 ui-monospace, monospace",
          color: COLORS.muted
        });
        box.setAttribute("data-models", "1");
        box.textContent = "Fetching models…";
        row.appendChild(box);
        command("models.list", { credentialId: entry.id }).then((result) => {
          if (!isRecord(result) || !Array.isArray(result.models)) {
            box.textContent = "No model data.";
            return;
          }
          box.replaceChildren();
          const models = result.models;
          if (models.length === 0) {
            box.textContent = "No models available.";
            return;
          }
          const heading = element("div", {
            font: "600 12px/1.6 system-ui, sans-serif",
            color: COLORS.text,
            marginBottom: "4px"
          });
          heading.textContent = `${models.length} models`;
          box.appendChild(heading);
          for (const m of models) {
            const mRow = element("div", { paddingLeft: "8px" });
            const ctx = m.contextWindow ? ` (${Math.round(m.contextWindow / 1024)}k ctx)` : "";
            mRow.textContent = `${m.id}${ctx}`;
            box.appendChild(mRow);
          }
          const refreshBtn = button("Refresh models", () => run("Refresh models", () => command("models.refresh", { credentialId: entry.id })));
          style(refreshBtn, { marginTop: "6px" });
          box.appendChild(refreshBtn);
        }).catch((error) => {
          box.textContent = `Models failed: ${String(error)}`;
          box.style.color = COLORS.bad;
        });
      }));
      const priority = element("input", {
        width: "56px",
        marginRight: "6px",
        padding: "3px 6px",
        font: "12px system-ui, sans-serif",
        color: COLORS.text,
        background: COLORS.panel,
        border: `1px solid ${COLORS.border}`,
        borderRadius: "5px"
      });
      priority.type = "number";
      priority.value = String(entry.priority);
      priority.title = "Lower runs first";
      actions.appendChild(priority);
      actions.appendChild(button("Set priority", () => run("Set priority", () => command("credentials.setPriority", {
        credentialId: entry.id,
        priority: Number(priority.value)
      }))));
      actions.appendChild(button("Remove", () => {
        if (!window.confirm(`Remove credential ${name}?`))
          return;
        run("Remove", () => command("credentials.remove", { credentialId: entry.id }));
      }, "bad"));
      row.appendChild(actions);
      return row;
    }
    function renderToolbar(current) {
      toolbar.replaceChildren();
      for (const mode of ["priority", "balanced"]) {
        toolbar.appendChild(button(`${mode === current ? "✓ " : ""}${mode}`, () => run(`Switch to ${mode}`, () => command("config.setLoadBalancing", { mode }))));
      }
      toolbar.appendChild(button("Reload", () => {
        setStatus("Reloading…");
        setBusy(true);
        refresh().then(() => setStatus("Reloaded.", "good")).catch((error) => setStatus(`Reload failed: ${String(error)}`, "bad")).finally(() => setBusy(false));
      }));
      toolbar.appendChild(button("Refresh all models", () => run("Refresh all models", async () => {
        const output = await command("status");
        if (!output?.entries?.length)
          return;
        for (const entry of output.entries) {
          if (!entry.disabled) {
            await command("models.refresh", { credentialId: entry.id });
          }
        }
      })));
    }
    function parseKskPlaintext(text) {
      const results = [];
      for (const line of text.split(`
`)) {
        const trimmed = line.trim();
        if (trimmed.startsWith("ksk_")) {
          results.push({ authMethod: "api_key", apiKey: trimmed });
        }
      }
      return results;
    }
    function renderImport() {
      importArea.replaceChildren();
      importArea.appendChild(element("div", { font: "600 13px/1.8 system-ui, sans-serif", color: COLORS.text }, "Import credentials"));
      importArea.appendChild(element("div", { font: "12px/1.6 system-ui, sans-serif", color: COLORS.muted }, "Paste a Kiro credentials export (JSON) or ksk_ API keys (one per line). Values are sent to the plugin backend and stored by the host; they are never displayed back here."));
      const box = element("textarea", {
        width: "100%",
        minHeight: "84px",
        marginTop: "6px",
        padding: "6px",
        font: "12px ui-monospace, monospace",
        color: COLORS.text,
        background: COLORS.panel,
        border: `1px solid ${COLORS.border}`,
        borderRadius: "5px",
        boxSizing: "border-box"
      });
      box.placeholder = "JSON array, object, or ksk_ keys (one per line)";
      importArea.appendChild(box);
      const submit = button("Import", () => {
        const raw = box.value.trim();
        if (raw.length === 0) {
          setStatus("Nothing to import.", "bad");
          return;
        }
        let parsed;
        try {
          parsed = JSON.parse(raw);
        } catch {
          const kskEntries = parseKskPlaintext(raw);
          if (kskEntries.length > 0) {
            parsed = kskEntries;
          } else {
            setStatus("Import payload is not valid JSON and contains no ksk_ keys.", "bad");
            return;
          }
        }
        box.value = "";
        run("Import", () => command("credentials.import", { credentials: parsed }).then((result) => {
          if (isRecord(result)) {
            const added = result.added ?? 0;
            const dupes = result.duplicates ?? 0;
            setStatus(`Import done: ${added} added, ${dupes} duplicates.`, "good");
          }
        }));
      });
      style(submit, { marginTop: "6px" });
      importArea.appendChild(submit);
    }
    async function refresh() {
      const output = await command("status");
      if (!output || !Array.isArray(output.entries)) {
        summary.textContent = "No credential information available.";
        return;
      }
      summary.textContent = `${output.availableCount} of ${output.credentialCount} credentials available  ·  load balancing: ${output.loadBalancingMode}`;
      summary.style.color = output.availableCount > 0 ? COLORS.text : COLORS.warn;
      list.replaceChildren();
      if (output.entries.length === 0) {
        list.appendChild(element("div", { font: "13px/1.7 system-ui, sans-serif", color: COLORS.warn }, "No credentials configured yet. Import one below."));
      }
      const tiers = ["enterprise", "paid", "free", ""];
      const grouped = new Map;
      for (const entry of output.entries) {
        const tier = entry.tier || "";
        if (!grouped.has(tier))
          grouped.set(tier, []);
        grouped.get(tier)?.push(entry);
      }
      for (const tier of tiers) {
        const entries = grouped.get(tier);
        if (!entries?.length)
          continue;
        if (output.entries.length > 1 && tier) {
          const tierHeader = element("div", {
            font: "600 12px/2 system-ui, sans-serif",
            color: COLORS.muted,
            textTransform: "uppercase",
            letterSpacing: "0.5px"
          });
          tierHeader.textContent = tierLabel(tier);
          list.appendChild(tierHeader);
        }
        for (const entry of entries) {
          list.appendChild(renderEntry(entry, output.currentId));
        }
      }
      renderToolbar(output.loadBalancingMode);
      sdk.notify("kiro-external.settings.ready", {
        credentialCount: output.credentialCount,
        availableCount: output.availableCount
      });
    }
    renderImport();
    refresh().catch((error) => {
      summary.textContent = `Failed to load credentials: ${String(error)}`;
      summary.style.color = COLORS.bad;
    });
  })();
})();
