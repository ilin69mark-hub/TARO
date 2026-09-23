// Auth-bootstrap web: anon uuid в localStorage → POST /api/auth/anon (см. 02-functional/02).
// TG WebApp initData — если открыты внутри Telegram (см. T16, ADR-04).
"use client";

const KEY = "taro_uuid";

export function getUuid(): string {
  let uuid = localStorage.getItem(KEY);
  if (!uuid) {
    uuid = crypto.randomUUID();
    localStorage.setItem(KEY, uuid);
  }
  return uuid;
}

export async function ensureAuth(): Promise<void> {
  const tg = (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp;
  if (tg?.initData) {
    await fetch("/api/auth/telegram", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-CSRF": "1" },
      body: JSON.stringify({ initData: tg.initData }),
    }).catch(() => undefined);
    return;
  }
  await fetch("/api/auth/anon", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF": "1" },
    body: JSON.stringify({ uuid: getUuid() }),
  }).catch(() => undefined);
}
