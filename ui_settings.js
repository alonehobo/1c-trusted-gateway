// ─── Settings Sub-Tabs ──────────────────────────────────────────────

function showSettingsSub(name, btn) {
  document.querySelectorAll(".settings-subpanel").forEach(p => p.classList.remove("active"));
  const panel = document.getElementById("settings-sub-" + name);
  if (panel) panel.classList.add("active");
  document.querySelectorAll(".settings-subtab").forEach(b => b.classList.remove("active"));
  if (btn) btn.classList.add("active");
  if (name === "fields") ftRenderTable();
  if (name === "types") tpRenderTable();
  if (name === "logs") logsRefresh();
  else logsStopAutoRefresh();
}

// ─── MCP Call Logs ───────────────────────────────────────────────
// Local UI-only view of recent MCP calls. Endpoints: /api/logs,
// /api/logs/entry?id=, /api/logs/clear. Agent has NO access.

let logsAutoTimer = null;
let logsLastEntries = [];
let logsDetailCurrent = null;
let logsDetailMode = "text";

function logsStopAutoRefresh() {
  if (logsAutoTimer) { clearInterval(logsAutoTimer); logsAutoTimer = null; }
  const cb = document.getElementById("logs-autorefresh");
  if (cb) cb.checked = false;
}

function logsToggleAutoRefresh() {
  const cb = document.getElementById("logs-autorefresh");
  if (cb && cb.checked) {
    if (logsAutoTimer) clearInterval(logsAutoTimer);
    logsAutoTimer = setInterval(logsRefresh, 3000);
  } else {
    logsStopAutoRefresh();
  }
}

async function logsRefresh() {
  try {
    const r = await fetch("/api/logs", { headers: { "X-Session-Token": TOKEN } });
    if (!r.ok) return;
    const j = await r.json();
    logsLastEntries = j.entries || [];
    const hint = document.getElementById("logs-count-hint");
    if (hint) hint.textContent = (j.count || 0) + " записей";
    logsRenderTable();
  } catch (e) {}
}

function logsRenderTable() {
  const body = document.getElementById("logs-body");
  const empty = document.getElementById("logs-empty");
  if (!body) return;
  body.innerHTML = "";
  if (!logsLastEntries.length) {
    if (empty) empty.style.display = "block";
    return;
  }
  if (empty) empty.style.display = "none";

  for (const e of logsLastEntries) {
    const tr = document.createElement("tr");
    tr.style.cursor = "pointer";
    tr.onclick = () => logsShowDetail(e.id);

    const ts = new Date(e.timestamp);
    const tsStr = ts.toLocaleTimeString("ru-RU", { hour12: false }) +
      "." + String(ts.getMilliseconds()).padStart(3, "0");

    const tdTime = document.createElement("td");
    tdTime.textContent = tsStr;
    tdTime.style.fontFamily = "monospace";
    tdTime.style.fontSize = "11px";

    const tdTool = document.createElement("td");
    tdTool.textContent = e.tool;
    tdTool.style.fontFamily = "monospace";
    tdTool.style.fontSize = "11px";
    if (e.is_error) tdTool.style.color = "var(--warning)";

    const tdDur = document.createElement("td");
    tdDur.textContent = e.duration_ms;
    tdDur.style.textAlign = "right";
    tdDur.style.fontFamily = "monospace";
    tdDur.style.fontSize = "11px";

    const tdLen = document.createElement("td");
    tdLen.textContent = logsFormatSize(e.text_len);
    tdLen.style.textAlign = "right";
    tdLen.style.fontFamily = "monospace";
    tdLen.style.fontSize = "11px";

    const tdSchema = document.createElement("td");
    tdSchema.style.textAlign = "center";
    tdSchema.style.fontSize = "11px";
    if (e.is_error) {
      tdSchema.textContent = "✖";
      tdSchema.style.color = "var(--warning)";
      tdSchema.title = e.error_message || "Ошибка";
    } else if (e.has_schema) {
      tdSchema.textContent = "✓ JSON";
      tdSchema.style.color = "var(--success,#4caf50)";
      tdSchema.title = "Ответ со схемой (columns/rows)";
    } else {
      tdSchema.textContent = "TSV";
      tdSchema.style.color = "var(--text-secondary)";
      tdSchema.title = "Без схемы — старый формат";
    }

    const tdPrev = document.createElement("td");
    tdPrev.textContent = e.text_preview || "";
    tdPrev.style.fontFamily = "monospace";
    tdPrev.style.fontSize = "11px";
    tdPrev.style.whiteSpace = "nowrap";
    tdPrev.style.overflow = "hidden";
    tdPrev.style.textOverflow = "ellipsis";
    tdPrev.style.maxWidth = "600px";
    if (e.is_error) tdPrev.style.color = "var(--warning)";

    tr.appendChild(tdTime);
    tr.appendChild(tdTool);
    tr.appendChild(tdDur);
    tr.appendChild(tdLen);
    tr.appendChild(tdSchema);
    tr.appendChild(tdPrev);
    body.appendChild(tr);
  }
}

function logsFormatSize(n) {
  if (n == null) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

async function logsClear() {
  if (!confirm("Очистить журнал MCP-вызовов?")) return;
  try {
    await fetch("/api/logs/clear", {
      method: "POST",
      headers: { "X-Session-Token": TOKEN },
    });
    logsRefresh();
  } catch (e) {}
}

async function logsShowDetail(id) {
  try {
    const r = await fetch("/api/logs/entry?id=" + id, { headers: { "X-Session-Token": TOKEN } });
    if (!r.ok) return;
    const j = await r.json();
    if (!j.ok || !j.entry) return;
    logsDetailCurrent = j.entry;
    logsDetailMode = "text";
    const meta = document.getElementById("log-detail-meta");
    const e = j.entry;
    const parts = [
      "Время:      " + new Date(e.timestamp).toLocaleString("ru-RU"),
      "Инструмент: " + e.tool,
      "URL:        " + (e.url || ""),
      "Длительность: " + e.duration_ms + " мс",
      "Размер:     " + logsFormatSize(e.text_len) + (e.text_truncated ? " (сохранено до 128 KB)" : ""),
      "Схема:      " + (e.has_schema ? "да (JSON со columns/rows)" : "нет"),
      "Ошибка:     " + (e.is_error ? (e.error_message || "да") : "нет"),
    ];
    if (meta) meta.textContent = parts.join("\n");
    document.getElementById("log-detail-overlay").style.display = "flex";
    logsDetailTab("text", document.getElementById("log-tab-text"));
  } catch (e) {}
}

function logsDetailTab(mode, btn) {
  logsDetailMode = mode;
  const body = document.getElementById("log-detail-body");
  const e = logsDetailCurrent;
  if (!e || !body) return;
  document.querySelectorAll("#log-detail-overlay button.btn-ghost").forEach(b => b.style.background = "");
  if (btn) btn.style.background = "var(--bg-elevated)";
  if (mode === "text") {
    body.textContent = e.text || "";
    // Если JSON — попробуем pretty-print для читабельности
    const t = (e.text || "").trim();
    if (t.startsWith("{") || t.startsWith("[")) {
      try { body.textContent = JSON.stringify(JSON.parse(t), null, 2); } catch {}
    }
  } else if (mode === "args") {
    body.textContent = JSON.stringify(e.args || {}, null, 2);
  } else if (mode === "structured") {
    if (e.has_structured) {
      body.textContent = typeof e.structured === "string"
        ? e.structured
        : JSON.stringify(e.structured, null, 2);
    } else {
      body.textContent = "(structuredContent отсутствует в ответе)";
    }
  }
}

function logsDetailClose() {
  document.getElementById("log-detail-overlay").style.display = "none";
  logsDetailCurrent = null;
}

async function logsDetailCopy() {
  const body = document.getElementById("log-detail-body");
  if (!body) return;
  try {
    await navigator.clipboard.writeText(body.textContent || "");
  } catch {}
}

// ─── Type Policy Table ──────────────────────────────────────────────
// tpRules is a working copy of { plain: [...], force: [...] } where each
// entry includes {name, kind: "prefix"|"exact"}. When saved it's converted
// to PersistedTypePolicy JSON and sent to /api/set_type_policy.
let tpRules = { plain: [], force: [] };

function tpFromEffective(eff) {
  const rules = { plain: [], force: [] };
  (eff?.plain_prefixes || []).forEach(p => rules.plain.push({ name: p, kind: "prefix" }));
  (eff?.plain_types || []).forEach(p => rules.plain.push({ name: p, kind: "exact" }));
  (eff?.forced_mask_prefixes || []).forEach(p => rules.force.push({ name: p, kind: "prefix" }));
  (eff?.forced_mask_types || []).forEach(p => rules.force.push({ name: p, kind: "exact" }));
  return rules;
}

function tpInitFromState(s) {
  const eff = s?.type_policy_effective || {};
  tpRules = tpFromEffective(eff);
}

function tpRenderTable() {
  const tbody = document.getElementById("tp-body");
  const empty = document.getElementById("tp-empty");
  if (!tbody) return;
  tbody.innerHTML = "";

  const all = [
    ...tpRules.plain.map(r => ({ ...r, policy: "plain" })),
    ...tpRules.force.map(r => ({ ...r, policy: "mask" })),
  ];
  all.sort((a, b) => a.name.localeCompare(b.name));

  if (all.length === 0) {
    empty.style.display = "block";
    return;
  }
  empty.style.display = "none";

  all.forEach((r, idx) => {
    const tr = document.createElement("tr");
    const tdName = document.createElement("td");
    tdName.textContent = r.name + (r.kind === "prefix" ? "  (префикс)" : "");
    const tdPolicy = document.createElement("td");
    tdPolicy.style.textAlign = "right";
    tdPolicy.innerHTML = r.policy === "plain"
      ? '<span style="color:var(--success,#4ea55a)">открыто</span>'
      : '<span style="color:var(--warning,#c88)">маскировать</span>';
    const tdAct = document.createElement("td");
    const btn = document.createElement("button");
    btn.className = "btn btn-ghost btn-xs";
    btn.textContent = "✕";
    btn.title = "Удалить правило";
    btn.onclick = () => { tpRemove(r.name, r.policy); };
    tdAct.appendChild(btn);
    tr.appendChild(tdName);
    tr.appendChild(tdPolicy);
    tr.appendChild(tdAct);
    tbody.appendChild(tr);
    idx; // silence unused
  });
}

function tpAddPlain() { tpAddRule("plain"); }
function tpAddForced() { tpAddRule("mask"); }

function tpAddRule(policy) {
  const el = document.getElementById("tp-new-type");
  const raw = el.value.trim();
  if (!raw) return;
  const kind = raw.endsWith(".") ? "prefix" : "exact";
  const bucket = policy === "plain" ? tpRules.plain : tpRules.force;
  // Remove duplicate if already present with the other policy.
  const otherBucket = policy === "plain" ? tpRules.force : tpRules.plain;
  for (let i = otherBucket.length - 1; i >= 0; i--) {
    if (otherBucket[i].name === raw) otherBucket.splice(i, 1);
  }
  if (!bucket.some(r => r.name === raw)) {
    bucket.push({ name: raw, kind });
  }
  el.value = "";
  tpRenderTable();
  tpSave(true); // auto-save — consistent with tpResetDefaults
}

function tpRemove(name, policy) {
  const bucket = policy === "plain" ? tpRules.plain : tpRules.force;
  const idx = bucket.findIndex(r => r.name === name);
  if (idx >= 0) bucket.splice(idx, 1);
  tpRenderTable();
  tpSave(true); // auto-save
}

function tpResetDefaults() {
  if (!confirm("Сбросить политику к дефолтам? Ваши правки будут удалены после 'Применить'.")) return;
  tpRules = { plain: [], force: [] };
  // Empty PersistedTypePolicy → server will rebuild defaults.
  tpSave(true);
}

async function tpSave(silent) {
  const payload = {
    plain_types: tpRules.plain.filter(r => r.kind === "exact").map(r => r.name),
    plain_prefixes: tpRules.plain.filter(r => r.kind === "prefix").map(r => r.name),
    forced_mask_types: tpRules.force.filter(r => r.kind === "exact").map(r => r.name),
    forced_mask_prefixes: tpRules.force.filter(r => r.kind === "prefix").map(r => r.name),
  };
  try {
    await api("/api/set_type_policy", { type_policy: payload });
    if (!silent) toast("Политика типов применена.");
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

// ─── Fields Table ───────────────────────────────────────────────────

let ftSortCol = "name";
let ftSortAsc = true;

function ftGetFields() {
  const allowStr = document.getElementById("s-allow-plain").value.trim();
  const forceStr = document.getElementById("s-force-mask").value.trim();
  const fields = [];
  if (allowStr) {
    allowStr.split(",").map(s => s.trim()).filter(Boolean).forEach(name => {
      fields.push({ name, type: "allow" });
    });
  }
  if (forceStr) {
    forceStr.split(",").map(s => s.trim()).filter(Boolean).forEach(name => {
      fields.push({ name, type: "force" });
    });
  }
  return fields;
}

function ftSetFields(fields) {
  const allow = fields.filter(f => f.type === "allow").map(f => f.name);
  const force = fields.filter(f => f.type === "force").map(f => f.name);
  document.getElementById("s-allow-plain").value = allow.join(", ");
  document.getElementById("s-force-mask").value = force.join(", ");
}

function ftRenderTable() {
  let fields = ftGetFields();
  const filter = (document.getElementById("ft-filter").value || "").trim().toLowerCase();
  if (filter) {
    fields = fields.filter(f => f.name.toLowerCase().includes(filter));
  }

  // Sort
  fields.sort((a, b) => {
    let cmp = 0;
    if (ftSortCol === "name") cmp = a.name.localeCompare(b.name, "ru");
    else cmp = a.type.localeCompare(b.type);
    return ftSortAsc ? cmp : -cmp;
  });

  // Sort arrows
  document.getElementById("ft-sort-name").textContent = ftSortCol === "name" ? (ftSortAsc ? "\u25B2" : "\u25BC") : "";
  document.getElementById("ft-sort-type").textContent = ftSortCol === "type" ? (ftSortAsc ? "\u25B2" : "\u25BC") : "";

  const tbody = document.getElementById("ft-body");
  const emptyEl = document.getElementById("ft-empty");

  if (!fields.length) {
    tbody.innerHTML = "";
    emptyEl.style.display = "";
    return;
  }
  emptyEl.style.display = "none";

  let html = "";
  fields.forEach(f => {
    const typeCls = f.type === "allow" ? "ft-type-allow" : "ft-type-force";
    const typeLabel = f.type === "allow" ? "Разрешено" : "Шифровать";
    const toggleLabel = f.type === "allow" ? "Шифровать" : "Разрешить";
    html += '<tr>' +
      '<td>' + escapeHtml(f.name) + '</td>' +
      '<td class="ft-actions">' +
        '<span class="ft-type ' + typeCls + '" style="cursor:pointer" onclick="ftToggle(\'' + escapeHtml(f.name).replace(/'/g, "\\'") + '\')" title="Нажмите для переключения">' + typeLabel + '</span>' +
        ' <button class="ft-del" onclick="ftRemove(\'' + escapeHtml(f.name).replace(/'/g, "\\'") + '\')" title="Удалить">\u00D7</button>' +
      '</td>' +
      '</tr>';
  });
  tbody.innerHTML = html;
}

function ftSort(col) {
  if (ftSortCol === col) ftSortAsc = !ftSortAsc;
  else { ftSortCol = col; ftSortAsc = true; }
  ftRenderTable();
}

function ftAddField() {
  const input = document.getElementById("ft-new-field");
  const name = input.value.trim();
  if (!name) return;
  const fields = ftGetFields();
  // Remove if exists, re-add as allow
  const filtered = fields.filter(f => f.name.toLowerCase() !== name.toLowerCase());
  filtered.push({ name, type: "allow" });
  ftSetFields(filtered);
  ftRenderTable();
  input.value = "";
  toast("\u2705 " + name + " добавлено в белый список");
}

function ftAddFieldForce() {
  const input = document.getElementById("ft-new-field");
  const name = input.value.trim();
  if (!name) return;
  const fields = ftGetFields();
  const filtered = fields.filter(f => f.name.toLowerCase() !== name.toLowerCase());
  filtered.push({ name, type: "force" });
  ftSetFields(filtered);
  ftRenderTable();
  input.value = "";
  toast("\uD83D\uDD12 " + name + " добавлено в принудительное шифрование");
}

function ftRemove(name) {
  const fields = ftGetFields().filter(f => f.name !== name);
  ftSetFields(fields);
  ftRenderTable();
  toast(name + " удалено");
}

function ftToggle(name) {
  const fields = ftGetFields();
  const idx = fields.findIndex(f => f.name === name);
  if (idx === -1) return;
  fields[idx].type = fields[idx].type === "allow" ? "force" : "allow";
  ftSetFields(fields);
  ftRenderTable();
  const label = fields[idx].type === "allow" ? "разрешено" : "шифруется";
  toast(name + " \u2192 " + label);
}

// ─── Toast ───────────────────────────────────────────────────────────

let toastTimer;
function toast(msg) {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 2500);
}

// ─── Draggable Splitter ────────────────────────────────────────────────

(function initSplitter() {
  const splitter = document.getElementById("pane-splitter");
  const editorPane = document.getElementById("editor-pane");
  const container = document.getElementById("mcp-split");
  if (!splitter || !editorPane || !container) return;

  // Restore saved ratio
  const saved = localStorage.getItem("splitter_ratio");
  if (saved) {
    const ratio = parseFloat(saved);
    if (ratio > 0.1 && ratio < 0.9) {
      editorPane.style.flex = "0 0 " + (ratio * 100) + "%";
      editorPane.style.maxWidth = "60%";
    }
  }

  let startX, startWidth, containerWidth;

  splitter.addEventListener("mousedown", function(e) {
    e.preventDefault();
    startX = e.clientX;
    startWidth = editorPane.getBoundingClientRect().width;
    containerWidth = container.getBoundingClientRect().width;
    splitter.classList.add("dragging");
    document.body.classList.add("splitter-dragging");
    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  });

  function onMouseMove(e) {
    const dx = e.clientX - startX;
    let newWidth = startWidth + dx;
    const minW = 200;
    const maxW = containerWidth - 200 - 6; // 6 = splitter width
    newWidth = Math.max(minW, Math.min(maxW, newWidth));
    const ratio = newWidth / containerWidth;
    editorPane.style.flex = "0 0 " + newWidth + "px";
    editorPane.style.maxWidth = newWidth + "px";
    // Resize Ace editor
    if (window.queryEditor && window.queryEditor.ace) window.queryEditor.ace.resize();
  }

  function onMouseUp(e) {
    splitter.classList.remove("dragging");
    document.body.classList.remove("splitter-dragging");
    document.removeEventListener("mousemove", onMouseMove);
    document.removeEventListener("mouseup", onMouseUp);
    // Save ratio
    const ratio = editorPane.getBoundingClientRect().width / container.getBoundingClientRect().width;
    localStorage.setItem("splitter_ratio", ratio.toFixed(3));
    if (window.queryEditor && window.queryEditor.ace) window.queryEditor.ace.resize();
  }
})();

// ─── Settings ────────────────────────────────────────────────────────

async function loadSettingsForm() {
  try {
    const s = await api("/api/settings");
    console.log("[Settings] /api/settings response:", JSON.stringify(s));
    document.getElementById("s-mcp-url").value = s.mcp_url || "";
    document.getElementById("s-mcp-token").value = "";
    const hint = document.getElementById("s-token-hint");
    if (s.mcp_token_saved) { hint.style.display = ""; hint.textContent = "\u0422\u0435\u043a\u0443\u0449\u0438\u0439 \u043a\u043b\u044e\u0447 \u0441\u043e\u0445\u0440\u0430\u043d\u0451\u043d. \u041e\u0441\u0442\u0430\u0432\u044c\u0442\u0435 \u043f\u043e\u043b\u0435 \u043f\u0443\u0441\u0442\u044b\u043c, \u0447\u0442\u043e\u0431\u044b \u043d\u0435 \u043c\u0435\u043d\u044f\u0442\u044c."; }
    else { hint.style.display = "none"; }
    document.getElementById("s-preview-chars").value = s.defaults_preview_chars || 4000;
    document.getElementById("s-auto-send").checked = !!s.defaults_auto_send;
    document.getElementById("s-sound-notify").checked = localStorage.getItem("sound_notifications") === "true";
    document.getElementById("s-allow-plain").value = s.defaults_allow_plain_fields || "";
    document.getElementById("s-force-mask").value = s.defaults_force_mask_fields || "";
    document.getElementById("s-keywords-hint").textContent = s.allow_plain_keywords || "";
    // Refresh fields table if visible
    if (document.getElementById("settings-sub-fields").classList.contains("active")) ftRenderTable();
  } catch (e) { toast("\u041e\u0448\u0438\u0431\u043a\u0430 \u0437\u0430\u0433\u0440\u0443\u0437\u043a\u0438 \u043d\u0430\u0441\u0442\u0440\u043e\u0435\u043a: " + e.message); }
}

async function saveSettings() {
  const payload = {
    mcp_url: document.getElementById("s-mcp-url").value.trim(),
    mcp_token: document.getElementById("s-mcp-token").value,
    defaults_preview_chars: parseInt(document.getElementById("s-preview-chars").value) || 4000,
    defaults_auto_send: document.getElementById("s-auto-send").checked,
    defaults_allow_plain_fields: document.getElementById("s-allow-plain").value.trim(),
    defaults_force_mask_fields: document.getElementById("s-force-mask").value.trim(),
  };
  try {
    // Save client-only settings to localStorage
    localStorage.setItem("sound_notifications", document.getElementById("s-sound-notify").checked ? "true" : "false");
    const r = await api("/api/settings", payload);
    if (r.ok) {
      toast("\u041d\u0430\u0441\u0442\u0440\u043e\u0439\u043a\u0438 \u0441\u043e\u0445\u0440\u0430\u043d\u0435\u043d\u044b \u0438 \u0437\u0430\u0448\u0438\u0444\u0440\u043e\u0432\u0430\u043d\u044b");
      const urlEl = document.getElementById("url");
      if (payload.mcp_url) urlEl.value = payload.mcp_url;
      headerTokenTouched = false;
      await fetchState();
    } else toast("\u041e\u0448\u0438\u0431\u043a\u0430: " + (r.error || "?"));
  } catch (e) { toast("\u041e\u0448\u0438\u0431\u043a\u0430: " + e.message); }
}

async function resetSettings() {
  if (!confirm("\u0423\u0434\u0430\u043b\u0438\u0442\u044c \u0432\u0441\u0435 \u0441\u043e\u0445\u0440\u0430\u043d\u0451\u043d\u043d\u044b\u0435 \u043d\u0430\u0441\u0442\u0440\u043e\u0439\u043a\u0438? \u041f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435 \u0432\u0435\u0440\u043d\u0451\u0442\u0441\u044f \u043a \u0437\u043d\u0430\u0447\u0435\u043d\u0438\u044f\u043c \u043f\u043e \u0443\u043c\u043e\u043b\u0447\u0430\u043d\u0438\u044e.")) return;
  try {
    const r = await api("/api/settings/reset", {});
    if (r.ok) { toast("\u041d\u0430\u0441\u0442\u0440\u043e\u0439\u043a\u0438 \u0441\u0431\u0440\u043e\u0448\u0435\u043d\u044b"); loadSettingsForm(); }
    else toast("\u041e\u0448\u0438\u0431\u043a\u0430: " + (r.error || "?"));
  } catch (e) { toast("\u041e\u0448\u0438\u0431\u043a\u0430: " + e.message); }
}

async function exportSettings() {
  try {
    const r = await fetch("/api/settings/export?token=" + encodeURIComponent(sessionToken));
    if (!r.ok) { toast("Ошибка экспорта"); return; }
    const data = await r.json();
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.json";
    a.click();
    URL.revokeObjectURL(url);
    toast("Настройки экспортированы (без ключа)");
  } catch (e) { toast("Ошибка: " + e.message); }
}

function importSettings(input) {
  const file = input.files[0];
  if (!file) return;
  const reader = new FileReader();
  reader.onload = async (e) => {
    try {
      const data = JSON.parse(e.target.result);
      const r = await api("/api/settings/import", data);
      if (r.ok) { toast("\u041d\u0430\u0441\u0442\u0440\u043e\u0439\u043a\u0438 \u0438\u043c\u043f\u043e\u0440\u0442\u0438\u0440\u043e\u0432\u0430\u043d\u044b"); loadSettingsForm(); }
      else toast("\u041e\u0448\u0438\u0431\u043a\u0430: " + (r.error || "?"));
    } catch (err) { toast("\u041e\u0448\u0438\u0431\u043a\u0430 \u0447\u0442\u0435\u043d\u0438\u044f \u0444\u0430\u0439\u043b\u0430: " + err.message); }
  };
  reader.readAsText(file);
  input.value = "";
}

function toggleTokenVis() {
  const inp = document.getElementById("s-mcp-token");
  inp.type = inp.type === "password" ? "text" : "password";
}

// ─── Favicon blink & browser notifications for agent waiting ─────────

let faviconInterval = null;
let faviconNormal = null;
let faviconAlert = null;
let agentWasWaiting = false;

function createFaviconDataURL(color) {
  const c = document.createElement("canvas");
  c.width = 32; c.height = 32;
  const ctx = c.getContext("2d");
  // Shield shape
  ctx.beginPath();
  ctx.moveTo(16, 2);
  ctx.quadraticCurveTo(2, 6, 2, 14);
  ctx.quadraticCurveTo(2, 26, 16, 30);
  ctx.quadraticCurveTo(30, 26, 30, 14);
  ctx.quadraticCurveTo(30, 6, 16, 2);
  ctx.closePath();
  ctx.fillStyle = color;
  ctx.fill();
  ctx.strokeStyle = "#fff";
  ctx.lineWidth = 1.5;
  ctx.stroke();
  return c.toDataURL();
}

function ensureFavicons() {
  if (!faviconNormal) faviconNormal = createFaviconDataURL("#4a90d9");
  if (!faviconAlert) faviconAlert = createFaviconDataURL("#e74c3c");
}

function getFaviconLink() {
  let link = document.querySelector("link[rel~='icon']");
  if (!link) {
    link = document.createElement("link");
    link.rel = "icon";
    document.head.appendChild(link);
  }
  return link;
}

function startFaviconBlink() {
  if (faviconInterval) return;
  ensureFavicons();
  const link = getFaviconLink();
  link.href = faviconNormal;
  let isAlert = false;
  faviconInterval = setInterval(() => {
    isAlert = !isAlert;
    link.href = isAlert ? faviconAlert : faviconNormal;
    document.title = isAlert ? "⚠ Агент ждёт одобрения" : "Trusted Gateway";
  }, 800);
}

function stopFaviconBlink() {
  if (faviconInterval) {
    clearInterval(faviconInterval);
    faviconInterval = null;
  }
  ensureFavicons();
  getFaviconLink().href = faviconNormal;
  document.title = "Trusted Gateway";
}

function showAgentNotification(message) {
  if (!("Notification" in window)) return;
  const doShow = () => {
    // Play attention sound
    try { new Audio("data:audio/wav;base64,UklGRl4AAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YToAAAD/f/9//3//f/9//3//f/9/AIA=").play().catch(()=>{}); } catch(e) {}
    const n = new Notification("⚡ Trusted Gateway", {
      body: message,
      requireInteraction: true,
      tag: "agent-waiting",
      icon: "/api/icon"
    });
    n.onclick = () => { window.focus(); n.close(); };
  };
  if (Notification.permission === "granted") {
    doShow();
  } else if (Notification.permission !== "denied") {
    Notification.requestPermission().then(p => { if (p === "granted") doShow(); });
  }
}

function handleAgentWaiting(isWaiting) {
  if (isWaiting && !agentWasWaiting) {
    startFaviconBlink();
    showAgentNotification("Агент ждёт одобрения полей в белый список");
  } else if (!isWaiting && agentWasWaiting) {
    stopFaviconBlink();
  }
  agentWasWaiting = isWaiting;
}

// Request notification permission on first user interaction
document.addEventListener("click", function reqNotifPerm() {
  if ("Notification" in window && Notification.permission === "default") {
    Notification.requestPermission();
  }
  document.removeEventListener("click", reqNotifPerm);
}, { once: true });

// ─── Query History ──────────────────────────────────────────────────

const HISTORY_KEY = "query_history";
const HISTORY_MAX = 50;

function getHistory() {
  try {
    return JSON.parse(localStorage.getItem(HISTORY_KEY)) || [];
  } catch(e) { return []; }
}

function saveToHistory(query, rowCount, kind) {
  kind = kind || "query";
  const history = getHistory();
  // Avoid duplicate of last entry of same kind
  if (history.length && history[0].query === query && (history[0].kind || "query") === kind) {
    history[0].ts = Date.now();
    history[0].rows = rowCount;
  } else {
    history.unshift({ query, ts: Date.now(), rows: rowCount || 0, kind });
  }
  if (history.length > HISTORY_MAX) history.length = HISTORY_MAX;
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history)); } catch(e) {}
}

function currentHistoryKind() {
  return appState.code_mode ? "code" : "query";
}

function toggleHistory() {
  const dd = document.getElementById("history-dropdown");
  if (dd.style.display !== "none") { dd.style.display = "none"; return; }
  renderHistory();
  dd.style.display = "";
  // Close on outside click
  setTimeout(() => {
    document.addEventListener("click", closeHistoryOnOutside, { once: true });
  }, 0);
}

function closeHistoryOnOutside(e) {
  const dd = document.getElementById("history-dropdown");
  if (dd && !dd.parentElement.contains(e.target)) {
    dd.style.display = "none";
  } else if (dd && dd.style.display !== "none") {
    setTimeout(() => document.addEventListener("click", closeHistoryOnOutside, { once: true }), 0);
  }
}

function renderHistory() {
  const dd = document.getElementById("history-dropdown");
  const history = getHistory();
  if (!history.length) {
    dd.innerHTML = '<div class="history-empty">История пуста</div>';
    return;
  }
  let html = "";
  history.forEach((item, i) => {
    const d = new Date(item.ts);
    const dateStr = d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" }) + " " +
                    d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
    const rows = item.rows ? item.rows + " стр." : "";
    const tooltip = escapeHtml(item.query).replace(/\n/g, "&#10;");
    const label = item.name
      ? escapeHtml(item.name)
      : escapeHtml(item.query.length > 60 ? item.query.substring(0, 60) + "..." : item.query);
    const labelCls = item.name ? "history-name" : "history-query";
    html += '<div class="history-item" title="' + tooltip + '" onclick="historySelect(' + i + ')">' +
            '<span class="' + labelCls + '">' + label + '</span>' +
            '<span class="history-meta">' + rows + (rows ? ' · ' : '') + dateStr +
            ' <span class="history-del history-name-edit" onclick="event.stopPropagation();historyRename(' + i + ')" title="Переименовать">&#9998;</span>' +
            '</span>' +
            '</div>';
  });
  html += '<div class="history-footer"><button class="btn btn-ghost btn-xs" onclick="clearHistory()">Очистить историю</button></div>';
  dd.innerHTML = html;
}

function historySelect(idx) {
  const history = getHistory();
  if (!history[idx]) return;
  if (window.queryEditor && window.queryEditor.ace) {
    window.queryEditor.ace.setValue(history[idx].query, -1);
    window.queryEditor.ace.clearSelection();
  }
  document.getElementById("history-dropdown").style.display = "none";
}

function historyRename(idx) {
  const history = getHistory();
  if (!history[idx]) return;
  const kind = history[idx].kind || "query";
  const current = history[idx].name || "";
  const name = prompt("Имя " + (kind === "code" ? "кода" : "запроса") + ":", current);
  if (name === null) return;
  history[idx].name = name.trim() || undefined;
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history)); } catch(e) {}
  renderHistory();
}

function historyDelete(idx) {
  const history = getHistory();
  if (!history[idx]) return;
  history.splice(idx, 1);
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history)); } catch(e) {}
  renderHistory();
}

function clearHistory() {
  const kind = currentHistoryKind();
  const history = getHistory().filter(h => (h.kind || "query") !== kind);
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history)); } catch(e) {}
  document.getElementById("history-dropdown").style.display = "none";
  toast("История " + (kind === "code" ? "кода" : "запросов") + " очищена");
}

function escapeHtml(s) {
  return s.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");
}

// ─── Saved Queries (Bookmarks) ──────────────────────────────────────

const SAVED_KEY = "saved_queries";

function getSavedQueries() {
  try { return JSON.parse(localStorage.getItem(SAVED_KEY)) || []; } catch(e) { return []; }
}

function saveCurrentQuery() {
  if (!window.queryEditor) return;
  const q = window.queryEditor.ace ? window.queryEditor.ace.getValue() : window.queryEditor.state.doc.toString();
  if (!q.trim()) { toast("Нечего сохранять"); return; }
  const kind = currentHistoryKind();
  const label = kind === "code" ? "кода" : "запроса";
  const name = prompt("Название " + label + ":", q.substring(0, 40).replace(/\n/g, " "));
  if (!name) return;
  const saved = getSavedQueries();
  saved.unshift({ name, query: q, ts: Date.now(), kind });
  try { localStorage.setItem(SAVED_KEY, JSON.stringify(saved)); } catch(e) {}
  toast((kind === "code" ? "Код" : "Запрос") + " сохранён: " + name);
}

function deleteSavedQuery(idx, e) {
  e.stopPropagation();
  const saved = getSavedQueries();
  saved.splice(idx, 1);
  try { localStorage.setItem(SAVED_KEY, JSON.stringify(saved)); } catch(e2) {}
  renderHistory();
}

function savedSelect(idx) {
  const saved = getSavedQueries();
  if (!saved[idx]) return;
  if (window.queryEditor && window.queryEditor.ace) {
    window.queryEditor.ace.setValue(saved[idx].query, -1);
    window.queryEditor.ace.clearSelection();
  }
  document.getElementById("history-dropdown").style.display = "none";
}

// Override renderHistory to include saved queries section and filter by kind
const _origRenderHistory = renderHistory;
renderHistory = function() {
  const dd = document.getElementById("history-dropdown");
  const kind = currentHistoryKind();
  const kindLabel = kind === "code" ? "кода" : "запросов";
  const allSaved = getSavedQueries();
  const allHistory = getHistory();
  const saved = allSaved.filter(s => (s.kind || "query") === kind);
  const history = allHistory.filter(h => (h.kind || "query") === kind);

  if (!saved.length && !history.length) {
    dd.innerHTML = '<div class="history-empty">История ' + kindLabel + ' пуста</div>';
    return;
  }

  let html = "";

  // Saved section
  if (saved.length) {
    html += '<div class="history-section-title">&#11088; Сохранённые</div>';
    saved.forEach((item) => {
      const realIdx = allSaved.indexOf(item);
      const tooltip = escapeHtml(item.query).replace(/\n/g, "&#10;");
      html += '<div class="history-item" title="' + tooltip + '" onclick="savedSelect(' + realIdx + ')">' +
              '<span class="history-name">' + escapeHtml(item.name) + '</span>' +
              '<span class="history-meta">' +
              '<span class="history-del" onclick="deleteSavedQuery(' + realIdx + ', event)" title="Удалить">&#10005;</span></span>' +
              '</div>';
    });
  }

  // Recent history section
  if (history.length) {
    if (saved.length) html += '<div class="history-section-title">&#128337; Недавние</div>';
    history.forEach((item) => {
      const realIdx = allHistory.indexOf(item);
      const d = new Date(item.ts);
      const dateStr = d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" }) + " " +
                      d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
      const rows = item.rows ? item.rows + " стр." : "";
      const tooltip = escapeHtml(item.query).replace(/\n/g, "&#10;");
      const label = item.name
        ? escapeHtml(item.name)
        : escapeHtml(item.query.length > 60 ? item.query.substring(0, 60) + "..." : item.query);
      const labelCls = item.name ? "history-name" : "history-query";
      html += '<div class="history-item" title="' + tooltip + '" onclick="historySelect(' + realIdx + ')">' +
              '<span class="' + labelCls + '">' + label + '</span>' +
              '<span class="history-meta">' + rows + (rows ? ' · ' : '') + dateStr +
              ' <span class="history-del history-name-edit" onclick="event.stopPropagation();historyRename(' + realIdx + ')" title="Переименовать">&#9998;</span>' +
              '<span class="history-del" onclick="event.stopPropagation();historyDelete(' + realIdx + ')" title="Удалить">&#10005;</span>' +
              '</span>' +
              '</div>';
    });
  }

  html += '<div class="history-footer"><button class="btn btn-ghost btn-xs" onclick="clearHistory()">Очистить историю ' + kindLabel + '</button></div>';
  dd.innerHTML = html;
};

// ─── Theme Toggle ───────────────────────────────────────────────────

function toggleTheme() {
  const isDark = document.body.classList.contains("theme-dark");
  setTheme(isDark ? "light" : "dark");
}

// ─── Table Font Scaling ─────────────────────────────────────────────

let tableFontSize = parseInt(localStorage.getItem("table_font_size")) || 12;

function applyTableFontSize() {
  const tbl = document.getElementById("result-table");
  if (tbl) tbl.style.fontSize = tableFontSize + "px";
}

function scaleTable(delta) {
  tableFontSize = Math.max(9, Math.min(18, tableFontSize + delta));
  try { localStorage.setItem("table_font_size", tableFontSize); } catch(e) {}
  applyTableFontSize();
}

// Apply on load
document.addEventListener("DOMContentLoaded", applyTableFontSize);

// ─── Sound & Desktop Notifications ──────────────────────────────────

function isSoundEnabled() {
  return localStorage.getItem("sound_notifications") === "true";
}

function playBeep() {
  if (!isSoundEnabled()) return;
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.connect(gain); gain.connect(ctx.destination);
    osc.type = "sine"; osc.frequency.value = 660;
    gain.gain.value = 0.15;
    osc.start(); osc.stop(ctx.currentTime + 0.15);
    setTimeout(() => ctx.close(), 200);
  } catch(e) {}
}

function desktopNotify(title, body) {
  if (!isSoundEnabled()) return;
  if ("Notification" in window && Notification.permission === "granted") {
    try { new Notification(title, { body, icon: "" }); } catch(e) {}
  }
}

