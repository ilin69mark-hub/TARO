"use client";

import { useEffect, useState } from "react";

// E11-режим «честный»: 18+ enforced только на оплате (сервер, age_confirmed_at).
// Эта модалка — НЕ гейт и НЕ проверка возраста, а разовое напоминание (см. OWNER E11).
// Флаг taro_notice — только «показано», ничего не подтверждает.
export default function AgeGate() {
  const [show, setShow] = useState(false);

  useEffect(() => {
    try {
      if (!localStorage.getItem("taro_notice")) setShow(true);
    } catch {
      setShow(true);
    }
  }, []);

  function dismiss() {
    try {
      localStorage.setItem("taro_notice", "1");
    } catch {
      /* ignore */
    }
    setShow(false);
  }

  if (!show) return null;
  return (
    <div role="dialog" aria-modal="true" aria-label="Важная информация" className="fixed inset-0 z-30 flex items-center justify-center bg-deep/90 p-6">
      <div className="w-full max-w-sm rounded-3xl border border-gold/40 bg-elev p-6 text-center">
        <p className="text-xl font-semibold text-paper">Прежде чем начать</p>
        <p className="mt-2 text-sm text-mist">
          Онлайн Таро — инструмент самопознания и рефлексии. Не является медицинской,
          психологической, юридической или финансовой услугой. Решения принимаете вы.
          Оплата доступна строго с 18 лет.
        </p>
        <button
          onClick={dismiss}
          className="mt-5 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
        >
          Понятно
        </button>
      </div>
    </div>
  );
}

// applyCalm — «Спокойный режим»: гасит анимации (см. 04-a11y.md; полный маппинг R3F — T28).
export function applyCalm(on: boolean) {
  try {
    localStorage.setItem("taro_calm", on ? "1" : "0");
  } catch {
    /* ignore */
  }
  document.documentElement.classList.toggle("calm", on);
  if (on) window.dispatchEvent(new Event("taro:calm")); // R3F → Lite (см. CinematicBackdrop)
}

export function isCalm(): boolean {
  try {
    return (
      localStorage.getItem("taro_calm") === "1" ||
      window.matchMedia("(prefers-reduced-motion: reduce)").matches
    );
  } catch {
    return false;
  }
}
