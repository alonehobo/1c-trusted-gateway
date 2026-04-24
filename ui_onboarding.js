// ─── Guided Tour ────────────────────────────────────────────────────

const TOUR_STEPS = [
  {
    target: "#header",
    title: "Панель подключения",
    text: "Здесь вы указываете URL сервера шлюза и ключ доступа. Нажмите <b>Подключить</b>, чтобы установить соединение с 1С. Индикатор справа покажет статус: зеленый — подключено, красный — ошибка.",
    position: "bottom"
  },
  {
    target: "#header-send-mode",
    title: "Режим отправки данных",
    text: "<b>Авто</b> — результаты запросов автоматически отправляются AI-агенту.<br><b>Ручной</b> — вы сначала проверяете данные и отправляете сами. Ручной режим безопаснее при работе с конфиденциальными данными.",
    position: "bottom"
  },
  {
    target: "#toolbar",
    title: "Навигация по вкладкам",
    text: "<b>MCP</b> — редактор запросов и сырой ответ.<br><b>Результат</b> — таблица с данными, фильтры, поиск.<br><b>Анализ</b> — текстовый анализ от AI-агента.<br><b>Настройки</b> — параметры подключения и маскирования.",
    position: "bottom"
  },
  {
    target: "#query-editor",
    title: "Редактор запросов",
    text: "Введите запрос на языке 1С или BSL-код для выполнения. Редактор поддерживает подсветку синтаксиса и автодополнение.<br>Горячая клавиша: <kbd>Ctrl+Enter</kbd> — выполнить запрос.",
    position: "right",
    before: function() { showTab("raw"); }
  },
  {
    target: "#btn-query-masked",
    title: "Выполнение запроса",
    text: "Нажмите для отправки запроса. Данные автоматически проходят через маскирование — персональные данные (ФИО, ИНН, телефоны) заменяются псевдонимами.",
    position: "bottom",
    before: function() { showTab("raw"); }
  },
  {
    target: "#mask-bar",
    title: "Управление маскированием",
    text: "Здесь перечислены поля, которые <b>не маскируются</b> (белый список). Добавляйте поля вроде Статус, ВидДвижения, Проведен — они нужны для анализа, но не содержат персональных данных.",
    position: "bottom"
  },
  {
    target: "#tab-result",
    title: "Таблица результатов",
    text: "Результат запроса отображается в виде таблицы. Столбцы с цветовой индикацией: <b>открытые</b> (не маскированы), <b>зашифрованные</b> (маскированы). Используйте поиск и фильтры для анализа данных.",
    position: "top",
    before: function() { showTab("result"); }
  },
  {
    target: "#tab-analysis",
    title: "Анализ данных",
    text: "Текстовый анализ от AI-агента отображается здесь в формате Markdown. Замаскированные псевдонимы будут автоматически расшифрованы шлюзом для вашего удобства.",
    position: "top",
    before: function() { showTab("analysis"); }
  },
  {
    target: "#tab-settings",
    title: "Настройки",
    text: "Параметры подключения, управление полями маскирования, экспорт/импорт конфигурации. Здесь же можно настроить NER-правила для автоматического обнаружения персональных данных.",
    position: "top",
    before: function() { showTab("settings"); }
  }
];

let tourCurrentStep = -1;
let tourActive = false;

function tourStart() {
  tourCurrentStep = 0;
  tourActive = true;
  document.getElementById("tour-overlay").style.display = "";
  document.getElementById("tour-spotlight").style.display = "";
  document.getElementById("tour-tooltip").style.display = "";
  document.getElementById("tour-overlay").onclick = function(e) {
    if (e.target === this) tourStop();
  };
  tourRender();
  try { localStorage.removeItem("tour_done"); } catch(e) {}
}

function tourStop() {
  tourActive = false;
  tourCurrentStep = -1;
  document.getElementById("tour-overlay").style.display = "none";
  document.getElementById("tour-spotlight").style.display = "none";
  document.getElementById("tour-tooltip").style.display = "none";
  try { localStorage.setItem("tour_done", "1"); } catch(e) {}
  showTab("raw");
}

function tourNext() {
  if (tourCurrentStep < TOUR_STEPS.length - 1) {
    tourCurrentStep++;
    tourRender();
  } else {
    tourStop();
    showToast("Обучение завершено!", "success");
  }
}

function tourPrev() {
  if (tourCurrentStep > 0) {
    tourCurrentStep--;
    tourRender();
  }
}

function tourRender() {
  const step = TOUR_STEPS[tourCurrentStep];
  if (!step) return;

  // Execute pre-step action (e.g. switch tab)
  if (step.before) step.before();

  // Wait a tick for DOM to update after tab switch
  requestAnimationFrame(() => {
    const targetEl = document.querySelector(step.target);
    if (!targetEl) { tourNext(); return; }

    // Scroll target into view if needed
    targetEl.scrollIntoView({ behavior: "smooth", block: "nearest" });

    requestAnimationFrame(() => {
      const rect = targetEl.getBoundingClientRect();
      const pad = 6;

      // Position spotlight
      const spotlight = document.getElementById("tour-spotlight");
      spotlight.style.top = (rect.top - pad) + "px";
      spotlight.style.left = (rect.left - pad) + "px";
      spotlight.style.width = (rect.width + pad * 2) + "px";
      spotlight.style.height = (rect.height + pad * 2) + "px";

      // Fill tooltip content
      document.getElementById("tour-title").innerHTML = step.title;
      document.getElementById("tour-text").innerHTML = step.text;

      // Progress dots
      const progressEl = document.getElementById("tour-progress");
      let dotsHtml = "";
      for (let i = 0; i < TOUR_STEPS.length; i++) {
        const cls = i < tourCurrentStep ? "done" : i === tourCurrentStep ? "active" : "";
        dotsHtml += '<div class="tour-dot ' + cls + '"></div>';
      }
      dotsHtml += '<span class="tour-counter">' + (tourCurrentStep + 1) + ' / ' + TOUR_STEPS.length + '</span>';
      progressEl.innerHTML = dotsHtml;

      // Prev/Next buttons
      document.getElementById("tour-prev-btn").style.display = tourCurrentStep === 0 ? "none" : "";
      const nextBtn = document.getElementById("tour-next-btn");
      nextBtn.textContent = tourCurrentStep === TOUR_STEPS.length - 1 ? "Готово \u2713" : "Далее \u2192";

      // Position tooltip
      tourPositionTooltip(rect, step.position || "bottom");
    });
  });
}

function tourPositionTooltip(targetRect, preferred) {
  const tooltip = document.getElementById("tour-tooltip");
  const arrow = document.getElementById("tour-arrow");
  const gap = 14;

  // Reset to measure
  tooltip.style.top = "0px";
  tooltip.style.left = "0px";
  const tRect = tooltip.getBoundingClientRect();
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  let top, left, pos = preferred;

  // Try preferred position, fall back if out of viewport
  if (pos === "bottom") {
    top = targetRect.bottom + gap;
    left = targetRect.left + targetRect.width / 2 - tRect.width / 2;
    if (top + tRect.height > vh - 10) pos = "top";
  }
  if (pos === "top") {
    top = targetRect.top - gap - tRect.height;
    left = targetRect.left + targetRect.width / 2 - tRect.width / 2;
    if (top < 10) pos = "bottom";
  }
  if (pos === "right") {
    top = targetRect.top + targetRect.height / 2 - tRect.height / 2;
    left = targetRect.right + gap;
    if (left + tRect.width > vw - 10) pos = "left";
  }
  if (pos === "left") {
    top = targetRect.top + targetRect.height / 2 - tRect.height / 2;
    left = targetRect.left - gap - tRect.width;
    if (left < 10) pos = "right";
  }

  // Final calculation based on resolved position
  switch (pos) {
    case "bottom":
      top = targetRect.bottom + gap;
      left = targetRect.left + targetRect.width / 2 - tRect.width / 2;
      break;
    case "top":
      top = targetRect.top - gap - tRect.height;
      left = targetRect.left + targetRect.width / 2 - tRect.width / 2;
      break;
    case "right":
      top = targetRect.top + targetRect.height / 2 - tRect.height / 2;
      left = targetRect.right + gap;
      break;
    case "left":
      top = targetRect.top + targetRect.height / 2 - tRect.height / 2;
      left = targetRect.left - gap - tRect.width;
      break;
  }

  // Clamp to viewport
  left = Math.max(10, Math.min(left, vw - tRect.width - 10));
  top = Math.max(10, Math.min(top, vh - tRect.height - 10));

  tooltip.style.top = top + "px";
  tooltip.style.left = left + "px";

  // Arrow
  arrow.className = "tour-arrow " + pos;

  // Position arrow relative to target center
  const arrowOffset = 24;
  if (pos === "bottom" || pos === "top") {
    const targetCenter = targetRect.left + targetRect.width / 2 - left;
    arrow.style.left = Math.max(12, Math.min(targetCenter - 6, tRect.width - 24)) + "px";
    arrow.style.top = "";
    arrow.style.right = "";
  } else {
    const targetCenter = targetRect.top + targetRect.height / 2 - top;
    arrow.style.top = Math.max(12, Math.min(targetCenter - 6, tRect.height - 24)) + "px";
    arrow.style.left = pos === "right" ? "-7px" : "";
    arrow.style.right = pos === "left" ? "-7px" : "";
  }
}

// Handle keyboard navigation during tour
document.addEventListener("keydown", function(e) {
  if (!tourActive) return;
  if (e.key === "Escape") { tourStop(); e.preventDefault(); }
  else if (e.key === "ArrowRight" || e.key === "Enter") { tourNext(); e.preventDefault(); }
  else if (e.key === "ArrowLeft") { tourPrev(); e.preventDefault(); }
});

// Reposition on resize
window.addEventListener("resize", function() {
  if (tourActive && tourCurrentStep >= 0) tourRender();
});

// ─── Onboarding ─────────────────────────────────────────────────────

let obStep = 0;

function onboardingShow() {
  const el = document.getElementById("onboarding");
  // Pre-fill from saved settings if available
  if (appState.connection && appState.connection.url) {
    document.getElementById("ob-url").value = appState.connection.url;
  }
  el.style.display = "";
  obStep = 0;
  onboardingRenderStep();
}

function onboardingSkip() {
  document.getElementById("onboarding").style.display = "none";
  try { localStorage.setItem("onboarding_done", "1"); } catch(e) {}
}

function restartOnboarding() {
  try { localStorage.removeItem("onboarding_done"); } catch(e) {}
  try { localStorage.removeItem("tour_done"); } catch(e) {}
  showTab("raw");
  onboardingShow();
}

function onboardingRenderStep() {
  for (let i = 0; i < 3; i++) {
    const dot = document.getElementById("ob-dot-" + i);
    dot.className = "onboarding-step-dot" + (i < obStep ? " done" : i === obStep ? " active" : "");
    document.getElementById("ob-step-" + i).style.display = i === obStep ? "" : "none";
  }
  const btn = document.getElementById("ob-next-btn");
  btn.textContent = obStep === 2 ? "Подключить" : "Далее";
  document.getElementById("ob-status").style.display = "none";
}

async function onboardingNext() {
  const statusEl = document.getElementById("ob-status");

  if (obStep === 0) {
    const url = document.getElementById("ob-url").value.trim();
    if (!url) {
      statusEl.textContent = "Введите URL сервера";
      statusEl.className = "onboarding-status err";
      statusEl.style.display = "";
      return;
    }
    // Copy to header field
    document.getElementById("url").value = url;
    obStep = 1;
    onboardingRenderStep();
  } else if (obStep === 1) {
    const token = document.getElementById("ob-token").value;
    if (token) document.getElementById("token").value = token;
    obStep = 2;
    onboardingRenderStep();
    // Auto-trigger connect
    await onboardingConnect();
  } else if (obStep === 2) {
    await onboardingConnect();
  }
}

async function onboardingConnect() {
  const statusEl = document.getElementById("ob-status");
  const btn = document.getElementById("ob-next-btn");
  btn.disabled = true;
  btn.textContent = "Проверяю...";
  statusEl.style.display = "none";

  try {
    await doConnect();
    // Wait a bit for SSE state update
    await new Promise(r => setTimeout(r, 500));
    if (appState.connection && appState.connection.verified) {
      statusEl.textContent = "Подключено!";
      statusEl.className = "onboarding-status ok";
      statusEl.style.display = "";
      try { localStorage.setItem("onboarding_done", "1"); } catch(e) {}
      setTimeout(() => {
        document.getElementById("onboarding").style.display = "none";
        // Offer guided tour after first successful connection
        let tourDone = false;
        try { tourDone = localStorage.getItem("tour_done") === "1"; } catch(e) {}
        if (!tourDone) {
          setTimeout(() => tourStart(), 400);
        }
      }, 800);
    } else {
      statusEl.textContent = "Не удалось подключиться. Проверьте URL и ключ.";
      statusEl.className = "onboarding-status err";
      statusEl.style.display = "";
      btn.textContent = "Повторить";
    }
  } catch(e) {
    statusEl.textContent = "Ошибка: " + e.message;
    statusEl.className = "onboarding-status err";
    statusEl.style.display = "";
    btn.textContent = "Повторить";
  }
  btn.disabled = false;
}

// ─── Init ────────────────────────────────────────────────────────────

fetchState().then(() => {
  connectSSE();
  loadBridgeSecret();

  // Show onboarding if never completed and not connected
  let obDone = false;
  try { obDone = localStorage.getItem("onboarding_done") === "1"; } catch(e) {}

  if (!obDone && !(appState.connection && appState.connection.verified)) {
    // If we have saved settings, auto-connect first, then show onboarding only if it fails
    if (appState.has_saved_settings && appState.connection && appState.connection.url) {
      doConnect().then(() => {
        setTimeout(() => {
          if (!(appState.connection && appState.connection.verified)) {
            onboardingShow();
          } else {
            try { localStorage.setItem("onboarding_done", "1"); } catch(e) {}
          }
        }, 600);
      });
    } else {
      onboardingShow();
    }
  } else if (!obDone && appState.connection && appState.connection.verified) {
    // Already connected — mark onboarding done
    try { localStorage.setItem("onboarding_done", "1"); } catch(e) {}
  } else if (appState.has_saved_settings && appState.connection && appState.connection.url && !appState.connection.verified) {
    // Onboarding done but not connected — auto-connect with saved settings
    doConnect();
  }
});
