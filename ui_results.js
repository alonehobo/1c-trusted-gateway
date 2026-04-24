// ─── Result Table ────────────────────────────────────────────────────

function rebuildFilterColumns() {
  // Update column options in all existing filter rows
  document.querySelectorAll(".filter-row select.filter-col").forEach(sel => {
    const cur = sel.value;
    sel.innerHTML = "";
    resultHeaders.forEach(h => { const o = document.createElement("option"); o.textContent = h; sel.appendChild(o); });
    if ([...sel.options].some(o => o.value === cur)) sel.value = cur;
  });
}

function addFilterRow(col, op, val) {
  const container = document.getElementById("filter-rows");
  const row = document.createElement("div");
  row.className = "filter-row";

  const colSel = document.createElement("select");
  colSel.className = "filter-col";
  resultHeaders.forEach(h => { const o = document.createElement("option"); o.textContent = h; colSel.appendChild(o); });
  if (col && [...colSel.options].some(o => o.value === col)) colSel.value = col;
  colSel.onchange = () => applyResultView();

  const opSel = document.createElement("select");
  opSel.className = "filter-op";
  ["содержит", "не содержит", "=", "!=", ">", ">=", "<", "<="].forEach(o => {
    const opt = document.createElement("option"); opt.textContent = o; opSel.appendChild(opt);
  });
  if (op) opSel.value = op;
  opSel.onchange = () => applyResultView();

  const valInp = document.createElement("input");
  valInp.className = "filter-val";
  valInp.type = "text";
  valInp.placeholder = "значение";
  if (val) valInp.value = val;
  valInp.oninput = () => applyResultView();

  const removeBtn = document.createElement("button");
  removeBtn.className = "btn-remove";
  removeBtn.innerHTML = "&times;";
  removeBtn.title = "Удалить фильтр";
  removeBtn.onclick = () => { row.remove(); applyResultView(); };

  row.append(colSel, opSel, valInp, removeBtn);
  container.appendChild(row);
  applyResultView();
}

function parseNumericValue(text) {
  if (text == null || text === "") return NaN;
  // Normalize: remove spaces, replace comma with dot
  const s = String(text).trim().replace(/\s/g, "").replace(",", ".");
  const n = parseFloat(s);
  // Ensure the whole string is a number (not "123abc")
  return isNaN(n) ? NaN : n;
}

function getActiveFilters() {
  const filters = [];
  document.querySelectorAll(".filter-row").forEach(row => {
    const col = row.querySelector(".filter-col")?.value;
    const op = row.querySelector(".filter-op")?.value;
    const val = row.querySelector(".filter-val")?.value ?? "";
    if (col && val !== "") filters.push({ col, op, val });
  });
  return filters;
}

function matchesFilter(row, mrow, filter) {
  const cellText = toText(row[filter.col]);
  const maskedText = mrow ? toText(mrow[filter.col]) : "";
  const valLower = filter.val.toLowerCase();

  switch (filter.op) {
    case "содержит":
      return cellText.toLowerCase().includes(valLower) ||
             maskedText.toLowerCase().includes(valLower);
    case "не содержит":
      return !cellText.toLowerCase().includes(valLower) &&
             !maskedText.toLowerCase().includes(valLower);
    case "=": {
      const nCell = parseNumericValue(cellText);
      const nVal = parseNumericValue(filter.val);
      if (!isNaN(nCell) && !isNaN(nVal)) return nCell === nVal;
      return cellText.toLowerCase() === valLower || maskedText.toLowerCase() === valLower;
    }
    case "!=": {
      const nCell = parseNumericValue(cellText);
      const nVal = parseNumericValue(filter.val);
      if (!isNaN(nCell) && !isNaN(nVal)) return nCell !== nVal;
      return cellText.toLowerCase() !== valLower && maskedText.toLowerCase() !== valLower;
    }
    case ">": case ">=": case "<": case "<=": {
      const nCell = parseNumericValue(cellText);
      const nVal = parseNumericValue(filter.val);
      if (isNaN(nCell) || isNaN(nVal)) return false;
      if (filter.op === ">") return nCell > nVal;
      if (filter.op === ">=") return nCell >= nVal;
      if (filter.op === "<") return nCell < nVal;
      return nCell <= nVal;
    }
    default: return true;
  }
}

function applyResultView() {
  cellAnchor = null; cellRange = null; cellDragging = false;
  const filterText = (document.getElementById("result-filter").value || "").trim().toLowerCase();
  const columnFilters = getActiveFilters();

  // Build filtered index array (keeps original indices for masked row lookup)
  let indices = resultRows.map((_, i) => i);

  // Quick text search across all fields
  if (filterText) {
    indices = indices.filter(i => {
      const row = resultRows[i];
      const mrow = maskedRows[i];
      return resultHeaders.some(h => {
        if (toText(row[h]).toLowerCase().includes(filterText)) return true;
        if (mrow && toText(mrow[h]).toLowerCase().includes(filterText)) return true;
        return false;
      });
    });
  }

  // Column-specific filters (AND logic)
  if (columnFilters.length) {
    indices = indices.filter(i => {
      const row = resultRows[i];
      const mrow = maskedRows[i];
      return columnFilters.every(f => matchesFilter(row, mrow, f));
    });
  }

  if (sortColumn !== null) {
    indices.sort((a, b) => {
      const va = sortKey(resultRows[a][sortColumn]), vb = sortKey(resultRows[b][sortColumn]);
      let cmp = va[0] !== vb[0] ? va[0] - vb[0] : va[1] < vb[1] ? -1 : va[1] > vb[1] ? 1 : 0;
      return sortReverse ? -cmp : cmp;
    });
  }

  // Store for virtual scroll renderer
  filteredIndices = indices;
  filteredRows = indices.map(i => resultRows[i]);
  filteredMaskedRows = indices.map(i => maskedRows[i] || null);

  const table = document.getElementById("result-table");
  const thead = table.querySelector("thead");
  const ph = document.getElementById("result-placeholder");
  const wrap = document.getElementById("result-table-wrap");
  const rc = document.getElementById("row-count");

  if (!resultRows.length) {
    wrap.style.display = "none";
    ph.style.display = ""; ph.textContent = "Результат появится здесь после выполнения запроса";
    ph.className = "result-placeholder";
    rc.textContent = "";
    return;
  }

  // Single column set: agent view as primary, original in tooltip for masked columns
  const hasMasked = maskedRows.length > 0 && maskedColumns.length > 0;
  displayCols = [];  // {header, isMasked, srcHeader, isOriginal}
  resultHeaders.forEach(h => {
    const masked = hasMasked && maskedColumns.includes(h);
    displayCols.push({ header: h, isMasked: masked, srcHeader: h, isOriginal: false });
    if (masked && showOriginals) {
      displayCols.push({ header: h + " ↩", isMasked: false, srcHeader: h, isOriginal: true });
    }
  });

  // Show/hide originals toggle button
  const togBtn = document.getElementById("toggle-originals-btn");
  if (togBtn) {
    togBtn.style.display = hasMasked ? "" : "none";
    togBtn.style.background = showOriginals ? "var(--accent)" : "";
    togBtn.style.color = showOriginals ? "#fff" : "";
  }

  thead.innerHTML = "";
  const headerRow = document.createElement("tr");
  displayCols.forEach((col, ci) => {
    const th = document.createElement("th");
    th.dataset.c = String(ci);
    th.dataset.srcHeader = col.srcHeader;
    if (col.isOriginal) {
      th.classList.add("col-original");
    } else if (col.isMasked) {
      th.classList.add("col-masked");
    } else if (hasMasked) {
      th.classList.add("col-open");
    }

    if (col.isMasked && !col.isOriginal) {
      const inner = document.createElement("div");
      inner.className = "th-inner";

      const actionBtn = document.createElement("button");
      actionBtn.type = "button";
      actionBtn.className = "th-title-action";
      actionBtn.title = "Добавить поле в белый список";
      actionBtn.onclick = (e) => {
        e.stopPropagation();
        allowField(col.srcHeader);
      };

      const icon = document.createElement("span");
      icon.className = "th-lock-icon";
      icon.textContent = "🔒";

      const label = document.createElement("span");
      label.className = "th-label-text";
      label.textContent = col.header;

      actionBtn.appendChild(icon);
      actionBtn.appendChild(label);
      inner.appendChild(actionBtn);

      const sortBtn = document.createElement("button");
      sortBtn.type = "button";
      sortBtn.className = "th-sort-btn";
      sortBtn.title = "Сортировать по столбцу";
      sortBtn.textContent = col.header === sortColumn ? (sortReverse ? "↓" : "↑") : "↕";
      sortBtn.onclick = (e) => {
        e.stopPropagation();
        toggleSort(col.header);
      };
      inner.appendChild(sortBtn);

      th.appendChild(inner);
    } else {
      th.textContent = col.header;
      if (!col.isOriginal && col.header === sortColumn) {
        const arrow = document.createElement("span");
        arrow.className = "sort-arrow";
        arrow.textContent = sortReverse ? " \u2193" : " \u2191";
        th.appendChild(arrow);
      }
      if (!col.isOriginal) th.onclick = () => toggleSort(col.header);
    }
    headerRow.appendChild(th);
  });
  thead.appendChild(headerRow);

  if (!filteredRows.length) {
    wrap.style.display = "none";
    ph.style.display = ""; ph.textContent = "По фильтру ничего не найдено";
    ph.className = "result-placeholder";
    rc.textContent = "0 из " + resultRows.length;
    return;
  }

  ph.style.display = "none";
  wrap.style.display = "";
  rc.textContent = filteredRows.length === resultRows.length ? filteredRows.length + " строк" : filteredRows.length + " из " + resultRows.length;

  // Initialize virtual scroll
  vsRenderStart = -1; // force re-render
  wrap.scrollTop = 0;
  vsScrollTop = 0;
  renderVisibleRows();
  applyTableFontSize();
}

// ─── Virtual Scroll ──────────────────────────────────────────────────

function renderVisibleRows() {
  const wrap = document.getElementById("result-table-wrap");
  const tbody = document.querySelector("#result-table tbody");
  if (!tbody || !filteredRows.length || !displayCols.length) return;

  const totalRows = filteredRows.length;
  const scrollTop = wrap.scrollTop;
  const wrapHeight = wrap.clientHeight || 400; // fallback if not yet laid out

  // Account for thead height in scroll calculations
  const thead = document.querySelector("#result-table thead");
  const theadH = thead ? thead.offsetHeight : 0;

  // Calculate visible range
  const effectiveScroll = Math.max(0, scrollTop - theadH);
  const startIdx = Math.max(0, Math.floor(effectiveScroll / vsRowHeight) - 5);
  const endIdx = Math.min(totalRows, Math.ceil((effectiveScroll + wrapHeight) / vsRowHeight) + 5);

  // Skip re-render if same range (but always render if forced via vsRenderStart === -1)
  if (vsRenderStart >= 0 && startIdx === vsRenderStart && endIdx === vsRenderEnd) return;
  vsRenderStart = startIdx;
  vsRenderEnd = endIdx;

  const hasMasked = maskedRows.length > 0 && maskedColumns.length > 0;
  const fragment = document.createDocumentFragment();

  // Top spacer row
  if (startIdx > 0) {
    const topSpacer = document.createElement("tr");
    topSpacer.className = "vs-spacer";
    const topTd = document.createElement("td");
    topTd.colSpan = displayCols.length;
    topTd.style.height = (startIdx * vsRowHeight) + "px";
    topTd.style.padding = "0";
    topTd.style.border = "none";
    topSpacer.appendChild(topTd);
    fragment.appendChild(topSpacer);
  }

  // Visible rows
  for (let ri = startIdx; ri < endIdx; ri++) {
    const row = filteredRows[ri];
    if (!row) continue;
    const maskedRow = hasMasked ? filteredMaskedRows[ri] : null;
    const tr = document.createElement("tr");
    for (let ci = 0; ci < displayCols.length; ci++) {
      const col = displayCols[ci];
      const td = document.createElement("td");
      let v;
      if (col.isOriginal) {
        // Original (unmasked) value column
        v = toText(row[col.srcHeader]);
        td.title = v;
        td.classList.add("cell-original");
      } else if (col.isMasked && maskedRow) {
        // Show masked (agent) value; tooltip shows original
        v = toText(maskedRow[col.srcHeader]);
        const orig = toText(row[col.srcHeader]);
        td.title = orig;
        td.classList.add("cell-masked-value");
      } else {
        v = toText(row[col.srcHeader]);
        td.title = v;
      }
      td.textContent = v;
      td.dataset.r = ri;
      td.dataset.c = ci;
      td.onmousedown = (e) => cellMouseDown(e, ri, ci);
      td.onmouseover = (e) => { if (e.buttons === 1 && cellDragging) cellDragTo(ri, ci); };
      tr.appendChild(td);
    }
    fragment.appendChild(tr);
  }

  // Bottom spacer row
  const bottomH = (totalRows - endIdx) * vsRowHeight;
  if (bottomH > 0) {
    const bottomSpacer = document.createElement("tr");
    bottomSpacer.className = "vs-spacer";
    const bottomTd = document.createElement("td");
    bottomTd.colSpan = displayCols.length;
    bottomTd.style.height = bottomH + "px";
    bottomTd.style.padding = "0";
    bottomTd.style.border = "none";
    bottomSpacer.appendChild(bottomTd);
    fragment.appendChild(bottomSpacer);
  }

  tbody.innerHTML = "";
  tbody.appendChild(fragment);

  // Restore cell selection highlight after DOM rebuild
  if (cellRange) paintCellSelection();
}

// Attach scroll listener once
(function() {
  const wrap = document.getElementById("result-table-wrap");
  if (wrap) {
    wrap.addEventListener("scroll", () => {
      if (!vsTicking) {
        vsTicking = true;
        requestAnimationFrame(() => {
          renderVisibleRows();
          vsTicking = false;
        });
      }
    }, { passive: true });
  }
})();

// renderMaskedTable removed — masked data merged into main table

// ─── Virtual text rendering for Bundle tab ───────────────────────────
function showPlaceholder(text, isError) {
  const ph = document.getElementById("result-placeholder");
  const wrap = document.getElementById("result-table-wrap");
  ph.textContent = text;
  ph.className = "result-placeholder" + (isError ? " error" : "");
  ph.style.display = "";
  wrap.style.display = "none";
}

function toggleSort(col) {
  if (sortColumn === col) sortReverse = !sortReverse;
  else { sortColumn = col; sortReverse = false; }
  applyResultView();
}

function toggleOriginals() {
  showOriginals = !showOriginals;
  applyResultView();
}

function sortKey(val) {
  if (val == null) return [2, ""];
  const s = String(val).replace(/\u00A0/g, "").replace(/ /g, "").replace(",", ".");
  const n = parseFloat(s);
  if (!isNaN(n)) return [0, n];
  return [1, String(val).toLowerCase()];
}

// ─── Cell selection (Excel-like) ─────────────────────────────────
let cellAnchor = null;   // {r, c}
let cellRange = null;    // {r1, c1, r2, c2}
let cellDragging = false;

function cellMouseDown(e, r, c) {
  e.preventDefault();
  // Blur any focused input so Ctrl+C works on table
  if (document.activeElement && document.activeElement.tagName === "INPUT") document.activeElement.blur();
  if (e.shiftKey && cellAnchor) {
    cellRange = makeRange(cellAnchor.r, cellAnchor.c, r, c);
    cellDragging = true;
  } else {
    cellAnchor = { r, c };
    cellRange = makeRange(r, c, r, c);
    cellDragging = true;
  }
  paintCellSelection();
}

function cellDragTo(r, c) {
  if (!cellAnchor) return;
  cellRange = makeRange(cellAnchor.r, cellAnchor.c, r, c);
  paintCellSelection();
}

document.addEventListener("mouseup", () => { cellDragging = false; });

function makeRange(r1, c1, r2, c2) {
  return { r1: Math.min(r1, r2), c1: Math.min(c1, c2), r2: Math.max(r1, r2), c2: Math.max(c1, c2) };
}

function paintCellSelection() {
  const tbody = document.querySelector("#result-table tbody");
  if (!tbody) return;
  tbody.querySelectorAll("td.cell-selected, td.cell-anchor").forEach(td => {
    td.classList.remove("cell-selected", "cell-anchor");
  });
  if (!cellRange) return;
  // Virtual scroll: rows in DOM have dataset.r with logical row index
  const tds = tbody.querySelectorAll("td");
  tds.forEach(td => {
    const ri = parseInt(td.dataset.r);
    const ci = parseInt(td.dataset.c);
    if (isNaN(ri) || isNaN(ci)) return;
    if (ri >= cellRange.r1 && ri <= cellRange.r2 && ci >= cellRange.c1 && ci <= cellRange.c2) {
      td.classList.add("cell-selected");
    }
    if (cellAnchor && ri === cellAnchor.r && ci === cellAnchor.c) {
      td.classList.add("cell-anchor");
    }
  });
}

// ─── Context menu on table ─────────────────────────────────────────
(function() {
  const menu = document.getElementById("table-ctx-menu");
  let ctxCol = null, ctxRow = null, ctxValue = "", ctxHeader = null;

  document.getElementById("result-table-wrap").addEventListener("contextmenu", function(e) {
    const td = e.target.closest("td");
    const th = e.target.closest("th");
    if (!td && !th) return;
    e.preventDefault();
    if (td) {
      ctxCol = parseInt(td.dataset.c);
      ctxRow = parseInt(td.dataset.r);
      ctxValue = td.textContent || "";
      ctxHeader = displayCols[ctxCol] ? displayCols[ctxCol].srcHeader : null;
      menu.querySelector('[data-action="copy-value"]').style.display = "";
      menu.querySelector('[data-action="filter-value"]').style.display = "";
    } else {
      ctxCol = parseInt(th.dataset.c);
      ctxRow = null;
      ctxValue = "";
      ctxHeader = th.dataset.srcHeader || null;
      menu.querySelector('[data-action="copy-value"]').style.display = "none";
      menu.querySelector('[data-action="filter-value"]').style.display = "none";
    }

    // Position menu
    const x = Math.min(e.clientX, window.innerWidth - 200);
    const y = Math.min(e.clientY, window.innerHeight - 200);
    menu.style.left = x + "px";
    menu.style.top = y + "px";
    menu.style.display = "block";
  });

  menu.addEventListener("click", function(e) {
    const item = e.target.closest(".ctx-item");
    if (!item) return;
    const action = item.dataset.action;
    menu.style.display = "none";

    if (action === "copy-value") {
      navigator.clipboard.writeText(ctxValue).then(() => toast("Скопировано"));
    } else if (action === "copy-column") {
      const hdr = ctxHeader;
      if (!hdr) return;
      const rows = filteredRows.length ? filteredRows : resultRows;
      const vals = rows.map(r => toText(r[hdr]));
      navigator.clipboard.writeText(vals.join("\n")).then(() => toast("Столбец скопирован (" + vals.length + " значений)"));
    } else if (action === "filter-value") {
      const input = document.getElementById("result-filter");
      if (input) { input.value = ctxValue; onSearchInput(); }
    } else if (action === "allow-column" || action === "mask-column") {
      const hdr = ctxHeader;
      if (!hdr) return;
      if (action === "allow-column" && typeof allowField === "function") {
        allowField(hdr);
      } else if (action === "mask-column" && typeof reMaskField === "function") {
        reMaskField(hdr);
      }
    }
  });

  // Close on click outside or Escape
  document.addEventListener("click", function(e) {
    if (!menu.contains(e.target)) menu.style.display = "none";
  });
  document.addEventListener("keydown", function(e) {
    if (e.key === "Escape") menu.style.display = "none";
  });
})();

// Ctrl+C / Cmd+C on table (e.code works regardless of keyboard layout)
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && (e.code === "KeyC" || e.key === "c" || e.key === "с")) {
    const active = document.activeElement;
    if (active && (active.tagName === "INPUT" || active.tagName === "TEXTAREA" || active.tagName === "SELECT")) return;
    // Only act if result tab is active and we have data
    const resultTab = document.getElementById("tab-result");
    if (!resultTab || !resultTab.classList.contains("active") || !filteredRows.length) return;
    e.preventDefault();
    if (cellRange) {
      copyCellSelection();
    } else {
      copyAllRows();
    }
  }
});

function onSearchInput() {
  const v = document.getElementById("result-filter").value;
  document.getElementById("search-clear").style.display = v ? "block" : "none";
  applyResultView();
}

function clearSearch() {
  document.getElementById("result-filter").value = "";
  document.getElementById("search-clear").style.display = "none";
  applyResultView();
}

function clearFilter() {
  document.getElementById("result-filter").value = "";
  document.getElementById("search-clear").style.display = "none";
  document.getElementById("filter-rows").innerHTML = "";
  applyResultView();
}

function toText(v) { return v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v); }

// ─── Clipboard ───────────────────────────────────────────────────────

function copyCellSelection() {
  if (!cellRange) { toast("Нет выделения"); return; }
  const hasMasked = maskedRows.length > 0 && maskedColumns.length > 0;
  const lines = [];
  for (let ri = cellRange.r1; ri <= cellRange.r2 && ri < filteredRows.length; ri++) {
    const row = filteredRows[ri];
    const maskedRow = hasMasked ? filteredMaskedRows[ri] : null;
    const vals = [];
    for (let ci = cellRange.c1; ci <= cellRange.c2 && ci < displayCols.length; ci++) {
      const col = displayCols[ci];
      if (col.isMasked && maskedRow) {
        vals.push(toText(maskedRow[col.srcHeader]));
      } else {
        vals.push(toText(row[col.srcHeader]));
      }
    }
    lines.push(vals.join("\t"));
  }
  const total = (cellRange.r2 - cellRange.r1 + 1) * (cellRange.c2 - cellRange.c1 + 1);
  navigator.clipboard.writeText(lines.join("\n")).then(() =>
    toast("Скопировано " + (total === 1 ? "1 ячейка" : total + " ячеек"))
  );
}

function copyAllRows() {
  const rows = filteredRows.length ? filteredRows : resultRows;
  if (!rows.length) { toast("Нет данных"); return; }
  const lines = [resultHeaders.join("\t")];
  rows.forEach(row => lines.push(resultHeaders.map(h => toText(row[h])).join("\t")));
  const suffix = filteredRows.length && filteredRows.length !== resultRows.length
    ? " строк (отфильтровано)" : " строк";
  navigator.clipboard.writeText(lines.join("\n")).then(() => toast("Скопировано " + rows.length + suffix));
}

function exportCSV() {
  const rows = filteredRows.length ? filteredRows : resultRows;
  if (!rows.length) { toast("Нет данных для экспорта"); return; }
  const escape = v => {
    const s = String(v == null ? "" : v);
    return s.indexOf(",") >= 0 || s.indexOf('"') >= 0 || s.indexOf("\n") >= 0
      ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  const lines = [resultHeaders.map(escape).join(",")];
  rows.forEach(row => lines.push(resultHeaders.map(h => escape(toText(row[h]))).join(",")));
  const csv = "\uFEFF" + lines.join("\r\n"); // BOM for Excel
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  const now = new Date();
  const ts = now.toISOString().slice(0,10);
  a.href = url;
  a.download = "gateway-export-" + ts + ".csv";
  a.click();
  URL.revokeObjectURL(url);
  toast("CSV экспортирован (" + rows.length + " строк)");
}

function renderMarkdownPanel(id, text) {
  const el = document.getElementById(id);
  if (!el) return;
  el.dataset.rawText = text;
  if (text && typeof marked !== "undefined") {
    el.innerHTML = marked.parse(text);
    el.classList.add("md-rendered");
  } else {
    el.textContent = text;
    el.classList.remove("md-rendered");
  }
}

function nerExportTemplate() {
  api("/api/ner/export_template", {}).then(r => {
    if (r.ok) toast("Шаблон создан: " + r.path);
    else toast("Ошибка: " + (r.error || "unknown"));
  });
}

function nerReload() {
  api("/api/ner/reload", {}).then(r => {
    if (r.ok) {
      toast("Правила загружены");
      const el = document.getElementById("ner-status");
      if (el && r.status) el.textContent = r.status;
    } else {
      toast("Ошибка: " + (r.error || "unknown"));
    }
  });
}

function copyAnalysis(which) {
  const el = document.getElementById(which === "masked" ? "analysis-masked" : "analysis-display");
  const t = el ? (el.dataset.rawText || el.textContent) : "";
  if (!t) { toast("Нет данных для копирования"); return; }
  navigator.clipboard.writeText(t).then(() => toast("Анализ скопирован"));
}

async function loadBridgeSecret() {
  const el = document.getElementById("bridge-secret-text");
  if (el.textContent && el.textContent !== "—") return;
  try {
    const r = await api("/api/bridge_secret");
    if (r.bridge_secret) el.textContent = r.bridge_secret;
  } catch (e) { /* ignore */ }
}

function copyBridgeSecret() {
  const t = document.getElementById("bridge-secret-text").textContent;
  if (!t || t === "—") { loadBridgeSecret(); return; }
  navigator.clipboard.writeText(t).then(() => toast("Bridge Secret скопирован"));
}
