"use strict";

const TOKEN = "{{SESSION_TOKEN}}";
let appState = { version: 0 };
let resultRows = [];
let resultHeaders = [];
let maskedRows = [];
let maskedHeaders = [];
let maskedColumns = [];
let tagAllowSet = new Set();   // fields opened via tag clicks (separate from textarea)
let tagForceSet = new Set();   // fields force-masked via tag clicks
let acceptedSuggestions = new Set(); // suggested fields already approved by user
let suggestConfirmed = false; // true after user clicked Confirm
let sortColumn = null;
let sortReverse = false;

// Virtual scroll state
let filteredRows = [];       // current filtered+sorted view (indices or rows)
let filteredMaskedRows = []; // parallel masked rows for filtered view
let filteredIndices = [];    // original row indices for filtered view (for agent send)
let displayCols = [];        // column descriptors
let showOriginals = false;   // show original value columns alongside masked
let vsRowHeight = 30;        // estimated row height in px
let vsVisibleCount = 50;     // rows to render
let vsScrollTop = 0;
let stableColumnOrder = [];   // fixed order for field tags (no re-sorting on click)
let lastDataVersion = -1;     // tracks server data_version for row refresh
let lastQueryVersion = -1;   // tracks server query_version — resets when new query arrives (not remask)
let lastEditorSyncQueryVersion = -1; // last query_version pushed into editor
let headerTokenTouched = false; // true only after user edits the header token field
let _lastTabSwitchQV = -1;   // prevents tab auto-switch on non-query state updates
let vsRenderStart = 0;
let vsRenderEnd = 0;
let vsTicking = false;
let rowDataVersion = 0;      // tracks when row data changes
const SAVED_TOKEN_SENTINEL = "__saved_token__";

function headerHasSavedTokenMarker() {
  const tokenEl = document.getElementById("token");
  return !!(tokenEl && tokenEl.dataset.savedTokenPlaceholder === "1");
}

function syncHeaderTokenField(s) {
  const tokenEl = document.getElementById("token");
  if (!tokenEl) return;

  if (s.has_saved_token && !headerTokenTouched) {
    tokenEl.dataset.savedTokenPlaceholder = "1";
    tokenEl.value = SAVED_TOKEN_SENTINEL;
    tokenEl.placeholder = "Сохранённый ключ";
    return;
  }

  if (tokenEl.dataset.savedTokenPlaceholder === "1") {
    delete tokenEl.dataset.savedTokenPlaceholder;
    if (tokenEl.value === SAVED_TOKEN_SENTINEL) tokenEl.value = "";
  }

  if (!tokenEl.value) {
    tokenEl.placeholder = s.has_saved_token ? "Сохранённый ключ" : "Ключ";
  }
}

// ─── API ─────────────────────────────────────────────────────────────

async function api(path, body, timeoutMs) {
  const opts = { headers: { "X-Session-Token": TOKEN } };
  if (body !== undefined) {
    opts.method = "POST";
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  if (timeoutMs) {
    const ctrl = new AbortController();
    opts.signal = ctrl.signal;
    setTimeout(() => ctrl.abort(), timeoutMs);
  }
  const r = await fetch(path, opts);
  return r.json();
}

// ─── SSE ─────────────────────────────────────────────────────────────

function connectSSE() {
  const es = new EventSource("/api/events?token=" + encodeURIComponent(TOKEN));
  es.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.version !== appState.version) fetchState();
  };
  es.onerror = () => { setTimeout(connectSSE, 3000); es.close(); };
}

let fetchingRows = false;
async function fetchState() {
  try {
    const s = await api("/api/state");
    const newRowCount = (s.result && s.result.row_count) || 0;
    const newDataVersion = s.data_version || 0;
    const newQueryVersion = s.query_version || 0;
    // Reset tag overrides BEFORE render so tags draw in correct (encrypted) state
    if (newQueryVersion !== lastQueryVersion) {
      tagAllowSet.clear();
      tagForceSet.clear();
      acceptedSuggestions.clear();
      stableColumnOrder = [];
      updateAllowPlainHighlight();
      lastQueryVersion = newQueryVersion;
    }
    appState = s;
    render(s);
    // Fetch rows when data version changed (remask, new query, etc.) or initial load
    if ((newDataVersion !== lastDataVersion || (newRowCount > 0 && resultRows.length === 0)) && !fetchingRows) {
      lastDataVersion = newDataVersion;
      await fetchAllRows();
    }
  } catch (e) { console.error("fetchState:", e); }
}

async function fetchAllRows() {
  const total = (appState.result && appState.result.row_count) || 0;
  if (total === 0) {
    resultRows = []; maskedRows = []; resultHeaders = []; maskedHeaders = [];
    maskedColumns = [];
    rowDataVersion++;
    rebuildFilterColumns();
    applyResultView();
    return;
  }
  fetchingRows = true;
  try {
    // Fetch all rows in chunks (server-side pagination for transport)
    const chunkSize = 2000;
    let allRows = [], allMasked = [];
    for (let offset = 0; offset < total; offset += chunkSize) {
      const r = await api("/api/rows?offset=" + offset + "&limit=" + chunkSize);
      if (!r || !r.rows) break;
      allRows = allRows.concat(r.rows);
      allMasked = allMasked.concat(r.masked_rows || []);
      if (offset === 0) {
        resultHeaders = r.headers || [];
        maskedHeaders = r.masked_headers || [];
        maskedColumns = r.masked_columns || [];
      }
    }
    resultRows = allRows;
    maskedRows = allMasked;
    rowDataVersion++;
    rebuildFilterColumns();
    applyResultView();
    updateApproveBtn();
  } finally {
    fetchingRows = false;
  }
}

// ─── Render ──────────────────────────────────────────────────────────

function render(s) {
  // Connection dot
  const dot = document.getElementById("conn-dot");
  if (s.connection.verified) {
    dot.className = "conn-dot ok";
    dot.title = "Подключено";
  } else {
    dot.className = "conn-dot fail";
    dot.title = "Не подключено";
  }

  // URL field (set once)
  const urlEl = document.getElementById("url");
  if (s.connection.url && !urlEl.value) urlEl.value = s.connection.url;

  // Token hint — show placeholder if saved token exists
  const tokenEl = document.getElementById("token");
  if (s.has_saved_token && !headerTokenTouched && tokenEl.value) {
    tokenEl.value = "";
  }
  if (s.has_saved_token && !tokenEl.value) {
    tokenEl.placeholder = "Сохранённый ключ (\u2022\u2022\u2022)";
  } else if (!s.has_saved_token && !tokenEl.value) {
    tokenEl.placeholder = "Ключ";
  }

  // Per-session mask fields — never prefill from defaults (they merge on backend)

  // Session meta
  syncHeaderTokenField(s);
  const metaEl = document.getElementById("meta-session");
  if (s.session_id) {
    metaEl.textContent = s.session_id + " \u00b7 " + (s.mode || "direct");
    metaEl.style.display = "";
  } else {
    metaEl.style.display = "none";
  }

  // Task
  document.getElementById("task-text").textContent = s.task || "Ожидаю задачу от контроллера";

  // Query state badge + elapsed timer
  const qs = document.getElementById("query-state");
  qs.className = "state-badge sb-" + s.query_state;
  if (s.query_state === "running") {
    if (!window._queryStartTime) window._queryStartTime = Date.now();
    if (!window._elapsedTimer) {
      window._elapsedTimer = setInterval(function() {
        const elapsed = Math.round((Date.now() - window._queryStartTime) / 1000);
        const label = qs.querySelector(".state-label");
        if (label) label.textContent = s.query_state_text + " (" + elapsed + "с)";
      }, 1000);
    }
  } else {
    if (window._elapsedTimer) {
      clearInterval(window._elapsedTimer);
      window._elapsedTimer = null;
    }
    if (window._queryStartTime) {
      const totalSec = Math.round((Date.now() - window._queryStartTime) / 1000);
      window._queryStartTime = null;
      if (totalSec > 0 && s.query_state === "done") {
        qs.querySelector(".state-label").textContent = s.query_state_text + " (" + totalSec + "с)";
      } else {
        qs.querySelector(".state-label").textContent = s.query_state_text;
      }
    } else {
      qs.querySelector(".state-label").textContent = s.query_state_text;
    }
  }

  // Cancel button visibility & query buttons enabled state
  const isCodeMode2 = !!(s.code_mode);
  const cancelBtn = document.getElementById("cancel-btn");
  if (cancelBtn) cancelBtn.style.display = (!isCodeMode2 && s.query_state === "running") ? "" : "none";
  if (s.query_state !== "running" && !isCodeMode2) {
    document.getElementById("btn-query-masked").disabled = false;
  }

  // Truncation banner
  const truncBanner = document.getElementById("truncation-banner");
  if (s.rows_truncated) {
    truncBanner.textContent = "Результат обрезан: показано " + (s.result.row_count || 0) + " из " + (s.total_row_count || "?") + " строк (лимит: " + (s.max_rows || "?") + ")";
    truncBanner.style.display = "";
  } else {
    truncBanner.style.display = "none";
  }

  // Placeholder (rows are fetched separately via /api/rows)
  if (s.result.placeholder && s.result.row_count === 0) showPlaceholder(s.result.placeholder, s.result.placeholder_error);

  // Analysis
  renderMarkdownPanel("analysis-masked", s.analysis_masked || "");
  renderMarkdownPanel("analysis-display", s.analysis_display || "");

  // Raw
  // Sync query input from server. New query_version must replace stale editor
  // text even if the editor was focused before the agent query arrived.
  const serverQuery = s.query_preview || "";
  const serverQueryVersion = s.query_version || 0;
  const mustSyncEditor = serverQueryVersion !== lastEditorSyncQueryVersion;
  if (serverQuery && window.queryEditor && (mustSyncEditor || !window.queryEditorFocused)) {
    const currentText = window.queryEditor.state.doc.toString();
    if (currentText !== serverQuery) {
      if (window.queryEditor.ace) {
        window.queryEditor.ace.setValue(serverQuery, -1);
        window.queryEditor.ace.clearSelection();
      } else {
        window.queryEditor.dispatch({
          changes: {from: 0, to: currentText.length, insert: serverQuery}
        });
      }
    }
    lastEditorSyncQueryVersion = serverQueryVersion;
  }
  const rawEl = document.getElementById("raw-response");
  // Try to pretty-print JSON in raw response
  const rawText = s.raw_response || "";
  if (rawText.trim().startsWith("{") || rawText.trim().startsWith("[")) {
    try {
      rawEl.textContent = JSON.stringify(JSON.parse(rawText), null, 2);
    } catch(e) {
      rawEl.textContent = rawText;
    }
  } else {
    rawEl.textContent = rawText;
  }
  rawEl.className = "text-panel raw-border-" + (s.raw_state || "neutral");

  // Show/hide code mode buttons
  const isCodeMode = !!(s.code_mode);
  const hasPendingCode = isCodeMode && !!s.pending_code;
  document.getElementById("btn-approve-code").style.display = isCodeMode ? "" : "none";
  document.getElementById("btn-reject-code").style.display = hasPendingCode ? "" : "none";
  document.getElementById("btn-query-masked").style.display = isCodeMode ? "none" : "";
  // Highlight active mode toggle
  const mqBtn = document.getElementById("mode-query-btn");
  const mcBtn = document.getElementById("mode-code-btn");
  mqBtn.classList.toggle("active", !isCodeMode);
  mcBtn.classList.toggle("active", isCodeMode);

  // Auto-switch tab only when a new query/code result arrives (query_version changed)
  // Never switch away from MCP (raw) or Settings tabs — user stays where they are
  const newQV = s.query_version || 0;
  if (s.active_tab && newQV !== _lastTabSwitchQV) {
    const currentPanel = document.querySelector(".tab-panel.active");
    const currentTab = currentPanel ? currentPanel.id.replace("tab-", "") : "";
    if (currentTab !== s.active_tab && currentTab !== "settings" && currentTab !== "raw") {
      showTab(s.active_tab);
    }
    _lastTabSwitchQV = newQV;
  }

  // Status bar
  document.getElementById("status-text").textContent = s.status || "Готово";
  document.getElementById("status-security").textContent = s.security_hint || "";
  document.getElementById("status-session").textContent = s.session_id ? "ID: " + s.session_id : "";
  document.getElementById("status-bridge").textContent = s.bridge_info || "";

  // Bridge address on MCP tab (secret loaded separately via /api/bridge_secret)
  document.getElementById("bridge-addr").textContent = s.bridge_info || "";

  // Sync persistent whitelist textarea from server (don't overwrite if user is actively typing)
  const allowPlainEl = document.getElementById("allow-plain");
  const serverAllowPlain = s.persistent_allow_plain || "";
  if (allowPlainEl !== document.activeElement && allowPlainEl.value !== serverAllowPlain) {
    allowPlainEl.value = serverAllowPlain;
    updateAllowPlainHighlight();
  }

  // Sync type policy from server (only when the types sub-tab is visible —
  // otherwise working copy may be mid-edit).
  const typesPanel = document.getElementById("settings-sub-types");
  if (typesPanel && !typesPanel.classList.contains("active")) {
    tpInitFromState(s);
  }

  // Agent-suggested fields
  renderSuggestedFields(s.suggested_fields || []);
  handleAgentWaiting(s.agent_waiting_approval || false);

  // NER status
  const nerEl = document.getElementById("ner-status");
  if (nerEl && s.ner_status) nerEl.textContent = s.ner_status;

  // Unmasked/masked columns indicator — render into both tabs
  const unmaskedCols = (s.masked_result && s.masked_result.unmasked_columns) || [];
  const maskedCols = (s.masked_result && s.masked_result.masked_columns) || [];
  const excludedRaw = s.excluded_fields || "";
  const excludedList = excludedRaw.split(",").map(s => s.trim()).filter(Boolean);

  // Build stable column order
  if (unmaskedCols.length > 0 || maskedCols.length > 0) {
    const allCols = [...unmaskedCols, ...maskedCols];
    const allSet = new Set(allCols);
    allCols.forEach(col => { if (!stableColumnOrder.includes(col)) stableColumnOrder.push(col); });
    stableColumnOrder = stableColumnOrder.filter(col => allSet.has(col));
  } else {
    stableColumnOrder = [];
  }

  const unmaskedSet = new Set(unmaskedCols);
  const excludedSet = new Set(excludedList.map(s => s.toLowerCase()));
  // Render field tags into a container
  function renderFieldTags(container) {
    container.innerHTML = "";
    stableColumnOrder.forEach(col => {
      if (excludedSet.has(col.toLowerCase())) return; // skip excluded fields
      const isUnmasked = unmaskedSet.has(col);
      const tag = document.createElement("span");
      tag.className = isUnmasked ? "unmasked-tag" : "masked-col-tag";
      if (isUnmasked) tag.style.cursor = "pointer";
      tag.textContent = col;
      tag.title = isUnmasked
        ? "Передаётся открыто. Нажмите, чтобы зашифровать"
        : "Зашифровано. Нажмите, чтобы разрешить передачу открыто";
      tag.onclick = isUnmasked ? () => reMaskField(col) : () => allowField(col);
      container.appendChild(tag);
      const x = document.createElement("span");
      x.className = "exclude-x";
      x.textContent = "✕";
      x.title = "Исключить поле из выборки";
      x.onclick = () => excludeField(col);
      container.appendChild(x);
    });
  }
  function renderExcludedTags(container) {
    container.innerHTML = "";
    excludedList.forEach(col => {
      const tag = document.createElement("span");
      tag.className = "excluded-col-tag";
      tag.textContent = col + " ↩";
      tag.title = "Исключено. Нажмите, чтобы вернуть поле";
      tag.onclick = () => restoreField(col);
      container.appendChild(tag);
    });
  }

  // Result tab — field tags (hide when auto-send is on — agent gets all fields anyway)
  const autoSendOn = !!s.auto_send_to_agent;
  const unmaskedInfoR = document.getElementById("unmasked-info-result");
  if (stableColumnOrder.length > 0 && !autoSendOn) {
    unmaskedInfoR.style.display = "";
    renderFieldTags(document.getElementById("unmasked-cols-result"));
  } else { unmaskedInfoR.style.display = "none"; }

  const excludedInfoR = document.getElementById("excluded-info-result");
  if (excludedList.length > 0 && !autoSendOn) {
    excludedInfoR.style.display = "";
    renderExcludedTags(document.getElementById("excluded-cols-result"));
  } else { excludedInfoR.style.display = "none"; }

  // Header mode toggle buttons
  const hdrAutoBtn = document.getElementById("hdr-mode-auto");
  const hdrManualBtn = document.getElementById("hdr-mode-manual");
  if (hdrAutoBtn && hdrManualBtn) {
    hdrAutoBtn.style.background = autoSendOn ? "var(--success)" : "";
    hdrAutoBtn.style.color = autoSendOn ? "#fff" : "";
    hdrManualBtn.style.background = !autoSendOn ? "var(--warning)" : "";
    hdrManualBtn.style.color = !autoSendOn ? "#fff" : "";
  }

  // Rate limit warning
  const rlWarn = document.getElementById("rate-limit-warning");
  if (rlWarn) {
    if (s.rate_limit_triggered && s.rate_limit_message) {
      rlWarn.textContent = s.rate_limit_message;
      rlWarn.style.display = "";
    } else {
      rlWarn.style.display = "none";
    }
  }

  updateApproveBtn();
}

function updateApproveBtn() {
  const approveBtn = document.getElementById("approve-send-btn");
  if (!approveBtn) return;
  const autoSendOn = !!(appState && appState.auto_send_to_agent);
  const hasResultData = resultRows.length > 0;
  const shouldShow = hasResultData && !autoSendOn;
  const wasHidden = approveBtn.style.visibility === "hidden";
  approveBtn.style.visibility = shouldShow ? "visible" : "hidden";
  // Notify when approval is newly needed
  if (shouldShow && wasHidden) {
    playBeep();
    desktopNotify("1C Trusted Gateway", "Данные готовы — ожидается одобрение");
  }
}

// ─── Tabs ────────────────────────────────────────────────────────────

function showTab(name, btn) {
  document.querySelectorAll(".tab-panel").forEach(p => p.classList.remove("active"));
  const panel = document.getElementById("tab-" + name);
  if (panel) panel.classList.add("active");
  document.querySelectorAll(".tab-btn").forEach(b => b.classList.remove("active"));
  if (btn) { btn.classList.add("active"); }
  else {
    const btns = document.querySelectorAll("#toolbar .tab-btn");
    const map = { raw: 0, result: 1, analysis: 2, settings: 3 };
    if (map[name] !== undefined && btns[map[name]]) btns[map[name]].classList.add("active");
  }
  if (name === "settings") loadSettingsForm();
  if (name === "raw") loadBridgeSecret();
}

