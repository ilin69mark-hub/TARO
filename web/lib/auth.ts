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
// Аудит B: fp больше не храним в localStorage (XSS забирал пару целиком).
// Порядок: память → legacy localStorage → генерация (только в память).
// Сервер ставит httpOnly cookie taro_fp; после первого успеха чистим legacy.
let memFp = "";
export function getFp(): string {
  if (memFp) return memFp;
  try {
    const legacy = localStorage.getItem(FPKEY);
    if (legacy) {
      memFp = legacy;
      return memFp;
    }
  } catch {
    /* ignore */
  }
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  memFp = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return memFp;
}

function dropLegacyFp(): void {
  memFp = memFp || "";
  try {
    localStorage.removeItem(FPKEY);
  } catch {
    /* ignore */
  }
}

export async function ensureAuth(): Promise<void> {
  const tg = (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp;
  if (tg?.initData) {
    const res = await fetch("/api/auth/telegram", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
      body: JSON.stringify({ initData: tg.initData }),
    }).catch(() => undefined);
    if (res?.ok) dropLegacyFp();
    return;
  }
  const res = await fetch("/api/auth/anon", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
    body: JSON.stringify({ uuid: getUuid(), fingerprint: getFp() }),
  }).catch(() => undefined);
  if (res?.ok) dropLegacyFp();
}
