"use client";

import { useEffect, useState } from "react";

// 18+ gate: модалка при первом входе + запись age_confirmed_at (см. 08-risks/02, T15/T20).
export default function AgeGate() {
  const [show, setShow] = useState(false);

  useEffect(() => {
    try {
      if (!localStorage.getItem("taro_age")) setShow(true);
    } catch {
      setShow(true);
    }
  }, []);

  async function confirm() {
    try {
      localStorage.setItem("taro_age", "1");
      await fetch("/api/me/age", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-CSRF": "1" },
        body: JSON.stringify({ confirmed: true }),
      }).catch(() => undefined);
    } catch {
      /* offline — локальная метка уже стоит */
    }
    setShow(false);
  }

  if (!show) return null;
  return (
    <div role="dialog" aria-modal="true" aria-label="Подтверждение возраста" className="fixed inset-0 z-30 flex items-center justify-center bg-deep/90 p-6">
      <div className="w-full max-w-sm rounded-3xl border border-gold/40 bg-elev p-6 text-center">
        <p className="text-xl font-semibold text-paper">Тебе есть 18?</p>
        <p className="mt-2 text-sm text-mist">
          Онлайн Таро — инструмент самопознания, не медицинская услуга.
        </p>
        <button
          onClick={confirm}
          className="mt-5 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
        >
          Да, мне есть 18
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
