"use client";

import { useEffect, useState } from "react";

// bumpReadingCount — вызывать после каждого done-чтения (см. spread-деталь T16).
export function bumpReadingCount() {
  try {
    const n = Number(localStorage.getItem("taro_readings") || 0) + 1;
    localStorage.setItem("taro_readings", String(n));
  } catch {
    /* ignore */
  }
}

// Регистрация SW (см. T19). Молча пропускаем ошибки (dev без https).
export default function SWRegister() {
  useEffect(() => {
    if ("serviceWorker" in navigator && window.isSecureContext) {
      navigator.serviceWorker.register("/sw.js").catch(() => undefined);
    }
  }, []);
  return null;
}

// InstallPrompt: кнопка установки приложения. Живёт в профиле, а НЕ плавающей
// поверх контента: раньше она была fixed поверх таб-бара и закрывала текст
// расклада — то есть ровно то, ради чего пользователь пришёл.
//
// Событие beforeinstallprompt одноразовое: если его не поймать заранее, кнопка
// не появится никогда, поэтому слушатель ставится сразу при монтировании, а
// eligibility (от 2-го расклада) влияет только на ВИДИМОСТЬ готового пункта.
export function useInstallPrompt() {
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

  // Отдельные флаги: «браузер умеет ставить» и «пользователь уже наиграл».
  // Второе не должно прятать первое отладочно — поэтому canInstall видно всегда.
  return {
    canInstall: deferred !== null,
    eligible,
    promptInstall: () => {
      (deferred as unknown as { prompt: () => void } | null)?.prompt();
      setDeferred(null);
    },
  };
}
