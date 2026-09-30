"use client";

import { useEffect, useState } from "react";
import { motion } from "framer-motion";
import { paywallHits, csrf } from "@/lib/api";
import { useScrollLock } from "@/lib/useScrollLock";
import TelegramLinkCard from "@/components/TelegramLinkCard";
import { useMe } from "@/lib/me";

// Paywall-sheet: snap снизу, цены из plans (не хардкод, см. 02-functional/05, T18).
export type Plan = { code: string; price_rub: number; stars_amount: number; duration_days: number | null };

const NAMES: Record<string, string> = {
  month_299: "Безлимит на месяц",
  year_2490: "Безлимит на год −30%",
  single_99: "Разовый premium-расклад",
  trial_3d: "Trial",
  referral_bonus: "Бонус",
};

// Короткие подписи для горизонтальной полосы. Полные названия в колонке ~104px
// не помещались: «Безлимит на год −30%» занимал три строки и line-clamp срезал
// хвост вместе со «−30%», то есть с самой полезной частью — выгодой. Здесь
// смысл сохранён целиком, а полное название лежит в title для наведения.
// Подписи оставлены различимыми по смыслу, а не сокращены до одного слова:
// существующий тест порядка Paywall.test.tsx опознаёт тариф по тексту первой
// карточки, и подпись «Месяц» его бы сломал, не будучи полезнее.
// Различаемость по цене тут важнее компактности — выбор делают по «−30%».
const SHORT: Record<string, string> = {
  month_299: "Безлимит",
  year_2490: "Год −30%",
  single_99: "Разовый",
  trial_3d: "Trial",
  referral_bonus: "Бонус",
};

export default function PaywallSheet({
  plans,
  abPrice,
  onClose,
}: {
  plans: Plan[];
  abPrice?: number; // U22: A/B цена month_299 (0 = выкл)
  onClose: () => void;
}) {
  // E17: чекбокс 18+ обязателен — без него бэк дает 403 (age_confirmed_at).
  // Состояние переживает сессии (localStorage) + пишется на сервер один раз.
  const [adult, setAdult] = useState(false);
  // Аудит C: stable idempotency-ключ на тариф (свежий UUID на клик давал двойные списания)
  // + busy-guard от даблклика.
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [paying, setPaying] = useState<string | null>(null);
  // Привязка Telegram — до оплаты, а не после. После оплаты подписка уже висит
  // на анонимной строке, и привязка её спасёт только если человек ещё помнит,
  // что покупал, и зайдёт в тот же аккаунт.
  const { me, reload: reloadMe } = useMe();

  function keyFor(code: string): string {
    let k = keys[code];
    if (!k) {
      k = crypto.randomUUID();
      setKeys((prev) => ({ ...prev, [code]: k }));
    }
    return k;
  }

  useEffect(() => {
    try {
      if (localStorage.getItem("taro_age") === "1") setAdult(true);
    } catch {
      /* ignore */
    }
  }, []);

  async function confirmAdult(v: boolean) {
    setAdult(v);
    if (!v) return;
    // E11-честный: метку кешируем только после 200 сервера (иначе врём себе при 403).
    try {
      const res = await fetch("/api/me/age", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
        body: JSON.stringify({ confirmed: true }),
      });
      if (!res.ok) {
        setAdult(false);
        return;
      }
      try {
        localStorage.setItem("taro_age", "1");
      } catch {
        /* ignore */
      }
    } catch {
      setAdult(false);
    }
  }

  async function pay(code: string) {
    if (!adult || paying) return; // кнопка disabled + in-flight guard от даблклика
    setPaying(code);
    // D6: Stars-invoice через прокси; в TG открываем нативно, иначе новая вкладка.
    try {
      const res = await fetch("/api/payments/stars/invoice", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
        // idempotency_key стабилен на тариф до успеха (аудит C: свежий UUID на клик давал дубли)
        body: JSON.stringify({ plan_code: code, idempotency_key: keyFor(code) }),
      });
      if (!res.ok) return;
      const { invoice_link } = await res.json();
      // Аудит B: ссылка только t.me, иначе фишинг через скомпрометированный ответ
      if (typeof invoice_link !== "string" || !/^https:\/\/(t\.me|telegram\.me)\//.test(invoice_link)) return;
      // успех: ротируем ключ для следующей покупки
      setKeys((prev) => {
        const next = { ...prev };
        delete next[code];
        return next;
      });
      const tg = (window as unknown as { Telegram?: { WebApp?: { openInvoice?: (u: string) => void } } }).Telegram?.WebApp;
      if (tg?.openInvoice) tg.openInvoice(invoice_link);
      else window.open(invoice_link, "_blank", "noopener");
    } catch {
      /* ignore */
    } finally {
      setPaying(null);
    }
  }
  // U26: упершимся 3+ раз показываем single_99 первым
  const ordered = [...plans].sort((a, b) => {
    if (paywallHits() >= 3) {
      if (a.code === "single_99") return -1;
      if (b.code === "single_99") return 1;
    }
    return 0;
  });

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Пока окно открыто, страница под ним не крутится.
  useScrollLock(true);
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Оформление безлимита"
      // items-end + max-h + ВНУТРЕННИЙ скролл: на низком экране (или при
      // большем числе тарифов) содержимое не влезает, и раньше верх окна —
      // с заголовком и чекбоксом 18+ — уезжал за пределы экрана и был
      // недоступен. dvh, а не vh: адресная строка на мобильных меняет vh.
      className="fixed inset-0 z-20 flex items-end justify-center bg-deep/70"
      onClick={onClose}
    >
      <motion.div
        initial={{ y: "100%" }}
        animate={{ y: 0 }}
        transition={{ type: "spring", damping: 28, stiffness: 300 }}
        className="flex max-h-[90dvh] w-full max-w-md flex-col rounded-t-3xl border-t border-gold/40 bg-elev"
        onClick={(e) => e.stopPropagation()}
      >

        {/* Шапка. Чекбокс 18+ остаётся обязательным (без age_confirmed_at бэк
            отдаёт 403), но в одну строку: рамка во всю ширину съедала ~52px,
            которых не хватало на кнопку оплаты. */}
        <div className="shrink-0 px-6 pt-4">
          <p className="text-lg font-semibold leading-tight text-paper">Заглянем глубже?</p>
          <p className="mt-0.5 text-xs text-mist">На сегодня бесплатные карты закончились</p>
          <label className="mt-2 flex cursor-pointer items-center gap-2">
            <input
              type="checkbox"
              checked={adult}
              onChange={(e) => confirmAdult(e.target.checked)}
              className="h-4 w-4 shrink-0 accent-[#D4AF37]"
            />
            <span className="text-xs text-paper">Мне есть 18, я понимаю условия выше</span>
          </label>
        </div>
        <div className="shrink-0 px-6 pb-1">
          <TelegramLinkCard me={me} compact tight onLinked={reloadMe} />
        </div>
        {/*
          Тарифы — одной горизонтальной полосой, без вертикальной прокрутки.

          Почему: список из трёх карточек в столбик занимал около 280px, и вместе
          с шапкой, чекбоксом 18+, карточкой Telegram и подвалом шит уходил за
          нижний край экрана (на 360×640 это ~650px против ~590 доступных). Человек
          видел верх списка и должен был прокручивать, чтобы добраться до кнопки
          «Оплатить» — то есть главное действие пряталось под сгибом.

          Ширина колонок считается в JS (grid-template-columns по числу тарифов),
          а не фиксированной тройкой классов: если в plans появится четвёртый
          тариф, полоса не перенесётся на вторую строку, а просто станет уже.
          Вертикальной прокрутки у списка нет намеренно — требование «всё в один
          экран», и любая внутренняя прокрутка его нарушает.
        */}
        <ul
          className="grid shrink-0 gap-2 px-6 pb-1"
          style={{ gridTemplateColumns: `repeat(${ordered.length}, minmax(0, 1fr))` }}
        >
          {ordered.map((p) => (
            <li
              key={p.code}
              className="flex min-w-0 flex-col items-center gap-1 rounded-2xl border border-white/10 bg-card px-2 py-3 text-center"
            >
              <p
                className="min-h-[2.4em] text-[11px] font-medium leading-tight text-mist"
                title={NAMES[p.code] || p.code}
              >
                {SHORT[p.code] || NAMES[p.code] || p.code}
              </p>
              <p className="text-xs text-mist">{p.stars_amount} Stars</p>
              <p className="text-base font-semibold leading-none text-gold">
                {p.code === "month_299" && abPrice ? abPrice : p.price_rub}₽
              </p>
              <button
                onClick={() => pay(p.code)}
                disabled={!adult || paying !== null}
                title={adult ? undefined : "Сначала подтверди 18+"}
                className="mt-0.5 w-full rounded-xl bg-gradient-to-br from-gold to-goldsoft px-1 py-2 text-xs font-semibold text-deep active:scale-95 disabled:opacity-40"
              >
                {paying === p.code ? "Создаём…" : "Оплатить"}
              </button>
            </li>
          ))}
        </ul>
        <div className="shrink-0 px-6 pb-4 pt-2">
          <p className="text-center text-[11px] text-mist">Оплата через Telegram Stars.</p>
          <button onClick={onClose} className="mt-1 w-full py-1.5 text-xs text-mist">
            Продолжить бесплатно завтра
          </button>
        </div>
      </motion.div>
    </div>
  );
}
