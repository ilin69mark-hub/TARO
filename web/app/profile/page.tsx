"use client";

import { useEffect, useState } from "react";
import { api, csrf } from "@/lib/api";
import { ensureAuth } from "@/lib/auth";
import { track, events } from "@/lib/analytics";
import { applyCalm, isCalm } from "@/components/Legal";
import { useInstallPrompt } from "@/components/PWA";
import PushOptIn from "@/components/PushOptIn";
import PushPrefs from "@/components/PushPrefs";
import PaywallSheet, { Plan } from "@/components/PaywallSheet";
import TelegramLinkCard from "@/components/TelegramLinkCard";
import { useMe } from "@/lib/me";
import { useScrollLock } from "@/lib/useScrollLock";

// Профиль: лимиты + тарифы + реферальный код (см. T18, 02-functional/05/06).
type Ent = { plan: string; free_left: number; love_left_week: number; valid_until: string | null; winback_eligible?: boolean };
type Ref = { code: string; invited: number; bonus_days: number };

export default function ProfilePage() {
  const [ent, setEnt] = useState<Ent | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [ref, setRef] = useState<Ref | null>(null);
  const [showPaywall, setShowPaywall] = useState(false);
  const [applyCode, setApplyCode] = useState("");
  const [applyMsg, setApplyMsg] = useState("");
  const [copiedRef, setCopiedRef] = useState(false);
  const [calm, setCalm] = useState(false);
  const [deleted, setDeleted] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [busy, setBusy] = useState(false);
  const { canInstall, eligible, promptInstall } = useInstallPrompt();
  // Профиль нужен не только ради баннера: без него человек не видит свой
  // user_id, а оператор не может найти аккаунт для выдачи доступа.
  const { me, reload: reloadMe } = useMe();

  // remove — удаление по подтверждению из модалки. Раньше стояли системные
  // confirm() и prompt(): два окна подряд, неуправляемый ввод и никакого
  // оформления. Теперь одно окно, «Отмена» подсвечена и стоит первой.
  async function remove() {
    if (confirmText.trim().toUpperCase() !== "УДАЛИТЬ") return;
    setBusy(true);
    try {
      const res = await fetch("/api/me", {
        method: "DELETE",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
        body: JSON.stringify({ confirm: "DELETE" }),
      }).catch(() => undefined);
      if (!res?.ok) return;
      try {
        localStorage.clear();
      } catch {
        /* ignore */
      }
      setConfirmDelete(false);
      setConfirmText("");
      setDeleted(true);
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void ensureAuth()
      .then(() => {
        void api.get<Ent>("/entitlements/me").then(setEnt).catch(() => undefined);
        void api.get<Plan[]>("/plans").then(setPlans).catch(() => undefined);
        void api.get<Ref>("/referral/me").then(setRef).catch(() => undefined);
        setCalm(isCalm());
      })
      .catch(() => undefined);
  }, []);

  async function apply() {
    setApplyMsg("");
    try {
      await api.post("/referral/apply", { code: applyCode.trim().toUpperCase() });
      setApplyMsg("Код принят! Бонус придет после первого расклада.");
      track(events.referralApplied, { via: "manual" });
    } catch (e: unknown) {
      setApplyMsg((e as Error).message);
    }
  }

  // Приглашение-ссылка. Собирается из origin, а не из зашитого хоста: на
  // проде это https://<домен>/r/<code>, иначе инвайты уходили бы в dev.
  function refLink(): string {
    if (!ref?.code) return "";
    return `${typeof window === "undefined" ? "" : window.location.origin}/r/${ref.code}`;
  }

  const refTG = `https://t.me/share/url?url=${encodeURIComponent(
    typeof window !== "undefined" ? refLink() : ""
  )}&text=${encodeURIComponent("Попробуй Таро — за первый расклад оба получим +3 дня безлимита")}`;

  async function copyRef() {
    try {
      await navigator.clipboard.writeText(refLink());
      setCopiedRef(true);
      track(events.shareDone, { kind: "referral" });
    } catch {
      setCopiedRef(false);
    }
  }

  // Пока открыто окно удаления, страница под ним не прокручивается.
  useScrollLock(confirmDelete);

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-center text-3xl font-display font-semibold text-paper">Профиль</h1>
      {/* Ссылка на дневник убрана — он стал пятым пунктом меню (TabBar). */}
      <section className="mt-6 rounded-2xl border border-white/10 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-mist">Тариф</p>
        <p className="mt-1 text-lg text-paper">
          {ent ? (ent.plan === "premium" ? "Безлимит" : `Гость — осталось сегодня: ${ent.free_left}`) : "…"}
        </p>
        {ent?.valid_until && (
          <p className="mt-1 text-sm text-mist">
            до {new Date(ent.valid_until).toLocaleDateString("ru-RU")}
          </p>
        )}
        <button
          onClick={() => setShowPaywall(true)}
          className="mt-4 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
        >
          Тарифы
        </button>
        {ent?.plan === "premium" && (
          <p className="mt-3 rounded-2xl border border-gold/40 p-3 text-sm text-paper">
            Годовой безлимит выгоднее −30% — загляни в тарифы 🌙
          </p>
        )}
        {ent?.winback_eligible && (
          <p className="mt-3 rounded-2xl border border-gold/40 p-3 text-sm text-paper">
            Скучали! Для возвращения доступен тариф со скидкой — спроси в тарифах.
          </p>
        )}
        {/* Отступ mt-5 обязателен: карточка лежит ВНУТРИ секции тарифа, а та и
            эта карточка имеют одинаковый фон bg-card. Без явного зазора
            «Тарифы» и бокс привязки сливались в один блок, и кнопка читалась
            как лежащая на нижнем боксе (жалоба владельца, 2026-09-30).
            Через className, а не обёрткой: карточка возвращает null при
            привязанном Telegram, и обёртка оставила бы пустое место. */}
        <TelegramLinkCard
          className="mt-5"
          me={me}
          onLinked={() => {
            reloadMe();
            void api.get<Ent>("/entitlements/me").then(setEnt).catch(() => undefined);
            void api.get<Ref>("/referral/me").then(setRef).catch(() => undefined);
          }}
        />
        {me && (
          <p className="mt-4 text-xs text-mist">
            Идентификатор аккаунта: <code className="break-all text-violet">{me.user_id}</code>
          </p>
        )}
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-mist">Пригласи друга</p>
        <p className="mt-1 text-sm text-paper">
          Приглашённый делает первый расклад по вашей ссылке — вы оба получаете
          +3 дня безлимита.
        </p>
        {/* Раньше тут был только «голый» код: нечем было поделиться, приглашённый
            обязан был продиктовать 8 символов. Теперь дип-линк /r/<code>
            (спека 06-referral) рабочий, плюс копирование и шаринг в Telegram. */}
        <p className="mt-3 break-all text-sm text-mist">{ref ? refLink() : "…"}</p>
        <p className="mt-1 text-2xl font-semibold tracking-widest text-gold">
          {ref ? ref.code : "…"}
        </p>
        <p className="mt-1 text-sm text-mist">
          Приглашено: {ref?.invited ?? "…"} · заработано дней: {ref?.bonus_days ?? "…"}
        </p>
        {ref && (
          <div className="mt-4 flex gap-2">
            <a
              href={refTG}
              target="_blank"
              rel="noopener"
              onClick={() => track(events.shareDone, { kind: "referral" })}
              className="flex-1 rounded-2xl border border-gold/40 px-4 py-2 text-sm font-semibold text-gold"
            >
              Пригласить в TG
            </a>
            <button
              onClick={copyRef}
              className="flex-1 rounded-2xl border border-white/10 px-4 py-2 text-sm text-paper"
            >
              {copiedRef ? "Скопировано!" : "Скопировать"}
            </button>
          </div>
        )}
        <div className="mt-4 flex flex-col gap-2">
          <label htmlFor="ref-code" className="text-sm text-mist">
            Есть код? Впишите его — бонус придёт после первого расклада приглашённого.
          </label>
          <div className="mx-auto flex max-w-sm gap-2">
            <input
              id="ref-code"
              value={applyCode}
              onChange={(e) => setApplyCode(e.target.value)}
              placeholder="Код из 8 символов"
              className="min-w-0 flex-1 rounded-2xl border border-white/10 bg-deep p-3 text-base uppercase tracking-widest text-paper placeholder:normal-case placeholder:tracking-normal placeholder:text-mist"
            />
            <button onClick={apply} className="rounded-2xl border border-gold/40 px-4 text-sm font-semibold text-gold">
              Применить
            </button>
          </div>
        </div>
        {applyMsg && <p className="mt-2 text-sm text-mist">{applyMsg}</p>}
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-mist">Спокойный режим</p>
        <p className="mx-auto mt-1 max-w-xs text-sm text-mist">
          Меньше движения и объёма картинок. Автоматически включается при высокой
          нагрузке.
        </p>
        <button
          onClick={() => {
            const next = !calm;
            setCalm(next);
            applyCalm(next);
          }}
          aria-pressed={calm}
          className="mt-3 rounded-2xl border border-white/10 px-5 py-2 text-sm text-paper"
        >
          {calm ? "Включен" : "Выключен"}
        </button>
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-mist">Напоминания</p>
        <div className="mx-auto mt-2 max-w-sm text-center">
          <PushOptIn />
        </div>
        <div className="mx-auto max-w-sm text-center">
          <PushPrefs />
        </div>
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-mist">Приложение</p>
        {canInstall ? (
          <button
            onClick={promptInstall}
            className="mt-2 w-full rounded-2xl border border-gold/40 px-4 py-2 text-sm font-semibold text-gold"
          >
            {eligible ? "Установить приложение" : "Установить — после второго расклада"}
          </button>
        ) : (
          <p className="mt-2 text-sm text-mist">
            Установка недоступна: браузер не предлагает, либо приложение уже стоит.
          </p>
        )}
        <p className="mt-2 text-xs text-mist">
          Работает без интернета: карты и последние расклады открываются из кэша.
        </p>
      </section>
      {/* Удаление данных — отдельным блоком В САМОМ НИЗУ страницы (владелец).
          Красное: действие необратимо. Перед вызовом — модалка с подтверждением,
          где явно подсвечена «Отмена»: удаление не должно подтверждаться
          на бегу, вслепую, одной кнопкой. */}
      <section className="mt-8 rounded-2xl border border-red-500/30 bg-card p-5 text-center">
        <p className="text-sm uppercase tracking-wider text-red-400">Удаление данных</p>
        <p className="mt-1 text-sm text-mist">
          Стирает аккаунт, чтения и дневник. Отменить нельзя.
        </p>
        <button
          onClick={() => setConfirmDelete(true)}
          className="mt-3 w-full rounded-2xl border border-red-500/50 px-4 py-2.5 text-sm font-semibold text-red-400"
        >
          Удалить мои данные
        </button>
        {deleted && (
          <p className="mt-2 text-sm text-mist">Данные удалены. Обнови страницу для нового начала.</p>
        )}
      </section>
      {confirmDelete && (
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="del-title"
          className="fixed inset-0 z-20 flex items-center justify-center bg-black/70 p-4"
        >
          <div className="w-full max-w-sm rounded-2xl border border-white/10 bg-card p-5">
            <h2 id="del-title" className="text-lg font-semibold text-paper">
              Удалить все мои данные?
            </h2>
            <p className="mt-2 text-sm text-mist">
              Аккаунт, чтения и записи дневника будут стёрты безвозвратно.
              Если передумали — нажмите «Отмена».
            </p>
            {confirmText && (
              <p className="mt-2 text-sm text-red-400">
                Введите слово УДАЛИТЬ, чтобы подтвердить.
              </p>
            )}
            <label htmlFor="del-confirm" className="mt-3 block text-sm text-mist">
              Для подтверждения впишите УДАЛИТЬ
            </label>
            <input
              id="del-confirm"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              autoComplete="off"
              placeholder="УДАЛИТЬ"
              className="mt-1 w-full rounded-2xl border border-white/15 bg-deep px-4 py-2.5 text-sm text-paper placeholder:text-mist/60"
            />
            <div className="mt-4 flex flex-col gap-2">
              <button
                onClick={() => {
                  setConfirmDelete(false);
                  setConfirmText("");
                }}
                autoFocus
                className="w-full rounded-2xl border border-gold/60 bg-gold/10 px-4 py-2.5 text-sm font-semibold text-gold"
              >
                Отмена
              </button>
              <button
                onClick={remove}
                disabled={busy}
                className="w-full rounded-2xl border border-red-500/50 px-4 py-2.5 text-sm font-semibold text-red-400 disabled:opacity-50"
              >
                {busy ? "Удаляю…" : "Удалить навсегда"}
              </button>
            </div>
          </div>
        </div>
      )}
      {showPaywall && <PaywallSheet plans={plans} onClose={() => setShowPaywall(false)} />}
    </main>
  );
}
