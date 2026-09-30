"use client";

// Приглашение привязать Telegram.
//
// Показывается в двух местах — в профиле и в paywall ДО оплаты. Место в
// paywall важнее: привязка после оплаты уже не спасает подписку, если человек
// успел сменить устройство, а до — успевает.
//
// Тон здесь особенный. Формулировка про «сохранить» без причины выглядит как
// давление, поэтому объясняем, что именно ломается: доступ держится на этом
// устройстве, и новое его не увидит.

import { useState } from "react";
import { insideTelegram, linkTelegram, startHandoff, telegramAppUrl, type Me } from "@/lib/me";

export type LinkCardProps = {
  me: Me | null;
  /** После успешной привязки: перезапросить профиль и квоты. */
  onLinked?: () => void;
  /** Компактный вид для paywall. */
  compact?: boolean;
};

export default function TelegramLinkCard({ me, onLinked, compact = false }: LinkCardProps) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [done, setDone] = useState(false);

  // Привязан — нечего показывать. И не показываем при загрузке, чтобы карточка
  // не мигала на каждой загрузке страницы.
  if (!me || me.telegram_linked || done) return null;

  const appUrl = telegramAppUrl();
  // Срочность зависит от факта оплаты. Человеку без платежей это «сохраните
  // доступ», человеку с платежом — «ваши деньги сейчас на этом устройстве и
  // только на нём». Один и тот же компонент с двумя формулировками: разводить
  // их в два компонента незачем, различается только тон.
  const paid = me.has_payment;

  async function link() {
    setBusy(true);
    setMsg("");
    const res = await linkTelegram();
    setBusy(false);
    if (res.ok) {
      setDone(true);
      onLinked?.();
      return;
    }
    setMsg(res.message);
  }

  async function transfer() {
    setBusy(true);
    setMsg("");
    // plan_code кладётся в токен: по нему потом открывается счёт. Пустым его
    // слать нельзя — в журнале переноса иначе не останется ничего, и вопрос
    // «что именно перенесли» останется без ответа.
    const code = me?.last_plan_code || "";
    const ok = await startHandoff(code);
    setBusy(false);
    if (!ok) {
      setMsg("Не удалось начать перенос. Попробуйте ещё раз через минуту.");
    }
  }

  return (
    <section
      className={
        compact
          ? "rounded-2xl border border-gold/40 bg-card p-3"
          : "rounded-2xl border border-gold/40 bg-card p-4"
      }
      data-testid="tg-link-card"
      aria-labelledby="tg-link-title"
    >
      <h2 id="tg-link-title" className={compact ? "text-sm text-paper" : "text-base text-paper"}>
        {paid ? "Ваши покупки привязаны к устройству" : "Привяжите Telegram"}
      </h2>
      <p className={compact ? "mt-1 text-xs text-mist" : "mt-1 text-sm text-mist"}>
        {paid
          ? "Вы платили, и подписка сейчас держится на этом устройстве. Смените его или очистите данные — и покупок не станет. Telegram их удержит."
          : "Сейчас доступ держится на этом устройстве. Новое устройство или вход из Telegram его не увидят."}
      </p>

      {/* Кнопка только внутри Telegram: вне его нет initData, и сервер вернёт
          422. Показывать её там, где она не сработает, — значит обмануть
          человека и заставить его сделать лишний тап. */}
      {appUrl ? (
        <>
          <a
            href={appUrl}
            className="mt-3 inline-flex h-11 w-full items-center justify-center rounded-2xl border border-white/20 text-sm font-semibold text-paper active:scale-95"
          >
            Открыть в Telegram
          </a>
          {/*
            «Уже покупали?» — строка для человека, который заплатил и пришёл в
            другой браузер.

            Замеряно на стенде: после оплаты month_299 новый браузер получает
            plan=free, has_payment=false и видит paywall, хотя деньги его.
            Определить такого человека нечем — у анонимной сессии в новом
            браузере нет ни cookie, ни localStorage, и сервер физически не
            знает, что это тот же человек. Поэтому строка идёт ВСЕМ анонимам
            вне Telegram, а не «бывшим плательщикам»: таргетировать не по чему,
            и псевдо-персонализация была бы враньём.

            Формулировка сообщает не «купите», а «если покупка была — она там»,
            и это единственное, что здесь правдиво. Разместил её рядом с
            кнопкой в Telegram: человек, который ищет покупку, смотрит именно
            сюда.
          */}          {!paid && !insideTelegram() && (
            <p className="mt-2 text-xs text-mist" data-testid="tg-already-paid">
              Уже покупали? Если доступ был в Telegram — он там и остался.
              Откройте приложение в Telegram, и покупки будут на месте.
            </p>
          )}
        </>
      ) : (
        <p className="mt-3 rounded-xl bg-elev px-3 py-2 text-xs text-mist">
          Откройте приложение в Telegram — привязка делается только там.
        </p>
      )}

      {msg && (
        <p className="mt-2 text-xs text-goldsoft" role="status">
          {msg}
        </p>
      )}
      <TelegramLinkButton busy={busy} onClick={link} compact={compact} />
      {/*
        ВНЕ Telegram обычная привязка невозможна: initData там не бывает. Но
        ровно здесь перенос и нужен — человек заплатил в браузере. Поэтому
        показываем кнопку, которая уводит его в Telegram с одноразовым
        токеном, а не бесполезную «привязку», которая упрётся в 422.
      */}
      {!insideTelegram() && paid && appUrl && (
        <button
          type="button"
          onClick={transfer}
          disabled={busy}
          data-testid="tg-handoff-btn"
          className={
            compact
              ? "mt-2 inline-flex h-10 w-full items-center justify-center rounded-xl border border-gold/50 text-sm font-semibold text-goldsoft active:scale-95 disabled:opacity-50"
              : "mt-2 inline-flex h-11 w-full items-center justify-center rounded-2xl border border-gold/50 text-sm font-semibold text-goldsoft active:scale-95 disabled:opacity-50"
          }
        >
          {busy ? "Готовим перенос…" : "Перенести покупку в Telegram"}
        </button>
      )}
    </section>
  );
}

/** Кнопка привязки — вынесена, чтобы «внутри/вне Telegram» было видно в одном месте. */
function TelegramLinkButton({
  busy,
  onClick,
  compact,
}: {
  busy: boolean;
  onClick: () => void;
  compact: boolean;
}) {
  // Вне Telegram initData нет — прячем кнопку совсем, а не «disable».
  if (typeof window !== "undefined" && !(window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp?.initData) {
    return null;
  }
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className={
        compact
          ? "mt-2 inline-flex h-10 w-full items-center justify-center rounded-xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold text-deep active:scale-95 disabled:opacity-50"
          : "mt-3 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95 disabled:opacity-50"
      }
    >
      {busy ? "Привязываем…" : "Привязать Telegram"}
    </button>
  );
}
