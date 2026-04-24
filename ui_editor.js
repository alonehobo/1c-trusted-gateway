(function() {
  // ─── 1C Query Language mode for Ace (direct require) ─────────────
  var oop = ace.require("ace/lib/oop");
  var TextMode = ace.require("ace/mode/text").Mode;
  var TextHighlightRules = ace.require("ace/mode/text_highlight_rules").TextHighlightRules;

  // ─── Словарь из SDBLLexer.g4 (1c-syntax/bsl-parser) ─────────────
  var keywords = (
    // Основные
    "ВЫБРАТЬ|РАЗРЕШЕННЫЕ|РАЗЛИЧНЫЕ|ПЕРВЫЕ|" +
    "ИЗ|КАК|ГДЕ|И|ИЛИ|НЕ|В|МЕЖДУ|ПОДОБНО|СПЕЦСИМВОЛ|ЕСТЬ|NULL|ИСТИНА|ЛОЖЬ|" +
    // Соединения
    "ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ|ВНЕШНЕЕ|СОЕДИНЕНИЕ|ПО|НА|" +
    // Группировка, сортировка
    "СГРУППИРОВАТЬ|ГРУППИРУЮЩИМ|НАБОРАМ|УПОРЯДОЧИТЬ|ВОЗР|УБЫВ|" +
    "ИМЕЮЩИЕ|ОБЪЕДИНИТЬ|ВСЕ|" +
    // Условия
    "КОГДА|ТОГДА|КОНЕЦ|ИНАЧЕ|ВЫБОР|" +
    // Типы и ссылки
    "ЗНАЧЕНИЕ|ТИП|ТИПЗНАЧЕНИЯ|ВЫРАЗИТЬ|" +
    // Временные таблицы
    "ПОМЕСТИТЬ|ТАБЛИЦА|УНИЧТОЖИТЬ|ИНДЕКСИРОВАТЬ|ПУСТАЯТАБЛИЦА|" +
    // Блокировка, иерархия, итоги
    "ДЛЯ|ИЗМЕНЕНИЯ|ИЕРАРХИЯ|ИЕРАРХИИ|ИТОГИ|ОБЩИЕ|ТОЛЬКО|ПЕРИОДАМИ|" +
    "АВТОУПОРЯДОЧИВАНИЕ|НЕОПРЕДЕЛЕНО|" +
    // Уникальность, группировка
    "УНИКАЛЬНО|СГРУППИРОВАНОПО|АВТОНОМЕРЗАПИСИ|" +
    // English — основные
    "SELECT|ALLOWED|DISTINCT|TOP|" +
    "FROM|AS|WHERE|AND|OR|NOT|IN|BETWEEN|LIKE|ESCAPE|IS|TRUE|FALSE|" +
    "LEFT|RIGHT|INNER|FULL|OUTER|JOIN|ON|BY|OF|" +
    "GROUP|GROUPING|SETS|ORDER|ASC|DESC|" +
    "HAVING|UNION|ALL|" +
    "WHEN|THEN|END|ELSE|CASE|" +
    "VALUE|TYPE|VALUETYPE|CAST|" +
    "INTO|TABLE|DROP|INDEX|FOR|UPDATE|HIERARCHY|TOTALS|AUTOORDER|" +
    "EMPTYTABLE|ONLY|PERIODS|UNDEFINED|" +
    "UNIQUE|GROUPEDBY|RECORDAUTONUMBER"
  );
  var builtinFunctions = (
    // Даты
    "ДАТАВРЕМЯ|ГОД|КВАРТАЛ|МЕСЯЦ|ДЕНЬГОДА|ДЕНЬ|НЕДЕЛЯ|ДЕНЬНЕДЕЛИ|ЧАС|МИНУТА|СЕКУНДА|" +
    "ПОЛУГОДИЕ|ДЕКАДА|" +
    "НАЧАЛОПЕРИОДА|КОНЕЦПЕРИОДА|ДОБАВИТЬКДАТЕ|РАЗНОСТЬДАТ|" +
    // Строки
    "ПОДСТРОКА|ДЛИНАСТРОКИ|СТРОКА|СОКРЛ|СОКРП|СОКРЛП|ВРЕГ|НРЕГ|СТРЗАМЕНИТЬ|СТРНАЙТИ|" +
    // Проверки и представления
    "ЕСТЬNULL|ПРЕДСТАВЛЕНИЕ|ПРЕДСТАВЛЕНИЕССЫЛКИ|" +
    // Агрегатные
    "КОЛИЧЕСТВО|СУММА|МИНИМУМ|МАКСИМУМ|СРЕДНЕЕ|" +
    // Математические
    "ЦЕЛ|ОКР|" +
    // Типы данных
    "ЧИСЛО|ДАТА|БУЛЕВО|" +
    // English — даты
    "DATETIME|YEAR|QUARTER|MONTH|DAYOFYEAR|DAY|WEEK|WEEKDAY|HOUR|MINUTE|SECOND|" +
    "HALFYEAR|TENDAYS|" +
    "BEGINOFPERIOD|ENDOFPERIOD|DATEADD|DATEDIFF|" +
    // English — строки
    "SUBSTRING|STRINGLENGTH|STRING|TRIML|TRIMR|TRIMALL|UPPER|LOWER|STRREPLACE|STRFIND|" +
    // English — проверки
    "ISNULL|PRESENTATION|REFPRESENTATION|" +
    // English — агрегатные
    "COUNT|SUM|MIN|MAX|AVG|" +
    // English — математические
    "INT|ROUND|ACOS|ASIN|ATAN|COS|SIN|TAN|EXP|LOG|LOG10|POW|SQRT|" +
    // English — типы
    "NUMBER|DATE|BOOLEAN"
  );
  var types = (
    "Число|Строка|Дата|Булево|Неопределено|" +
    "Number|String|Date|Boolean|Undefined"
  );

  var OneCRules = function() {
    this.$rules = {
      "start": [
        { token: "comment.line", regex: "//.*$" },
        { token: "string", regex: '"', next: "string" },
        { token: "constant.numeric", regex: "\\d+(?:\\.\\d+)?" },
        { token: "keyword", regex: "(?:" + keywords + ")(?![a-zA-Zа-яА-ЯёЁ0-9_])", caseInsensitive: true },
        { token: "support.function", regex: "(?:" + builtinFunctions + ")(?=\\s*\\()", caseInsensitive: true },
        { token: "storage.type", regex: "(?:" + types + ")(?![a-zA-Zа-яА-ЯёЁ0-9_])", caseInsensitive: true },
        { token: "variable.parameter", regex: "&[a-zA-Zа-яА-ЯёЁ_][a-zA-Zа-яА-ЯёЁ0-9_]*" },
        { token: "keyword.operator", regex: "<>|<=|>=|[=<>+\\-*/]" },
        { token: "paren.lparen", regex: "\\(" },
        { token: "paren.rparen", regex: "\\)" },
        { token: "punctuation.operator", regex: "[,;.]" },
        { token: "identifier", regex: "[a-zA-Zа-яА-ЯёЁ_][a-zA-Zа-яА-ЯёЁ0-9_]*" },
        { token: "text", regex: "\\s+" }
      ],
      "string": [
        { token: "string", regex: '""' },
        { token: "string", regex: '"', next: "start" },
        { token: "constant.character.escape", regex: "\\|" },
        { defaultToken: "string" }
      ]
    };
  };
  oop.inherits(OneCRules, TextHighlightRules);

  var OneCMode = function() {
    this.HighlightRules = OneCRules;
    this.$behaviour = this.$defaultBehaviour;
  };
  oop.inherits(OneCMode, TextMode);

  // ─── Editor init ──────────────────────────────────────────────────
  var editor = ace.edit("query-editor");
  editor.setTheme("ace/theme/one_dark");
  editor.session.setMode(new OneCMode());
  editor.setOptions({
    fontSize: "13px",
    fontFamily: "'Cascadia Code', 'JetBrains Mono', 'Fira Code', Consolas, 'Courier New', monospace",
    showPrintMargin: false,
    wrap: true,
    tabSize: 4,
    useSoftTabs: false,
    enableBasicAutocompletion: true,
    enableLiveAutocompletion: true,
    enableSnippets: false
  });
  editor.renderer.setScrollMargin(4, 4);

  // ─── Autocomplete: 1C Query Language completer ─────────────────────
  (function() {
    var langTools = ace.require("ace/ext/language_tools");

    // Build word list from existing keyword/function/type definitions
    var kwList = keywords.split("|").map(function(w) {
      return { caption: w, value: w, score: 1000, meta: "ключевое слово" };
    });
    var fnList = builtinFunctions.split("|").map(function(w) {
      return { caption: w, value: w + "(", score: 900, meta: "функция" };
    });
    var typeList = types.split("|").map(function(w) {
      return { caption: w, value: w, score: 800, meta: "тип" };
    });

    // Common table prefixes from 1C metadata
    var metaTables = [
      "Справочник", "Catalog",
      "Документ", "Document",
      "РегистрНакопления", "AccumulationRegister",
      "РегистрСведений", "InformationRegister",
      "РегистрБухгалтерии", "AccountingRegister",
      "РегистрРасчета", "CalculationRegister",
      "ПланСчетов", "ChartOfAccounts",
      "ПланВидовРасчета", "ChartOfCalculationTypes",
      "ПланВидовХарактеристик", "ChartOfCharacteristicTypes",
      "ПланОбмена", "ExchangePlan",
      "Перечисление", "Enum",
      "БизнесПроцесс", "BusinessProcess",
      "Задача", "Task",
      "Последовательность", "Sequence",
      "КритерийОтбора", "FilterCriterion",
      "ЖурналДокументов", "DocumentJournal",
      "Константа", "Constant"
    ];
    var metaList = metaTables.map(function(w) {
      return { caption: w, value: w + ".", score: 700, meta: "метаданные" };
    });

    var allWords = kwList.concat(fnList).concat(typeList).concat(metaList);

    var oneCCompleter = {
      getCompletions: function(editor, session, pos, prefix, callback) {
        if (!prefix || prefix.length < 1) { callback(null, []); return; }
        var lowerPrefix = prefix.toLowerCase();
        var filtered = allWords.filter(function(item) {
          return item.caption.toLowerCase().indexOf(lowerPrefix) === 0;
        });
        callback(null, filtered);
      }
    };

    langTools.setCompleters([oneCCompleter]);
  })();

  window.queryEditorFocused = false;
  const headerTokenEl = document.getElementById("token");
  if (headerTokenEl) {
    headerTokenEl.addEventListener("beforeinput", function() {
      if (headerTokenEl.dataset.savedTokenPlaceholder === "1") {
        delete headerTokenEl.dataset.savedTokenPlaceholder;
        headerTokenEl.value = "";
      }
    });
    headerTokenEl.addEventListener("input", function() { headerTokenTouched = true; });
  }
  editor.on("focus", function() { window.queryEditorFocused = true; });
  editor.on("blur", function() { window.queryEditorFocused = false; });

  // ─── Hotkeys (Ace) ─────────────────────────────────────────────────
  editor.commands.addCommand({
    name: "executeQuery",
    bindKey: { win: "Ctrl-Enter", mac: "Cmd-Enter" },
    exec: function() {
      var approveBtn = document.getElementById("btn-approve-code");
      if (approveBtn && approveBtn.style.display !== "none") {
        doApproveCode();
      } else {
        doQuery("masked");
      }
    }
  });

  // ─── Hotkeys (global) ─────────────────────────────────────────────
  document.addEventListener("keydown", function(e) {
    // Escape — cancel query
    if (e.key === "Escape") {
      var cancelBtn = document.getElementById("cancel-btn");
      if (cancelBtn && cancelBtn.style.display !== "none") {
        doCancelQuery();
        e.preventDefault();
      }
    }
    // Ctrl+S — save settings (when Settings tab is active)
    if ((e.ctrlKey || e.metaKey) && e.key === "s") {
      var settingsPanel = document.getElementById("tab-settings");
      if (settingsPanel && settingsPanel.classList.contains("active")) {
        e.preventDefault();
        saveSettings();
      }
    }
  });

  // Unified API for the rest of the app
  window.queryEditor = {
    state: { doc: { toString: function() { return editor.getValue(); } } },
    dispatch: function(tr) {
      if (tr && tr.changes) editor.setValue(tr.changes.insert || "", -1);
    },
    ace: editor
  };
})();