import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

const HOP_BY_HOP_HEADERS = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

type HeadersWithSetCookie = Headers & {
  getSetCookie?: () => string[];
  raw?: () => Record<string, string[]>;
};

function normalizeOrigin(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (url.username || url.password) return null;
    return url.origin;
  } catch {
    return null;
  }
}

function proxyOrigin(): string {
  return (
    normalizeOrigin(process.env.PUBLIC_ORIGIN) ||
    normalizeOrigin(process.env.NEXT_PUBLIC_BASE_URL) ||
    normalizeOrigin(process.env.INTERNAL_ORIGIN) ||
    "http://localhost:3000"
  );
}

/**
 * realIP — адрес клиента от nginx.
 *
 * Доверяем ТОЛЬКО `X-Real-IP`, который ставит nginx (`$remote_addr`). Первый
 * элемент `X-Forwarded-For` клиент подделывает сам (nginx дописывает реальный
 * адрес в конец), поэтому XFF не используем.
 *
 * Раньше это правило было продублировано ровно в одном роуте (`auth/anon`),
 * а `fwdHeaders` его не пробрасывал. Последствия были неочевидны:
 *   - антиферма рефералки получала пустой IP — IP-сигнал и кластерный
 *     IP-счётчик молча работали вхолостую (юнит-тесты зелёные, т.к. звали
 *     Go-хендлер напрямую с заголовком; поймал только живый прогон);
 *   - `ratelimit` с `byUser=false` (/v1/auth/, /v1/spreads, /v1/share) ключевал
 *     по IP, а IP был пустым — все анонимные юзеры делили ОДИН бакет.
 *
 * Поэтому правило живёт здесь и вызывается из fwdHeaders, то есть на всех
 * прокси-роутах сразу.
 */
export function realIP(req: NextRequest): string {
  const v = req.headers.get("x-real-ip");
  if (!v) return "unknown";
  return v.split(",")[0].trim() || "unknown";
}

export function fwdHeaders(req: NextRequest): Record<string, string> {
  const h: Record<string, string> = {
    "Content-Type": "application/json",
    Cookie: req.headers.get("cookie") || "",
    "X-Real-IP": realIP(req),
  };
  for (const k of ["X-CSRF", "Idempotency-Key"]) {
    const v = req.headers.get(k);
    if (v) h[k] = v;
  }
  const origin = proxyOrigin();
  if (origin) {
    h.Origin = origin;
    h.Referer = origin;
    h["X-Forwarded-Host"] = new URL(origin).host;
  }
  return h;
}

function setCookieValues(headers: Headers): string[] {
  const source = headers as HeadersWithSetCookie;
  let values: string[] = [];
  if (typeof source.getSetCookie === "function") {
    try {
      values = source.getSetCookie();
    } catch {
      values = [];
    }
  }
  if (values.length) return values;
  const raw = rawSetCookieValues(source);
  if (raw) return raw;
  const value = headers.get("set-cookie");
  return value ? [value] : [];
}

function rawSetCookieValues(headers: HeadersWithSetCookie): string[] | null {
  if (typeof headers.raw !== "function") return null;
  try {
    const raw = headers.raw();
    for (const [key, values] of Object.entries(raw)) {
      if (key.toLowerCase() === "set-cookie" && values.length) return values;
    }
  } catch {
    return null;
  }
  return null;
}

export function copyResponseHeaders(source: Headers, target: Headers): void {
  const cookies = setCookieValues(source);
  source.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (lower === "set-cookie") {
      if (cookies.length === 0) target.append(key, value);
      return;
    }
    if (!HOP_BY_HOP_HEADERS.has(lower)) target.set(key, value);
  });
  for (const cookie of cookies) target.append("set-cookie", cookie);
}

export async function passThrough(res: Response) {
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  copyResponseHeaders(res.headers, out.headers);
  return out;
}

// relay — ЕДИНАЯ точка возврата ответа Go в браузер (A20/F-13).
//
// До A20 каждый роут собирал NextResponse руками и копировал только
// content-type (иногда вместе с Content-Disposition). Терялись:
//   - Set-Cookie — вместе с ним ротация CSRF и обновление сессии: браузер
//     оставался со старым токеном до 401, и «прозрачный» прокси переставал быть
//     прозрачным;
//   - Retry-After — при 429 клиент не знал, когда повторить;
//   - любые будущие заголовки Go (X-Request-Id, RateLimit-*), о которых
//     Next-роут не знал и потому молча срезал их.
//
// Роуты обязаны звать relay/relayStream, а не собирать ответ сами. hop-by-hop
// заголовки (Connection, Transfer-Encoding и прочие) по-прежнему не копируются:
// они относятся к соединению Go↔Next, а не к ответу клиенту.
export function relay(res: Response): Promise<NextResponse> {
  return passThrough(res);
}

// relayStream — то же для потоковых ответов (SSE чтения). Тело не буферизуется:
// буферизация съела бы весь «живой» поток и держала бы соединение до конца
// генерации.
export function relayStream(res: Response): NextResponse {
  const out = new NextResponse(res.body, { status: res.status });
  copyResponseHeaders(res.headers, out.headers);
  out.headers.set("Cache-Control", "no-store");
  return out;
}

// relayJSON — буферизованный ответ с принудительным no-store. Для приватных
// данных (история, дневник, профиль) кэш прокси недопустим даже если Go забыл
// про Cache-Control.
export async function relayJSON(res: Response): Promise<NextResponse> {
  const out = await passThrough(res);
  out.headers.set("Cache-Control", "no-store");
  return out;
}

// bodyTooLarge — 413 до чтения тела (аудит D: 10MB initData буферились Нодой целиком).
// Без content-length пропускаем (Go отрежет своим 1MB) — ложных 413 не будет.
export function bodyTooLarge(req: NextRequest, limit = 1_048_576): boolean {
  const len = req.headers.get("content-length");
  if (len === null) return false;
  const n = Number(len);
  return !Number.isFinite(n) || n < 0 || n > limit;
}

export function tooLarge() {
  return NextResponse.json({ error: { message_ru: "Слишком большое тело" } }, { status: 413 });
}
