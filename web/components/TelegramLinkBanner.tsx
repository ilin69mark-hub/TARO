"use client";

// Баннер «привяжите Telegram» на весь сайт.
//
// Почему он здесь, а не только в профиле: профиль открывают единицы, а потерять
// покупку можно молча — человек закрыл вкладку, сменил телефон, и через месяц
// обнаружил, что доступа нет. Баннер на каждой странице — это «периодический
// возврат», о котором шла речь.
//
// Условие жёсткое: только аноним, который РЕАЛЬНО платил. Без оплаты баннер
// превращается в постоянный раздражающий плашкой, а человеку, который не
// привязывал Telegram сознательно, он вообще ничего не сообщает.
//
// Показ один раз: после закрытия пишем метку и больше не мучаем. Повторное
// напоминание имеет смысл только если привязка не случилась, а это человек
// решил сам — навязываться не надо.

import { useEffect, useState } from "react";
import { insideTelegram, linkTelegram, startHandoff, telegramAppUrl, useMe } from "@/lib/me";

const DISMISSED = "taro_tg_banner_dismissed";

export default function TelegramLinkBanner() {
  const { me, loading, reload } = useMe();
  const [hidden, setHidden] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (loading || !me || me.telegram_linked || !me.has_payment) return;
    try {
      if (localStorage.getItem(DISMISSED) === "1") return;
    } catch {
      /* localStorage может быть недоступен — тогда просто покажем */
    }
    setHidden(false);
  }, [me, loading]);

  if (hidden || !me) return null;

  function dismiss() {
    setHidden(true);
    try {
      localStorage.setItem(DISMISSED, "1");
    } catch {
      /* ignore */
    }
  }

  async function link() {
    setBusy(true);
    setError("");
    // Вне Telegram initData нет, и link упрётся в 422. Единственный способ
    // для анонимного плательщика — перенос: увести в Telegram с одноразовым
    // токеном. Баннер появляется именно у заплативших анонимов, так что
    // «идти в Telegram» здесь — не отговорка, а единственное действие.
    if (!insideTelegram()) {
      const ok = await startHandoff(me?.last_plan_code || "");
      setBusy(false);
      if (!ok) setError("Не удалось начать перенос. Попробуйте ещё раз.");
      return;
    }
    const res = await linkTelegram();
    setBusy(false);
    if (res.ok) {
      reload();
      return;
    }
    setError(res.message);
  }

  return (
    <div
      role="status"
      className="border-b border-gold/30 bg-elev px-4 py-2 text-center text-xs text-paper"
    >
      <p>
        Покупки привязаны к этому устройству —{" "}
        <button type="button" onClick={link} disabled={busy} className="underline disabled:opacity-50">
          {busy ? "привязываем…" : insideTelegram() ? "привяжите Telegram" : "перенесите покупку в Telegram"}
        </button>
        , чтобы не потерять доступ
      </p>
      {!telegramAppUrl() && (
        <p className="mt-1 text-mist">Перенос работает, когда приложение опубликовано в Telegram.</p>
      )}
      {error && <p className="mt-1 text-mist">{error}</p>}
      <button
        type="button"
        onClick={dismiss}
        aria-label="Скрыть"
        className="absolute right-3 top-2 text-mist"
      >
        ✕
      </button>
    </div>
  );
}
