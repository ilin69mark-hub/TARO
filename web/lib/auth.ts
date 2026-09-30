// Auth-bootstrap web: anon uuid в localStorage → POST /api/auth/anon (см. 02-functional/02).
// TG WebApp initData — если открыты внутри Telegram (см. T16, ADR-04).
// S07: у входов нет сессии → токена нет, шлем пустой X-CSRF; Go проверяет Origin.
"use client";

import { csrf } from "./api";

const KEY = "taro_uuid";
const FPKEY = "taro_fp";
const FP_READY_KEY = "taro_fp_ready";
const FP_PENDING_KEY = "taro_fp_pending";

let memFp = "";
let authPromise: Promise<void> | null = null;

function storage(name: "localStorage" | "sessionStorage"): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window[name];
  } catch {
    return null;
  }
}

function readValue(store: Storage | null, key: string): string {
  try {
    return store?.getItem(key) || "";
  } catch {
    return "";
  }
}

function writeValue(store: Storage | null, key: string, value: string): void {
  try {
    store?.setItem(key, value);
  } catch {
    return;
  }
}

function removeValue(store: Storage | null, key: string): void {
  try {
    store?.removeItem(key);
  } catch {
    return;
  }
}

export function getUuid(): string {
  const store = storage("localStorage");
  let uuid = readValue(store, KEY);
  if (!uuid) {
    uuid = crypto.randomUUID();
    writeValue(store, KEY, uuid);
  }
  return uuid;
}

function fingerprintReady(): boolean {
  return readValue(storage("localStorage"), FP_READY_KEY) === "1";
}

export function getFp(): string {
  if (memFp) return memFp;
  const local = storage("localStorage");
  const legacy = readValue(local, FPKEY);
  if (legacy) {
    memFp = legacy;
    return memFp;
  }
  const session = storage("sessionStorage");
  const pending = readValue(session, FP_PENDING_KEY);
  if (pending) {
    memFp = pending;
    return memFp;
  }
  if (fingerprintReady()) return "";
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  memFp = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  writeValue(session, FP_PENDING_KEY, memFp);
  return memFp;
}

function rememberAuthFp(): void {
  const local = storage("localStorage");
  const session = storage("sessionStorage");
  writeValue(local, FP_READY_KEY, "1");
  removeValue(local, FPKEY);
  removeValue(session, FP_PENDING_KEY);
}

// forgetAnonIdentity — сброс анонимной личности целиком.
//
// Зачем: fingerprint живёт в httpOnly-cookie taro_fp, а localStorage хранит только
// маркер taro_fp_ready=1. Если cookie потеряна (частичная чистка данных, eviction,
// не-Secure-контекст), getFp() возвращает "" и больше НИКОГДА не сгенерирует новый:
// маркер готовности стоит, а копии значения нет. Go отвечает 403 FP_REQUIRED.
// Восстановиться генерацией тоже нельзя — в БД у uuid уже записан старый
// fingerprint, и сервер вернёт FP_MISMATCH. Прежде это был тупик: пользователь
// навсегда терял доступ к своим чтениям, не выходя из стартовой сессии.
//
// Поэтому 403 от привязки к устройству трактуется как «эта личность больше не
// восстанавливается» и начинается новая анонимная личность. Антифрод не
// ослаблен: подделать чужой fingerprint по-прежнему нельзя, мы просто перестаём
// пытаться и заводим нового пользователя.
function forgetAnonIdentity(): void {
  const local = storage("localStorage");
  const session = storage("sessionStorage");
  removeValue(local, KEY);
  removeValue(local, FP_READY_KEY);
  removeValue(local, FPKEY);
  removeValue(session, FP_PENDING_KEY);
  memFp = "";
}

// FP-привязка терминальна: ни FP_REQUIREED, ни FP_MISMATCH не лечатся повтором с
// тем же fingerprint, поэтому на них нужен сброс личности, а не ретрай.
const FP_LOCKOUT = new Set(["FP_REQUIRED", "FP_MISMATCH"]);

async function isFpLockout(res: Response): Promise<boolean> {
  if (res.status !== 403) return false;
  try {
    const parsed = (await res.clone().json()) as { error?: { code?: string } };
    return FP_LOCKOUT.has(parsed?.error?.code || "");
  } catch {
    return false;
  }
}

type AuthPayload = { initData?: string; uuid?: string; fingerprint?: string };

function authPayload(initData?: string): AuthPayload {
  const body: AuthPayload = initData ? { initData } : { uuid: getUuid() };
  const fingerprint = getFp();
  if (fingerprint) body.fingerprint = fingerprint;
  return body;
}

async function authenticate(allowIdentityReset: boolean): Promise<void> {
  const tg = (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp;
  const initData = tg?.initData;
  const res = await fetch(initData ? "/api/auth/telegram" : "/api/auth/anon", {
    method: "POST",
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
    body: JSON.stringify(authPayload(initData)),
  });
  if (!res.ok) {
    // Ровно одна попытка с новой личностью: allowIdentityReset снимается, чтобы
    // при persistent-403 не уйти в бесконечный цикл запросов.
    if (allowIdentityReset && (await isFpLockout(res))) {
      forgetAnonIdentity();
      return authenticate(false);
    }
    throw new Error(`Auth failed: ${res.status}`);
  }
  rememberAuthFp();
}

export function ensureAuth(): Promise<void> {
  if (authPromise) return authPromise;
  let pending: Promise<void>;
  pending = authenticate(true).catch((error: unknown) => {
    if (authPromise === pending) authPromise = null;
    throw error;
  });
  authPromise = pending;
  return pending;
}

export function retryAuth(): Promise<void> {
  authPromise = null;
  return ensureAuth();
}
