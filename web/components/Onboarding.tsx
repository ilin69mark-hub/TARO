"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";
import type { Plan } from "@/components/PaywallSheet";

// Онбординг новичка 3 шага (см. U23): вопрос → карта → честный paywall.
// Показывается 1 раз (taro_onb), skippable на каждом шаге. Цена — из plans (см. D5).
function steps(monthPrice: number) {
  return [
    {
      title: "Шаг 1 — задай вопрос",
      text: "Сформулируй то, что тревожит. Вопрос можно пропустить — карты услышат и так.",
    },
    {
      title: "Шаг 2 — вытяни карту",
      text: "Одна бесплатная карта каждый день. Толкование появится за секунды.",
    },
    {
      title: "Шаг 3 — честный paywall",
      text: `Бесплатного хватает навсегда. Безлимит — только если захочешь глубже: ${monthPrice}₽/мес.`,
    },
  ];
}

export default function Onboarding() {
  const [step, setStep] = useState(-1);
  const [monthPrice, setMonthPrice] = useState(299); // дефолт Книги, уточняем из plans (см. D5)

  useEffect(() => {
    try {
      if (!localStorage.getItem("taro_onb")) setStep(0);
      else return;
    } catch {
      setStep(0);
    }
    api
      .get<Plan[]>("/plans")
      .then((plans) => {
        const m = plans.find((p) => p.code === "month_299");
        if (m) setMonthPrice(m.price_rub);
      })
      .catch(() => undefined);
  }, []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") close();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function close() {
    try {
      localStorage.setItem("taro_onb", "1");
    } catch {
      /* ignore */
    }
    setStep(-1);
  }

  if (step < 0) return null;
  const all = steps(monthPrice);
  const s = all[step];
  const last = step === all.length - 1;
  return (
    <div role="dialog" aria-modal="true" aria-label="Знакомство" className="fixed inset-0 z-30 flex items-center justify-center bg-deep/90 p-6">
      <div className="w-full max-w-sm rounded-3xl border border-gold/40 bg-elev p-6 text-center">
        <p className="text-sm text-mist">
          {step + 1} / {all.length}
        </p>
        <p className="mt-2 text-xl font-semibold text-paper">{s.title}</p>
        <p className="mt-2 text-base text-mist">{s.text}</p>
        <div className="mt-5 flex gap-2">
          <button onClick={close} className="flex-1 rounded-2xl border border-white/10 py-3 text-sm text-mist">
            Пропустить
          </button>
          {last ? (
            <Link
              href="/spreads"
              onClick={close}
              className="flex-1 rounded-2xl bg-gradient-to-br from-gold to-goldsoft py-3 text-center text-sm font-semibold uppercase text-deep"
            >
              Начать
            </Link>
          ) : (
            <button
              onClick={() => setStep(step + 1)}
              className="flex-1 rounded-2xl bg-gradient-to-br from-gold to-goldsoft py-3 text-sm font-semibold uppercase text-deep"
            >
              Далее
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
