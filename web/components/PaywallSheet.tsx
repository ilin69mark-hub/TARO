"use client";

import { useEffect, useState } from "react";
import { motion } from "framer-motion";
import { paywallHits, csrf } from "@/lib/api";

// Paywall-sheet: snap снизу, цены из plans (не хардкод, см. 02-functional/05, T18).
export type Plan = { code: string; price_rub: number; stars_amount: number; duration_days: number | null };

const NAMES: Record<string, string> = {
  month_299: "Безлимит на месяц",
  year_2490: "Безлимит на год −30%",
  single_99: "Разовый premium-расклад",
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
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Оформление безлимита"
      className="fixed inset-0 z-20 flex items-end justify-center bg-deep/70"
      onClick={onClose}
    >
      <motion.div
        initial={{ y: "100%" }}
        animate={{ y: 0 }}
        transition={{ type: "spring", damping: 28, stiffness: 300 }}
        className="w-full max-w-md rounded-t-3xl border-t border-gold/40 bg-elev p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <p className="text-xl font-semibold text-paper">Заглянем глубже?</p>
        <p className="mt-1 text-sm text-mist">На сегодня бесплатные карты закончились</p>
        <label className="mt-3 flex cursor-pointer items-center gap-2 rounded-2xl border border-white/10 p-3">
          <input
            type="checkbox"
            checked={adult}
            onChange={(e) => confirmAdult(e.target.checked)}
            className="h-5 w-5 accent-[#D4AF37]"
          />
          <span className="text-sm text-paper">Мне есть 18, я понимаю условия выше</span>
        </label>
        <ul className="mt-4 space-y-3">
          {ordered.map((p) => (
            <li
              key={p.code}
              className="flex items-center justify-between gap-2 rounded-2xl border border-white/10 bg-card p-4"
            >
              <div>
                <p className="text-base text-paper">{NAMES[p.code] || p.code}</p>
                <p className="text-sm text-mist">{p.stars_amount} Stars</p>
              </div>
              <div className="text-right">
                <p className="text-lg font-semibold text-gold">
                  {p.code === "month_299" && abPrice ? abPrice : p.price_rub}₽
                </p>
                <button
                  onClick={() => pay(p.code)}
                  disabled={!adult || paying !== null}
                  title={adult ? undefined : "Сначала подтверди 18+"}
                  className="mt-1 rounded-xl bg-gradient-to-br from-gold to-goldsoft px-4 py-1.5 text-sm font-semibold text-deep active:scale-95 disabled:opacity-40"
                >
                  {paying === p.code ? "Создаём счёт…" : "Оплатить"}
                </button>
              </div>
            </li>
          ))}
        </ul>
        <p className="mt-4 text-xs text-mist">Оплата через Telegram Stars.</p>
        <button onClick={onClose} className="mt-2 w-full py-2 text-sm text-mist">
          Продолжить бесплатно завтра
        </button>
      </motion.div>
    </div>
  );
}
