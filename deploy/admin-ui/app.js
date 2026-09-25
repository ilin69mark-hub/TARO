const app = document.getElementById("app");
const toast = document.getElementById("toast");

const state = {
  authenticated: false,
  view: "dashboard",
  config: null,
  payments: [],
  pushStats: [],
};

const views = {
  dashboard: "Обзор",
  config: "Настройки",
  plans: "Тарифы",
  spreads: "Расклады",
  payments: "Платежи",
  push: "Push",
};

class ApiError extends Error {
  constructor(status, payload) {
    super(payload?.error?.message_ru || `Ошибка HTTP ${status}`);
    this.status = status;
    this.code = payload?.error?.code || "HTTP_ERROR";
  }
}

function el(tag, attrs = {}, children = []) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null) continue;
    if (key === "className") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "value") node.value = value;
    else if (key === "checked") node.checked = Boolean(value);
    else if (key === "disabled") node.disabled = Boolean(value);
    else if (key === "spellcheck") node.spellcheck = Boolean(value);
    else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2).toLowerCase(), value);
    else node.setAttribute(key, value);
  }
  for (const child of [].concat(children)) {
    if (child === undefined || child === null) continue;
    node.append(child);
  }
  return node;
}

function button(label, className, handler) {
  return el("button", { type: "button", className, onClick: handler }, label);
}

function showToast(message, error = false) {
  toast.textContent = message;
  toast.className = error ? "visible error" : "visible";
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => {
    toast.className = "";
  }, 3600);
}

async function request(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "include",
  });
  const text = await response.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }
  if (!response.ok) throw new ApiError(response.status, payload);
  return payload || {};
}

function showError(error) {
  if (error instanceof ApiError && error.status === 403) {
    state.authenticated = false;
    renderLogin("Сессия истекла или доступ запрещён.");
    return;
  }
  showToast(error?.message || "Не удалось выполнить запрос", true);
}

function setBusy(buttonNode, busy, label) {
  if (!buttonNode) return;
  if (!buttonNode.dataset.defaultLabel) buttonNode.dataset.defaultLabel = buttonNode.textContent;
  buttonNode.disabled = busy;
  buttonNode.textContent = busy ? (label || buttonNode.dataset.defaultLabel) : buttonNode.dataset.defaultLabel;
}

function fieldValue(object, key) {
  if (!object) return undefined;
  if (object[key] !== undefined) return object[key];
  const capitalized = key.charAt(0).toUpperCase() + key.slice(1);
  if (object[capitalized] !== undefined) return object[capitalized];
  const upper = key.toUpperCase();
  if (object[upper] !== undefined) return object[upper];
  return undefined;
}

function formatDate(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}

function formatNumber(value) {
  const number = Number(value);
  return Number.isFinite(number) ? new Intl.NumberFormat("ru-RU").format(number) : "—";
}

function jsonValue(value) {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  return JSON.stringify(value, null, 2);
}

function scalarValue(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function card(label, value) {
  return el("div", { className: "card" }, [
    el("div", { className: "card-label", text: label }),
    el("div", { className: "card-value", text: String(value) }),
  ]);
}

function badge(text, kind = "") {
  return el("span", { className: `badge ${kind}`.trim(), text: String(text) });
}

function empty(message) {
  return el("div", { className: "empty", text: message });
}

function parseValue(value, json) {
  if (!json) {
    const number = Number(value);
    if (!Number.isFinite(number)) throw new Error("Введите число");
    return number;
  }
  try {
    return JSON.parse(value);
  } catch {
    throw new Error("Введите корректный JSON");
  }
}

function renderLogin(message = "") {
  state.authenticated = false;
  const username = el("input", {
    id: "admin-username",
    name: "username",
    type: "text",
    autocomplete: "username",
    maxLength: "64",
    spellcheck: "false",
    required: true,
  });
  const password = el("input", {
    id: "admin-password",
    name: "password",
    type: "password",
    autocomplete: "current-password",
    maxLength: "72",
    required: true,
  });
  const submit = button("Войти", "btn", async () => {
    const login = username.value.trim();
    const secret = password.value;
    if (!login || !secret) {
      showToast("Введите логин и пароль", true);
      return;
    }
    setBusy(submit, true, "Проверяем…");
    try {
      await request("/v1/admin/login", {
        method: "POST",
        body: JSON.stringify({ username: login, password: secret }),
      });
      state.authenticated = true;
      await loadData();
    } catch (error) {
      showError(error);
    } finally {
      setBusy(submit, false);
    }
  });
  const form = el("form", { onSubmit: (event) => { event.preventDefault(); submit.click(); } }, [
    el("label", { for: "admin-username", text: "Логин" }, [username]),
    el("label", { for: "admin-password", text: "Пароль" }, [password]),
    submit,
  ]);
  app.replaceChildren(el("div", { className: "login-shell" }, [
    el("section", { className: "login-card" }, [
      el("div", { className: "brand" }, [el("span", { className: "brand-mark", text: "TC" }), "Taro Control"]),
      el("h1", { text: "Управление проектом" }),
      el("p", { className: "muted", text: "Панель работает через локальный admin-access и использует логин с паролем." }),
      message ? el("div", { className: "notice", text: message }) : null,
      form,
      el("p", { className: "small muted", text: "Пароль передаётся только по локальному HTTPS/туннелю и хранится в виде bcrypt-хэша." }),
    ]),
  ]));
}

function navButton(view, label, icon) {
  return button(`${icon}  ${label}`, `nav-button ${state.view === view ? "active" : ""}`, () => {
    state.view = view;
    render();
  });
}

function renderShell() {
  const sidebar = el("aside", { className: "sidebar" }, [
    el("div", { className: "brand" }, [el("span", { className: "brand-mark", text: "TC" }), "Taro Control"]),
    el("nav", { className: "sidebar-nav", "aria-label": "Разделы" }, [
      navButton("dashboard", "Обзор", "01"),
      navButton("config", "Настройки", "02"),
      navButton("plans", "Тарифы", "03"),
      navButton("spreads", "Расклады", "04"),
      navButton("payments", "Платежи", "05"),
      navButton("push", "Push", "06"),
    ]),
    el("div", { className: "sidebar-footer" }, [
      el("div", { className: "session-label", text: "Сессия администратора" }),
      button("Выйти", "btn ghost", async () => {
        try {
          await request("/v1/admin/logout", { method: "POST", body: "{}" });
          renderLogin("Сессия завершена.");
        } catch (error) {
          showToast(`Не удалось завершить сеанс: ${error?.message || "ошибка сервера"}`, true);
        }
      }),
    ]),
  ]);
  const content = el("section", { className: "content" });
  const main = el("main", { className: "main" }, [
    el("header", { className: "topbar" }, [
      el("div", {}, [
        el("h2", { text: views[state.view] }),
        el("p", { className: "muted small", text: "Локальная панель управления текущим deployment" }),
      ]),
      el("div", { className: "topbar-actions" }, [
        button("Обновить", "btn secondary", async () => {
          try {
            await loadData();
            showToast("Данные обновлены");
          } catch (error) {
            showError(error);
          }
        }),
      ]),
    ]),
    content,
  ]);
  app.replaceChildren(el("div", { className: "app-shell" }, [sidebar, main]));
  renderView(content);
}

function render() {
  if (!state.authenticated || !state.config) {
    renderLogin();
    return;
  }
  renderShell();
}

function renderView(content) {
  content.replaceChildren();
  if (state.view === "dashboard") renderDashboard(content);
  if (state.view === "config") renderConfig(content);
  if (state.view === "plans") renderPlans(content);
  if (state.view === "spreads") renderSpreads(content);
  if (state.view === "payments") renderPayments(content);
  if (state.view === "push") renderPush(content);
}

function renderDashboard(content) {
  const plans = state.config.plans || [];
  const spreads = state.config.spreads || [];
  const configKeys = Object.keys(state.config.app_config || {}).length;
  const paymentCount = state.payments.length;
  content.append(
    el("div", { className: "card-grid" }, [
      card("Активных тарифов", plans.filter((plan) => plan.is_active).length),
      card("Активных раскладов", spreads.filter((spread) => spread.is_active).length),
      card("Ключей конфигурации", configKeys),
      card("Платежей в выдаче", paymentCount),
    ]),
    el("div", { className: "panel" }, [
      el("div", { className: "panel-header" }, [
        el("div", {}, [el("h3", { text: "Быстрые действия" }), el("p", { className: "small muted", text: "Операции с побочными эффектами требуют подтверждения." })]),
      ]),
      el("div", { className: "action-grid" }, [
        actionCard("Повернуть сезонные расклады", "Применить окна spreads.seasonal по текущей дате.", "Запустить", () => runAction("/v1/admin/rotate-seasonal", "Запустить ротацию сезонных раскладов?")),
        actionCard("Вечерняя рассылка", "Отправить вечерний push пользователям с включённым временем.", "Запустить", () => runAction("/v1/admin/push-evening", "Отправить вечернюю рассылку?")),
        actionCard("Streak risk", "Отправить push пользователям, которые недавно потеряли активность.", "Запустить", () => runAction("/v1/admin/push-streak-risk", "Отправить streak-risk рассылку?")),
        actionCard("Напоминание об окончании", "Отправить напоминание подпискам, истекающим в течение 72 часов.", "Запустить", () => runAction("/v1/admin/remind-expiring", "Отправить напоминания?")),
      ]),
    ]),
    el("div", { className: "section-grid" }, [
      el("div", { className: "panel" }, [
        el("div", { className: "panel-header" }, [el("h3", { text: "Состояние системы" })]),
        el("div", { className: "btn-row" }, [
          badge("API доступен", "good"),
          badge(`Schema ${state.config.plans?.length ? "подключена" : "не проверена"}`, state.config.plans?.length ? "good" : "warn"),
          badge("Cookie admin", "good"),
        ]),
        el("p", { className: "small muted", text: "Панель работает через loopback admin-access; токен серверного API в браузер не передаётся." }),
      ]),
      el("div", { className: "panel" }, [
        el("div", { className: "panel-header" }, [el("h3", { text: "Последние операции" })]),
        el("p", { className: "small muted", text: "Изменения конфигурации и сезонов фиксируются в admin_audit. Для refund и push аудит ведётся только в текущем API-контуре." }),
      ]),
    ]),
  );
}

function actionCard(title, description, label, handler) {
  return el("div", { className: "action-card" }, [
    el("strong", { text: title }),
    el("p", { text: description }),
    button(label, "btn secondary", handler),
  ]);
}

async function runAction(path, confirmation) {
  if (!window.confirm(confirmation)) return;
  try {
    const result = await request(path, { method: "POST", body: "{}" });
    const values = Object.entries(result || {}).map(([key, value]) => `${key}: ${value}`).join(" · ");
    showToast(values || "Операция завершена");
    await loadData();
  } catch (error) {
    showError(error);
  }
}

const scalarSettings = [
  ["free.daily_limit", "Бесплатных раскладов в день", 0, 100],
  ["love.free_weekly", "Бесплатных love-раскладов в неделю", 0, 100],
  ["history.free_limit", "Лимит бесплатной истории", 1, 500],
];

const stringSettings = [
  ["copy.paywall_title", "Заголовок paywall"],
  ["copy.paywall_desc", "Описание paywall"],
  ["copy.paywall_cta", "Кнопка paywall"],
];

const jsonSettings = [
  ["trial", "Пробный период", "JSON-настройки trial"],
  ["referral", "Реферальная программа", "JSON-настройки referral"],
  ["ai", "AI", "JSON-настройки AI"],
  ["ab.price_month", "A/B цена", "JSON-настройки A/B"],
  ["offers.winback", "Winback", "JSON-настройки предложения"],
  ["spreads.seasonal", "Сезонные окна", "Массив окон с code/from/to"],
  ["payments.yookassa", "ЮKassa", "JSON-настройки провайдера"],
];

function renderConfig(content) {
  const list = el("div", { className: "settings-list" });
  const values = state.config.app_config || {};
  for (const [key, label, min, max] of scalarSettings) {
    const input = el("input", { type: "number", min, max, step: "1", value: scalarValue(values[key], 0) });
    list.append(el("div", { className: "setting-row" }, [
      el("label", { text: label }),
      input,
      el("div", { className: "setting-actions" }, [button("Сохранить", "btn small", () => saveConfig({ [key]: parseValue(input.value, false) }))]),
    ]));
  }
  for (const [key, label] of stringSettings) {
    const input = el("input", { type: "text", maxLength: "500", value: String(values[key] || "") });
    list.append(el("div", { className: "setting-row" }, [
      el("label", { text: label }),
      input,
      el("div", { className: "setting-actions" }, [button("Сохранить", "btn small", () => saveConfig({ [key]: input.value }))]),
    ]));
  }
  for (const [key, label, placeholder] of jsonSettings) {
    const input = el("textarea", { placeholder, spellcheck: "false" });
    input.value = jsonValue(values[key]);
    list.append(el("div", { className: "setting-row" }, [
      el("label", { text: label }),
      input,
      el("div", { className: "setting-actions" }, [button("Сохранить", "btn small", () => saveConfig({ [key]: parseValue(input.value, true) }))]),
    ]));
  }
  content.append(
    el("div", { className: "panel" }, [
      el("div", { className: "panel-header" }, [
        el("div", {}, [el("h3", { text: "Runtime-конфигурация" }), el("p", { className: "small muted", text: "Каждое изменение публикуется отдельной транзакцией и проходит серверную валидацию." })]),
      ]),
      el("div", { className: "notice" }, [el("strong", { text: "Аккуратно: " }), "значения влияют на лимиты, AI, тарифы и сезонные расклады."]),
      list,
    ]),
  );
}

async function saveConfig(appConfig) {
  try {
    const result = await request("/v1/admin/config/publish", {
      method: "POST",
      body: JSON.stringify({ app_config: appConfig }),
    });
    showToast(`Сохранено изменений: ${result?.applied?.app_config ?? 0}`);
    await loadData();
  } catch (error) {
    showError(error);
  }
}

function renderPlans(content) {
  const rows = state.config.plans || [];
  const body = el("tbody");
  for (const plan of rows) {
    const price = el("input", { type: "number", min: "1", max: "100000", step: "1", value: plan.price_rub });
    const stars = el("input", { type: "number", min: "1", max: "100000", step: "1", value: plan.stars_amount });
    const duration = el("input", { type: "number", min: "1", max: "3650", step: "1", value: plan.duration_days ?? "" });
    const active = el("input", { type: "checkbox", checked: plan.is_active });
    const save = button("Сохранить", "btn small", async () => {
      if (duration.value === "" && plan.duration_days !== null && plan.duration_days !== undefined) {
        showToast("Срок тарифа нельзя очистить: укажите новое значение", true);
        return;
      }
      const planPatch = { code: plan.code, price_rub: parseValue(price.value, false), stars_amount: parseValue(stars.value, false), is_active: active.checked };
      if (duration.value !== "") planPatch.duration_days = parseValue(duration.value, false);
      await savePlan(planPatch);
    });
    body.append(el("tr", {}, [
      el("td", {}, [el("span", { className: "code", text: plan.code })]),
      el("td", {}, [price]),
      el("td", {}, [stars]),
      el("td", {}, [duration]),
      el("td", {}, [active]),
      el("td", {}, [save]),
    ]));
  }
  content.append(el("div", { className: "panel" }, [
    el("div", { className: "panel-header" }, [
      el("div", {}, [el("h3", { text: "Тарифы" }), el("p", { className: "small muted", text: "Публикация создаёт новую версию тарифа; существующие платежи сохраняют свой snapshot." })]),
    ]),
    el("div", { className: "table-wrap" }, [el("table", {}, [el("thead", {}, [el("tr", {}, [
      el("th", { text: "Код" }), el("th", { text: "Цена ₽" }), el("th", { text: "Stars" }), el("th", { text: "Дней" }), el("th", { text: "Активен" }), el("th", { text: "" }),
    ])]), body])]),
  ]));
}

async function savePlan(plan) {
  try {
    const result = await request("/v1/admin/config/publish", { method: "POST", body: JSON.stringify({ plans: [plan] }) });
    showToast(`Сохранено тарифов: ${result?.applied?.plans ?? 0}`);
    await loadData();
  } catch (error) {
    showError(error);
  }
}

function renderSpreads(content) {
  const body = el("tbody");
  for (const spread of state.config.spreads || []) {
    const active = el("input", { type: "checkbox", checked: spread.is_active });
    const premium = el("input", { type: "checkbox", checked: spread.is_premium });
    const order = el("input", { type: "number", min: "-1000", max: "1000", step: "1", value: spread.sort_order });
    const save = button("Сохранить", "btn small", async () => {
      await saveSpread({ code: spread.code, is_active: active.checked, is_premium: premium.checked, sort_order: parseValue(order.value, false) });
    });
    body.append(el("tr", {}, [
      el("td", {}, [el("span", { className: "code", text: spread.code })]),
      el("td", {}, [active]),
      el("td", {}, [premium]),
      el("td", {}, [order]),
      el("td", {}, [badge(spread.is_active ? "Активен" : "Выключен", spread.is_active ? "good" : "warn")]),
      el("td", {}, [save]),
    ]));
  }
  content.append(el("div", { className: "panel" }, [
    el("div", { className: "panel-header" }, [
      el("div", {}, [el("h3", { text: "Расклады" }), el("p", { className: "small muted", text: "Позиции и тексты карт управляются через миграции/seed; здесь меняются видимость, порядок и premium-флаг." })]),
    ]),
    el("div", { className: "table-wrap" }, [el("table", {}, [el("thead", {}, [el("tr", {}, [
      el("th", { text: "Код" }), el("th", { text: "Активен" }), el("th", { text: "Premium" }), el("th", { text: "Порядок" }), el("th", { text: "Статус" }), el("th", { text: "" }),
    ])]), body])]),
  ]));
}

async function saveSpread(spread) {
  try {
    const result = await request("/v1/admin/config/publish", { method: "POST", body: JSON.stringify({ spreads: [spread] }) });
    showToast(`Сохранено раскладов: ${result?.applied?.spreads ?? 0}`);
    await loadData();
  } catch (error) {
    showError(error);
  }
}

function renderPayments(content) {
  const search = el("input", { type: "search", placeholder: "Поиск по ID, user или тарифу" });
  const status = el("select", {}, [
    el("option", { value: "", text: "Все статусы" }),
    ...["pending", "succeeded", "refunding", "refunded", "expired", "reconciliation"].map((value) => el("option", { value, text: value })),
  ]);
  const tableWrap = el("div", { className: "table-wrap" });
  const update = () => renderPaymentRows(tableWrap, search.value, status.value);
  search.addEventListener("input", update);
  status.addEventListener("change", update);
  content.append(
    el("div", { className: "panel" }, [
      el("div", { className: "panel-header" }, [
        el("div", {}, [el("h3", { text: "Платежи" }), el("p", { className: "small muted", text: "Возврат доступен только для succeeded и требует подтверждения." })]),
        button("Обновить", "btn secondary", loadPayments),
      ]),
      el("div", { className: "filter-row" }, [el("label", { text: "Фильтр" }, [search]), el("label", { text: "Статус" }, [status])]),
      tableWrap,
    ]),
  );
  renderPaymentRows(tableWrap, "", "");
}

function renderPaymentRows(container, search, status) {
  container.replaceChildren();
  const query = search.trim().toLowerCase();
  const filtered = state.payments.filter((payment) => {
    const currentStatus = String(fieldValue(payment, "status") || "");
    if (status && currentStatus !== status) return false;
    if (!query) return true;
    return [fieldValue(payment, "ID"), fieldValue(payment, "UserID"), fieldValue(payment, "Plan"), fieldValue(payment, "Provider")].some((value) => String(value || "").toLowerCase().includes(query));
  });
  if (!filtered.length) {
    container.append(empty("Платежи не найдены"));
    return;
  }
  const body = el("tbody");
  for (const payment of filtered) {
    const currentStatus = String(fieldValue(payment, "status") || "unknown");
    const refund = currentStatus === "succeeded" ? button("Вернуть", "btn danger small", () => refundPayment(payment)) : badge("—");
    body.append(el("tr", {}, [
      el("td", {}, [el("span", { className: "code", text: String(fieldValue(payment, "ID") || "").slice(0, 8) })]),
      el("td", {}, [el("span", { className: "code", text: String(fieldValue(payment, "Plan") || "—") })]),
      el("td", { text: formatNumber(fieldValue(payment, "Price")) }),
      el("td", { text: formatNumber(fieldValue(payment, "Stars")) }),
      el("td", {}, [badge(currentStatus, currentStatus === "succeeded" ? "good" : currentStatus === "refunded" ? "warn" : currentStatus === "reconciliation" ? "bad" : "")]),
      el("td", { text: formatDate(fieldValue(payment, "Created")) }),
      el("td", {}, [refund]),
    ]));
  }
  container.append(el("table", {}, [el("thead", {}, [el("tr", {}, [
    el("th", { text: "ID" }), el("th", { text: "Тариф" }), el("th", { text: "Цена" }), el("th", { text: "Stars" }), el("th", { text: "Статус" }), el("th", { text: "Создан" }), el("th", { text: "" }),
  ])]), body]));
}

async function refundPayment(payment) {
  const id = fieldValue(payment, "ID");
  if (!id || !window.confirm(`Запустить возврат платежа ${String(id).slice(0, 8)}?`)) return;
  try {
    const result = await request("/v1/admin/refund", { method: "POST", body: JSON.stringify({ payment_id: id }) });
    showToast(result?.reconciled ? "Возврат подтверждён reconcile" : "Возврат запущен");
    await loadPayments();
    render();
  } catch (error) {
    showError(error);
  }
}

function renderPush(content) {
  const actions = el("div", { className: "action-grid" }, [
    actionCard("Вечерняя рассылка", "Реальная отправка push пользователям.", "Запустить", () => runAction("/v1/admin/push-evening", "Отправить вечернюю рассылку?")),
    actionCard("Streak risk", "Отправка по потерянной активности.", "Запустить", () => runAction("/v1/admin/push-streak-risk", "Отправить streak-risk рассылку?")),
    actionCard("Истекающие подписки", "Напоминание о подписках, истекающих скоро.", "Запустить", () => runAction("/v1/admin/remind-expiring", "Отправить напоминания?")),
  ]);
  const table = el("div", { className: "table-wrap" });
  renderPushStats(table);
  content.append(
    el("div", { className: "panel" }, [el("div", { className: "panel-header" }, [el("div", {}, [el("h3", { text: "Операции push" }), el("p", { className: "small muted", text: "Кнопки запускают реальные рассылки, а не preview." })])]), actions]),
    el("div", { className: "panel" }, [el("div", { className: "panel-header" }, [el("h3", { text: "Агрегаты за 7 дней" })]), table]),
  );
}

function renderPushStats(container) {
  container.replaceChildren();
  if (!state.pushStats.length) {
    container.append(empty("Статистика пока пуста"));
    return;
  }
  const body = el("tbody");
  for (const item of state.pushStats) {
    body.append(el("tr", {}, [
      el("td", {}, [el("span", { className: "code", text: String(fieldValue(item, "kind") || "—") })]),
      el("td", { text: formatNumber(fieldValue(item, "count")) }),
      el("td", { text: formatDate(fieldValue(item, "last")) }),
    ]));
  }
  container.append(el("table", {}, [el("thead", {}, [el("tr", {}, [el("th", { text: "Тип" }), el("th", { text: "Событий" }), el("th", { text: "Последнее" })])]), body]));
}

async function loadPayments() {
  try {
    state.payments = await request("/v1/admin/payments?limit=200");
    if (state.view === "payments") render();
  } catch (error) {
    showError(error);
  }
}

async function loadData() {
  const [config, payments, pushStats] = await Promise.all([
    request("/v1/admin/config"),
    request("/v1/admin/payments?limit=200"),
    request("/v1/admin/push-stats"),
  ]);
  state.config = config;
  state.payments = payments;
  state.pushStats = pushStats;
  state.authenticated = true;
  render();
}

renderLogin();
