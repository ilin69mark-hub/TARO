// API-клиент web→same-origin /api (см. T16, 04-architecture/02, S07).
// CSRF: per-session токен из cookie taro_csrf (ставит Go при login, см. issueCSRF).
// Прокси его НЕ инжектит — форвардит клиентский (см. S07).
import { track, events } from "./analytics";

export function csrf(): string {  try {
    const m = document.cookie.match(/(?:^|;\s*)taro_csrf=([^;]*)/);
    return m ? decodeURIComponent(m[1]) : "";
  } catch {
    return "";
  }
}

async function req(path: string, init: RequestInit = {}) {
  const res = await fetch(`/api${path}`, {
    credentials: "include",
    ...init,
    headers: { "Content-Type": "application/json", "X-CSRF": csrf(), ...(init.headers || {}) },
  });
  if (res.status === 402) {
    const body = await res.json().catch(() => ({}));
    track(events.paywallShow, { plan: "unknown" });
    bumpPaywalls(); // U26: счетчик упоров для single-промо
    const err: Error & { paywall?: unknown; code?: string } = new Error("Лимит исчерпан");
    err.code = "LIMIT_EXCEEDED";
    err.paywall = body.paywall;
    throw err;
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error?.message_ru || `Ошибка ${res.status}`);
  }
  return res;
}

export const api = {
  get: <T>(path: string) => req(path).then((r) => r.json() as Promise<T>),
  post: <T>(path: string, body: unknown) =>
    req(path, { method: "POST", body: JSON.stringify(body) }).then((r) => r.json() as Promise<T>),
};

/** POST /v1/readings с SSE: onToken per token, возвращает финальный фрейм. */
export function paywallHits(): number {
  try {
    return Number(localStorage.getItem("taro_paywalls") || 0);
  } catch {
    return 0;
  }
}

function bumpPaywalls(): void {
  try {
    localStorage.setItem("taro_paywalls", String(paywallHits() + 1));
  } catch {
    /* ignore */
  }
}export async function postReadingSSE(
  body: { spread_code: string; question?: string; idempotency_key: string },
  onToken: (t: string) => void
): Promise<{ reading_id: string; status: string }> {
  const res = await fetch("/api/readings", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream", "X-CSRF": csrf() },
    body: JSON.stringify(body),
  });
  if (res.status === 402) {
    track(events.paywallShow, { plan: "unknown" });
    const err: Error & { code?: string } = new Error("Лимит исчерпан");
    err.code = "LIMIT_EXCEEDED";
    throw err;
  }
  if (!res.ok || !res.body) throw new Error(`Ошибка ${res.status}`);
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let readingId = "";
  let status = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const frames = buf.split("\n\n");
    buf = frames.pop() || "";
    for (const f of frames) {
      const line = f.trim();
      if (!line.startsWith("data:")) continue;
      const payload = JSON.parse(line.slice(5).trim());
      if (payload.token) onToken(payload.token);
      if (payload.done) {
        readingId = payload.reading_id;
        status = payload.status;
      }
    }
  }
  track(events.readingDone, { reading_id: readingId, spread_code: body.spread_code });
  return { reading_id: readingId, status };
}

export type Spread = {
  code: string;
  name: string;
  positions: { label: string; meaning: string }[];
  is_premium: boolean;
};

export type Reading = {
  id: string;
  spread: string;
  question: string;
  cards: { card_id: number; name_ru?: string; image_key?: string; reversed: boolean; position: number }[];
  interpretation: string;
  locked: boolean;
  status: string;
};
