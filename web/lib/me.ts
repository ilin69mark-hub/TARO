// Профиль текущей сессии: GET /v1/me и привязка Telegram.
//
// Зачем это отдельно от /v1/entitlements/me: там квоты (free_left, winback) с
// кэшем, а здесь личность — свой user_id, привязан ли Telegram, платил ли
// человек вообще. Смешивать их нельзя: правила лимитов и правила показа
// приглашения двигаются по разным причинам.
//
// Почему привязка вообще нужна. Аноним, который заплатил 299₽, держит
// подписку на строке, привязанной к anon_uuid в localStorage. Смена
// устройства, очистка данных, вход из Telegram — это новый users.id, и
// подписка остаётся у анонима. Деньги целы, но человек их не видит.
//
// Привязка — POST /v1/auth/link, который мержит аккаунты в одной транзакции
// (valid_until = max(a,b), чтения/платежи/рефералы перелинковываются). Но он
// требует initData, а initData существует ТОЛЬКО внутри Telegram. Поэтому
// вне Telegram кнопка не просто не работает — её и показывать нельзя, иначе
// человек нажмёт и упрётся в стену. Отсюда две ветки в UI.

import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { ensureAuth, getFp } from "./auth";

export type Me = {
  user_id: string;
  telegram_linked: boolean;
  age_confirmed: boolean;
  /** Платил ли когда-либо. Не то же самое, что «есть подписка»: подписка
   *  истекает, факт оплаты — нет, и именно по факту решается, показывать ли
   *  приглашение привязаться. */
  has_payment: boolean;
  valid_until: string | null;
  created_at: string;
  referral_code: string;
  readings_total: number;
  /** Тариф действующей покупки. Нужен переносу: он кладётся в токен, и без
   *  него в журнале не остаётся, что именно перенесли. NULL без подписки. */
  last_plan_code: string | null;
};

export type MeState = {
  me: Me | null;
  loading: boolean;
  failed: boolean;
  reload: () => void;
};

/** Есть ли мы внутри Telegram WebApp. Вне него initData нет, и Link невозможен. */
export function insideTelegram(): string {
  if (typeof window === "undefined") return "";
  return (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp
    ?.initData ?? "";
}

/**
 * Ссылка, по которой приложение открывается в Telegram. Это публичный адрес
 * бота, секретом не является, поэтому задаётся на сборке. Пустая строка —
 * приложение ещё не опубликовано, и приглашение показывается словами.
 */
export function telegramAppUrl(): string {
  return process.env.NEXT_PUBLIC_TG_APP_URL || "";
}

export function useMe(): MeState {
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let live = true;
    setLoading(true);
    void ensureAuth()
      .then(() => api.get<Me>("/me"))
      .then((p) => {
        if (!live) return;
        setMe(p);
        setFailed(false);
      })
      .catch(() => {
        if (!live) return;
        setFailed(true);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { me, loading, failed, reload };
}

export type LinkResult =
  | { ok: true; merged: boolean }
  | { ok: false; code: string; message: string };

/**
 * Привязка Telegram к текущей сессии. Сервер после мержа перевыпускает cookie,
 * поэтому вызывающий обязан перезапросить профиль и квоты — иначе на экране
 * останутся данные старого аккаунта.
 *
 * handoff — токен переноса из start_param. С ним user_id берётся не из сессии,
 * а из одноразового токена, и fingerprint не сверяется: устройства у браузера и
 * у WebApp заведомо разные. Подробности в handoff.go.
 */
export async function linkTelegram(handoff?: string): Promise<LinkResult> {
  const initData = insideTelegram();
  if (!initData) {
    return {
      ok: false,
      code: "NOT_IN_TELEGRAM",
      message: "Привязка работает только внутри Telegram — откройте приложение там.",
    };
  }
  try {
    const res = await api.post<{ merged: boolean }>("/auth/link", {
      initData,
      fingerprint: getFp(),
      ...(handoff ? { handoff } : {}),
    });
    return { ok: true, merged: Boolean(res.merged) };
  } catch (e: unknown) {
    const err = e as Error & { code?: string };
    // ALREADY_LINKED — не ошибка для человека: он уже привязан.
    if (err.code === "ALREADY_LINKED") return { ok: true, merged: false };
    return { ok: false, code: err.code || "LINK_FAILED", message: err.message };
  }
}

// ─── Перенос покупки в Telegram ────────────────────────────────────────────
//
// Проблема, которую это решает. Аноним заплатил 299₽; подписка живёт на строке
// users, найденной по anon_uuid в localStorage. Смена телефона, очистка данных,
// вход из Telegram — новый users.id, и человек не видит своей покупки. Раньше
// выход был один: он и так должен был открыть Telegram, но link требует
// initData, а initData в обычном браузере не бывает. То есть в момент, когда
// перенос нужнее всего, способа не было.
//
// Как устроено. Браузер просит одноразовый токен (POST /auth/handoff) и
// открывает t.me-ссылку с этим токеном в startapp. Telegram открывает Mini App,
// тот достаёт токен из initDataUnsafe.start_param и отправляет его в
// POST /auth/link вместе с живым initData. Сервер склеивает аккаунты.
//
// Почему startapp, а не ?t= в URL. Два независимых причины.
//  1) Техническая: NEXT_PUBLIC_TG_APP_URL — это https://t.me/<bot>/<app>.
//     Параметры запроса к t.me в Mini App не попадают; единственный
//     транспорт — startapp. Ссылка с «?t=» просто не сработала бы.
//  2) Безопасностная: токен не попадает в адресную строку Mini App, историю
//     браузера, access-лог nginx и $current_url в PostHog. Платить приходится
//     за это только одним: исходный браузер всё же открывает t.me-ссылку с
//     токеном, поэтому переход делается через location.replace — записи в
//     истории не остаётся.

/** Префикс start_param. Telegram пропускает только [A-Za-z0-9_-], 64 символа. */
const START_PREFIX = "pay_";

/** Стартовый параметр, который Telegram передал в Mini App. */
export function startParam(): string {
  if (typeof window === "undefined") return "";
  const initDataUnsafe = (window as unknown as {
    Telegram?: { WebApp?: { initDataUnsafe?: { start_param?: string } } };
  }).Telegram?.WebApp?.initDataUnsafe;
  return initDataUnsafe?.start_param ?? "";
}

/** Токен переноса из start_param, либо "" если переноса не было. */
export function handoffToken(): string {
  const raw = startParam();
  return raw.startsWith(START_PREFIX) ? raw.slice(START_PREFIX.length) : "";
}

/**
 * Ссылка на Mini App с токеном внутри. Собирается ТОЛЬКО из значения, вшитого
 * на сборке (NEXT_PUBLIC_TG_APP_URL), и никогда — из ответа сервера: ссылка от
 * сервера означала бы, что скомпрометированный бэкенд может увести человека
 * вместе с токеном куда угодно. В проекте это правило уже есть для invoice_link
 * (PaywallSheet, «Аудит B»).
 */
export function handoffStartLink(token: string): string {
  const app = telegramAppUrl();
  if (!app || !token) return "";
  const sep = app.includes("?") ? "&" : "?";
  return `${app}${sep}startapp=${START_PREFIX}${encodeURIComponent(token)}`;
}

export type HandoffResult =
  | { ok: true; token: string; expiresIn: number }
  | { ok: false; code: string; message: string };

/**
 * Выдать токен переноса. Требует живой браузерной сессии, поэтому вне её
 * отказ ожидаем и не является ошибкой продукта.
 */
export async function requestHandoff(planCode: string): Promise<HandoffResult> {
  try {
    const res = await api.post<{ token: string; expires_in: number }>("/auth/handoff", {
      plan_code: planCode,
    });
    return { ok: true, token: res.token, expiresIn: res.expires_in };
  } catch (e: unknown) {
    const err = e as Error & { code?: string };
    return { ok: false, code: err.code || "HANDOFF_FAILED", message: err.message };
  }
}

/**
 * Перейти в Telegram с переносом. Возвращает false, если уйти не удалось:
 * тогда токен уже израсходован, и повторять кликом бессмысленно.
 */
export async function startHandoff(planCode: string): Promise<boolean> {
  const res = await requestHandoff(planCode);
  if (!res.ok) return false;
  const link = handoffStartLink(res.token);
  if (!link) return false;
  const tg = (window as unknown as { Telegram?: { WebApp?: { openTelegramLink?: (u: string) => void } } })
    .Telegram?.WebApp;
  // Внутри Telegram открываем внутри него. Снаружи — replace, а не open:
  // обычная навигация оставила бы ссылку с токеном в истории браузера.
  if (tg?.openTelegramLink) tg.openTelegramLink(link);
  else window.location.replace(link);
  return true;
}
