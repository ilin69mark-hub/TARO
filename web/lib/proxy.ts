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

export function fwdHeaders(req: NextRequest): Record<string, string> {
  const h: Record<string, string> = {
    "Content-Type": "application/json",
    Cookie: req.headers.get("cookie") || "",
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
