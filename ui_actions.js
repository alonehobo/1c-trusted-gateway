// ─── Actions ─────────────────────────────────────────────────────────

function mergedAllowPlain() {
  const fromTextarea = document.getElementById("allow-plain").value.split(",").map(s => s.trim()).filter(Boolean);
  const merged = new Set(fromTextarea.map(s => s.toLowerCase()));
  tagAllowSet.forEach(f => merged.add(f.toLowerCase()));
  tagForceSet.forEach(f => merged.delete(f.toLowerCase()));
  // Return original-case names
  const result = [];
  const seen = new Set();
  [...fromTextarea, ...tagAllowSet].forEach(f => {
    const lc = f.toLowerCase();
    if (merged.has(lc) && !seen.has(lc)) { result.push(f); seen.add(lc); }
  });
  return result.join(", ");
}

function mergedForceMask() {
  const fromTextarea = document.getElementById("force-mask").value.split(",").map(s => s.trim()).filter(Boolean);
  const merged = new Set(fromTextarea.map(s => s.toLowerCase()));
  tagForceSet.forEach(f => merged.add(f.toLowerCase()));
  tagAllowSet.forEach(f => merged.delete(f.toLowerCase()));
  const result = [];
  const seen = new Set();
  [...fromTextarea, ...tagForceSet].forEach(f => {
    const lc = f.toLowerCase();
    if (merged.has(lc) && !seen.has(lc)) { result.push(f); seen.add(lc); }
  });
  return result.join(", ");
}

function updateAllowPlainHighlight() {
  const el = document.getElementById("allow-plain");
  const hasValue = el.value.trim().split(",").map(s => s.trim()).filter(Boolean).length > 0;
  el.classList.toggle("has-value", hasValue);
}

function renderSuggestedFields(fields) {
  const bar = document.getElementById("suggested-bar");
  const container = document.getElementById("suggested-tags");
  if (!fields || fields.length === 0) {
    bar.style.display = "none";
    return;
  }
  bar.style.display = "";
  container.innerHTML = "";
  // Check which fields are already in the persistent whitelist
  const currentAllow = new Set(
    document.getElementById("allow-plain").value.split(",").map(s => s.trim().toLowerCase()).filter(Boolean)
  );
  let hasAccepted = false;
  fields.forEach(field => {
    const tag = document.createElement("span");
    const isAccepted = acceptedSuggestions.has(field.toLowerCase()) || currentAllow.has(field.toLowerCase());
    if (isAccepted) hasAccepted = true;
    tag.className = "suggested-field-tag" + (isAccepted ? " accepted" : "");
    tag.textContent = isAccepted ? field + " ✓" : field;
    tag.title = isAccepted
      ? "Уже добавлено в белый список"
      : "Нажмите, чтобы добавить в белый список";
    if (!isAccepted) {
      tag.onclick = () => acceptSuggestedField(field);
    }
    container.appendChild(tag);
  });
  // Add confirm button inline after tags
  if (hasAccepted) {
    const btn = document.createElement("button");
    btn.className = "suggest-confirm-btn";
    if (suggestConfirmed) {
      btn.textContent = "Подтверждено ✓";
      btn.disabled = true;
      btn.style.opacity = "0.5";
      btn.style.cursor = "default";
    } else {
      btn.textContent = "Подтвердить";
      btn.onclick = confirmSuggestedFields;
    }
    container.appendChild(btn);
  }
}

async function acceptSuggestedField(fieldName) {
  suggestConfirmed = false; // reset — whitelist changed
  acceptedSuggestions.add(fieldName.toLowerCase());
  // Add to persistent whitelist textarea
  const el = document.getElementById("allow-plain");
  const current = el.value.split(",").map(s => s.trim()).filter(Boolean);
  if (!current.some(f => f.toLowerCase() === fieldName.toLowerCase())) {
    current.push(fieldName);
    el.value = current.join(", ");
    updateAllowPlainHighlight();
  }
  // Also add to tag allow set for immediate remask
  tagAllowSet.add(fieldName);
  tagForceSet.delete(fieldName);
  // Send to server as persistent whitelist + remask (but don't signal agent yet)
  try {
    await api("/api/set_whitelist", {
      allow_plain_fields: el.value,
      force_mask_fields: document.getElementById("force-mask").value
    });
    await api("/api/remask", {
      force_mask_fields: mergedForceMask(),
      allow_plain_fields: mergedAllowPlain()
    });
    // Re-render tags to update accepted state and show confirm button
    const fields = [...document.querySelectorAll("#suggested-tags .suggested-field-tag")].map(el => el.textContent.replace(" ✓", ""));
    if (fields.length > 0) renderSuggestedFields(fields);
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

async function confirmSuggestedFields() {
  try {
    await api("/api/confirm_suggested_fields", {});
    suggestConfirmed = true;
    toast("Подтверждено — агент получит обновлённые данные.");
    stopFaviconBlink();
    // Re-render to show disabled button
    const fields = [...document.querySelectorAll("#suggested-tags .suggested-field-tag")].map(el => el.textContent.replace(" ✓", ""));
    if (fields.length > 0) renderSuggestedFields(fields);
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

let allowPlainDebounce = null;
function onAllowPlainInput() {
  suggestConfirmed = false; // reset — whitelist changed manually
  updateAllowPlainHighlight();
  syncAcceptedSuggestions();
  clearTimeout(allowPlainDebounce);
  allowPlainDebounce = setTimeout(async () => {
    const allowPlain = document.getElementById("allow-plain").value;
    const forceMask = document.getElementById("force-mask").value;
    await api("/api/set_whitelist", { allow_plain_fields: allowPlain, force_mask_fields: forceMask });
  }, 400);
}

// Sync acceptedSuggestions with current whitelist — remove fields that were deleted from whitelist
function syncAcceptedSuggestions() {
  const currentAllow = new Set(
    document.getElementById("allow-plain").value.split(",").map(s => s.trim().toLowerCase()).filter(Boolean)
  );
  for (const f of [...acceptedSuggestions]) {
    if (!currentAllow.has(f)) {
      acceptedSuggestions.delete(f);
    }
  }
  // Re-render suggested field tags if any are shown
  const bar = document.getElementById("suggested-bar");
  if (bar && bar.style.display !== "none") {
    const fields = [...document.querySelectorAll("#suggested-tags .suggested-field-tag")].map(el => el.textContent.replace(" ✓", ""));
    if (fields.length > 0) renderSuggestedFields(fields);
  }
}

async function clearAllowPlain() {
  const el = document.getElementById("allow-plain");
  if (!el.value.trim()) { toast("Список уже пуст"); return; }
  el.value = "";
  acceptedSuggestions.clear();
  updateAllowPlainHighlight();
  syncAcceptedSuggestions();
  try {
    await api("/api/set_whitelist", {
      allow_plain_fields: "",
      force_mask_fields: document.getElementById("force-mask").value
    });
    await api("/api/remask", {
      force_mask_fields: mergedForceMask(),
      allow_plain_fields: mergedAllowPlain()
    });
    toast("\u2705 Белый список очищен (для текущей сессии)");
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function saveAllowPlainAsDefault() {
  try {
    const s = await api("/api/settings");
    const existing = (s.defaults_allow_plain_fields || "").split(",").map(f => f.trim()).filter(Boolean);
    const current = mergedAllowPlain().split(",").map(f => f.trim()).filter(Boolean);
    // Build case-preserving union: current takes precedence for casing
    const caseMap = new Map();
    existing.forEach(f => caseMap.set(f.toLowerCase(), f));
    current.forEach(f => caseMap.set(f.toLowerCase(), f));
    const merged = [...caseMap.values()].join(", ");
    const payload = { ...s, defaults_allow_plain_fields: merged, mcp_token: "" };
    const r = await api("/api/settings", payload);
    if (r.ok) toast("\u2705 Список сохранён в постоянные настройки");
    else toast("Ошибка: " + (r.error || "?"));
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function allowField(fieldName) {
  tagAllowSet.add(fieldName);
  tagForceSet.delete(fieldName);
  // Flush into textareas so the user sees the change and "В настройки" picks it up
  const apEl = document.getElementById("allow-plain");
  apEl.value = mergedAllowPlain();
  tagAllowSet.clear();
  const fmEl = document.getElementById("force-mask");
  const fmLc = fieldName.toLowerCase();
  const fmFiltered = fmEl.value.split(",").map(s => s.trim()).filter(s => s && s.toLowerCase() !== fmLc);
  fmEl.value = fmFiltered.join(", ");
  updateAllowPlainHighlight();
  try {
    const r = await api("/api/set_whitelist", {
      allow_plain_fields: apEl.value,
      force_mask_fields: fmEl.value
    });
    if (r.ok) {
      toast("\u2705 " + fieldName + " добавлено в белый список");
      flashTag(fieldName, "open");
    } else toast(r.error || "Перезапустите запрос.");
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

async function reMaskField(fieldName) {
  tagForceSet.add(fieldName);
  tagAllowSet.delete(fieldName);
  // Flush into textareas so the user sees the change
  const apEl = document.getElementById("allow-plain");
  const apLc = fieldName.toLowerCase();
  const apFiltered = apEl.value.split(",").map(s => s.trim()).filter(s => s && s.toLowerCase() !== apLc);
  apEl.value = apFiltered.join(", ");
  const fmEl = document.getElementById("force-mask");
  fmEl.value = mergedForceMask();
  tagForceSet.clear();
  updateAllowPlainHighlight();
  try {
    const r = await api("/api/set_whitelist", {
      allow_plain_fields: apEl.value,
      force_mask_fields: fmEl.value
    });
    if (r.ok) {
      toast("\u{1f512} " + fieldName + " зашифровано");
      flashTag(fieldName, "mask");
    } else toast(r.error || "Ошибка ремаскировки");
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

function flashTag(fieldName, type) {
  // Find tag elements containing this field name in mask-bar
  const tags = document.querySelectorAll("#mask-bar .unmasked-tag, #mask-bar .masked-col-tag, #mask-bar .excluded-col-tag");
  const cls = type === "open" ? "tag-flash-open" : "tag-flash-mask";
  tags.forEach(tag => {
    if (tag.textContent.trim().replace(/\s*\u00d7$/, "") === fieldName) {
      tag.classList.remove("tag-flash-open", "tag-flash-mask");
      void tag.offsetWidth; // force reflow
      tag.classList.add(cls);
      tag.addEventListener("animationend", () => tag.classList.remove(cls), { once: true });
    }
  });
}

async function doApproveSend() {
  try {
    // Send filtered indices so server builds bundle from visible rows only
    const isFiltered = filteredIndices.length !== resultRows.length;
    const payload = isFiltered ? { filtered_indices: filteredIndices } : {};
    const r = await api("/api/approve_send", payload);
    if (r.ok) toast("Данные отправлены агенту" + (isFiltered ? " (" + filteredIndices.length + " строк)" : "") + ".");
    else toast(r.error || "Ошибка");
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function doApproveCode() {
  // Take code from editor (user may have edited it)
  const code = (window.queryEditor ? window.queryEditor.state.doc.toString() : "").trim();
  if (!code) { toast("Введите код для выполнения"); return; }
  try {
    const r = await api("/api/execute_code", { task: "Выполнение кода из UI", code: code });
    if (r.ok) { toast("Код выполнен."); saveToHistory(code, 0, "code"); }
    else toast(r.error || r.message || "Ошибка выполнения");
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function doRejectCode() {
  try {
    const r = await api("/api/reject_code", {});
    if (r.ok) toast("Выполнение кода отклонено.");
    else toast(r.error || "Ошибка");
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function setEditorMode(codeMode) {
  try {
    await api("/api/code_mode", { enabled: codeMode });
  } catch (e) { toast("Ошибка: " + e.message); }
}

async function setSendMode(auto) {
  try {
    await api("/api/auto_send", { enabled: auto });
  } catch (e) { toast("Ошибка: " + e.message); }
}

function getExcludedList() {
  return (appState.excluded_fields || "").split(",").map(s => s.trim()).filter(Boolean);
}

async function excludeField(fieldName) {
  const current = getExcludedList();
  if (current.some(f => f.toLowerCase() === fieldName.toLowerCase())) {
    toast(fieldName + " уже исключено");
    return;
  }
  current.push(fieldName);
  // Clean up text fields — remove excluded field from both
  const apEl = document.getElementById("allow-plain");
  apEl.value = apEl.value.split(",").map(s => s.trim()).filter(Boolean)
    .filter(f => f.toLowerCase() !== fieldName.toLowerCase()).join(", ");
  const fmEl = document.getElementById("force-mask");
  fmEl.value = fmEl.value.split(",").map(s => s.trim()).filter(Boolean)
    .filter(f => f.toLowerCase() !== fieldName.toLowerCase()).join(", ");
  try {
    const r = await api("/api/exclude_fields", { excluded_fields: current.join(", ") });
    if (r.ok) {
      toast(fieldName + " исключено из выборки.");
    } else {
      toast(r.error || "Ошибка исключения поля.");
    }
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

async function restoreField(fieldName) {
  const current = getExcludedList().filter(f => f.toLowerCase() !== fieldName.toLowerCase());
  try {
    const r = await api("/api/exclude_fields", { excluded_fields: current.join(", ") });
    if (r.ok) {
      toast(fieldName + " возвращено в выборку.");
    } else {
      toast(r.error || "Ошибка возврата поля.");
    }
  } catch (e) {
    toast("Ошибка: " + e.message);
  }
}

async function doConnect() {
  const url = document.getElementById("url").value.trim();
  const token = document.getElementById("token").value.trim();
  const useSavedToken = headerHasSavedTokenMarker() || (!token && appState.has_saved_token);
  if (!url) { toast("Введите MCP URL"); return; }
  const dot = document.getElementById("conn-dot");
  dot.className = "conn-dot pulse";
  const payload = { url };
  if (useSavedToken) {
    payload.use_saved_token = true;
  } else if (token) {
    payload.token = token;
  } else {
    payload.token = "";
  }
  try {
    const r = await api("/api/connect", payload, 60000);
    if (r.ok) toast("Подключено");
    else toast("Ошибка: " + (r.error || "?").substring(0, 100));
  } catch (e) {
    if (e.name === "AbortError") toast("Таймаут подключения (60 сек)");
    else toast("Ошибка: " + e.message);
  }
}

async function doQuery(mode) {
  const queryText = (window.queryEditor ? window.queryEditor.state.doc.toString() : "").trim();
  if (!queryText) { toast("Введите текст запроса"); return; }
  if (!appState.connection || !appState.connection.verified) { toast("Сначала подключитесь к серверу"); return; }
  const forceMask = mergedForceMask();
  const allowPlain = mergedAllowPlain();
  document.getElementById("btn-query-masked").disabled = true;
  try {
    const r = await api("/api/query", {
      task: "Запрос из UI",
      query_text: queryText,
      mode: mode,
      force_mask_fields: forceMask,
      allow_plain_fields: allowPlain
    }, 310000);
    if (r.ok) {
      toast("Запрос выполнен (" + mode + ")");
      // Save to history
      const rowCount = (appState.rows && appState.rows.length) || 0;
      saveToHistory(queryText, rowCount);
    } else {
      toast("Ошибка: " + (r.message || r.error || "?").substring(0, 150));
    }
  } catch (e) {
    if (e.name === "AbortError") toast("Таймаут запроса (300 сек)");
    else toast("Ошибка: " + e.message);
  } finally {
    document.getElementById("btn-query-masked").disabled = false;
  }
}

async function doCancelQuery() {
  try {
    const r = await api("/api/cancel_query", {});
    if (r.ok) toast("Запрос отменён");
  } catch (e) {
    toast("Ошибка отмены: " + e.message);
  }
}

function clearToken() {
  document.getElementById("token").value = "";
  api("/api/disconnect", {});
}

async function doClearSession() {
  await api("/api/clear_session", {});
  sortColumn = null; sortReverse = false;
  resultRows = []; maskedRows = []; filteredRows = []; filteredMaskedRows = []; filteredIndices = [];
  tagAllowSet.clear(); tagForceSet.clear();
  rowDataVersion++;
  toast("Сессия очищена");
}

function setTheme(name) {
  document.body.className = "theme-" + name;
  api("/api/theme", { theme: name });
}

// ─── Query Formatter ─────────────────────────────────────────────────

function formatQuery() {
  if (!window.queryEditor) return;
  const src = window.queryEditor.state.doc.toString();
  if (!src.trim()) return;
  const formatted = format1CQuery(src);
  if (window.queryEditor.ace) {
    window.queryEditor.ace.setValue(formatted, -1);
    window.queryEditor.ace.clearSelection();
  } else {
    const len = window.queryEditor.state.doc.length;
    window.queryEditor.dispatch({changes: {from: 0, to: len, insert: formatted}});
  }
}

function format1CQuery(src) {
  // Preserve string literals
  const strings = [];
  let text = src.replace(/"([^"]*)"/g, (m) => {
    strings.push(m); return "\x01S" + (strings.length - 1) + "\x01";
  });

  // Normalize whitespace
  text = text.replace(/\s+/g, " ").trim();

  // Word boundary for Unicode (cyrillic-aware)
  // We use a tokenizer approach instead of \b
  const T = "\t";
  const NL = "\n";

  // Keyword sets (uppercase for matching)
  const newlineBefore = new Set([
    "ВЫБРАТЬ","SELECT","ИЗ","FROM","ГДЕ","WHERE",
    "ИМЕЮЩИЕ","HAVING","ИТОГИ","TOTALS"
  ]);
  const newlineBeforeMulti = [
    ["ГРУППИРОВАТЬ","ПО"], ["GROUP","BY"],
    ["УПОРЯДОЧИТЬ","ПО"], ["ORDER","BY"],
    ["ОБЪЕДИНИТЬ","ВСЕ"], ["UNION","ALL"],
    ["ЛЕВОЕ","СОЕДИНЕНИЕ"], ["LEFT","JOIN"],
    ["ПРАВОЕ","СОЕДИНЕНИЕ"], ["RIGHT","JOIN"],
    ["ВНУТРЕННЕЕ","СОЕДИНЕНИЕ"], ["INNER","JOIN"],
    ["ПОЛНОЕ","СОЕДИНЕНИЕ"], ["FULL","JOIN"],
  ];
  const indentBefore = new Set(["И","ИЛИ","AND","OR"]);
  const selectKw = new Set(["ВЫБРАТЬ","SELECT"]);

  // Tokenize: split into words, punctuation, whitespace
  const tokens = [];
  const tokenRe = /([\p{L}_][\p{L}\p{N}_.]*|\d+(?:\.\d+)?|"[^"]*"|'[^']*'|[(),;=<>!+\-*/]|\S)/gu;
  let m;
  while ((m = tokenRe.exec(text)) !== null) tokens.push(m[1]);

  let result = "";
  let inSelect = false;
  let depth = 0; // paren depth

  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    const up = tok.toUpperCase();
    const next = (tokens[i + 1] || "").toUpperCase();

    // Check multi-word keywords (lookahead)
    let multiMatch = null;
    for (const pair of newlineBeforeMulti) {
      if (up === pair[0] && next === pair[1]) { multiMatch = pair; break; }
    }

    if (multiMatch) {
      if (result.length > 0) result += NL;
      result += tok + " " + tokens[i + 1];
      i++; // skip next token
      inSelect = false;
      result += NL + T;
    } else if (newlineBefore.has(up)) {
      if (result.length > 0) result += NL;
      result += tok;
      inSelect = selectKw.has(up);
      result += NL + T;
    } else if (indentBefore.has(up) && depth === 0) {
      result += NL + T + tok + " ";
    } else if (tok === ",") {
      if (inSelect && depth === 0) {
        result += "," + NL + T;
      } else {
        result += ", ";
      }
    } else if (tok === "(") {
      depth++;
      result += "(";
    } else if (tok === ")") {
      depth = Math.max(0, depth - 1);
      result += ")";
    } else {
      // Add space before token if needed
      if (result.length > 0) {
        const last = result[result.length - 1];
        if (last !== NL && last !== T && last !== "(" && last !== " " && tok !== ")") {
          result += " ";
        }
      }
      result += tok;
    }
  }

  result = result.replace(/\n{3,}/g, "\n\n").trim();

  // Restore string literals
  result = result.replace(/\x01S(\d+)\x01/g, (m, idx) => strings[parseInt(idx)]);
  return result;
}

