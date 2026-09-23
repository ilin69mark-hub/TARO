// Auth-bootstrap web: anon uuid в localStorage → POST /api/auth/anon (см. 02-functional/02).
// TG WebApp initData — если открыты внутри Telegram (см. T16, ADR-04).
// S07: у входов нет сессии → токена нет, шлем пустой X-CSRF; Go проверяет Origin.
"use client";

import { csrf } from "./api";

const KEY = "taro_uuid";
const FPKEY = "taro_fp";

export function getUuid(): string {
  let uuid = localStorage.getItem(KEY);
  if (!uuid) {
    uuid = crypto.randomUUID();
    localStorage.setItem(KEY, uuid);
  }
  return uuid;
}

// S08: отдельный fingerprint браузера (не uuid): кража только uuid без fp не входит.
export function getFp(): string {
  let fp = localStorage.getItem(FPKEY);
  if (!fp) {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    fp = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    localStorage.setItem(FPKEY, fp);
  }
  return fp;
}

export async function ensureAuth(): Promise<void> {
  const tg = (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp;
  if (tg?.initData) {
    await fetch("/api/auth/telegram", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
      body: JSON.stringify({ initData: tg.initData }),
    }).catch(() => undefined);
    return;
  }
  await fetch("/api/auth/anon", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
    body: JSON.stringify({ uuid: getUuid(), fingerprint: getFp() }),
  }).catch(() => undefined);
}
