"use client";

import { useEffect, useState } from "react";

// Регистрация SW (см. T19). Молча пропускаем ошибки (dev без https).
export default function SWRegister() {
  useEffect(() => {
    if ("serviceWorker" in navigator && window.isSecureContext) {
      navigator.serviceWorker.register("/sw.js").catch(() => undefined);
    }
  }, []);
  return null;
}

// Install-промпт: только после 2-го расклада (см. 03-nonfunctional/06).
// Счетчик чтений — localStorage taro_readings (инкремент в spread-детали).
export function InstallPrompt() {
  const [deferred, setDeferred] = useState<Event | null>(null);
  const [eligible, setEligible] = useState(false);

  useEffect(() => {
    const n = Number(localStorage.getItem("taro_readings") || 0);
    if (n >= 2) setEligible(true);
    const handler = (e: Event) => {
      e.preventDefault();
      setDeferred(e);
    };
    window.addEventListener("beforeinstallprompt", handler);
    return () => window.removeEventListener("beforeinstallprompt", handler);
  }, []);

  if (!eligible || !deferred) return null;
  return (
    <button
      onClick={() => {
        (deferred as unknown as { prompt: () => void }).prompt();
        setDeferred(null);
      }}
      className="fixed bottom-20 left-1/2 z-10 -translate-x-1/2 rounded-2xl border border-gold/40 bg-elev px-5 py-3 text-sm font-semibold text-gold"
    >
      Установить приложение
    </button>
  );
}

// bumpReadingCount — вызывать после каждого done-чтения (см. spread-деталь T16).
export function bumpReadingCount() {
  try {
    const n = Number(localStorage.getItem("taro_readings") || 0) + 1;
    localStorage.setItem("taro_readings", String(n));
  } catch {
    /* ignore */
  }
}
