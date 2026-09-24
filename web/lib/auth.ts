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

type AuthPayload = { initData?: string; uuid?: string; fingerprint?: string };

function authPayload(initData?: string): AuthPayload {
  const body: AuthPayload = initData ? { initData } : { uuid: getUuid() };
  const fingerprint = getFp();
  if (fingerprint) body.fingerprint = fingerprint;
  return body;
}

async function authenticate(): Promise<void> {
  const tg = (window as unknown as { Telegram?: { WebApp?: { initData?: string } } }).Telegram?.WebApp;
  const initData = tg?.initData;
  const res = await fetch(initData ? "/api/auth/telegram" : "/api/auth/anon", {
    method: "POST",
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
    body: JSON.stringify(authPayload(initData)),
  });
  if (!res.ok) throw new Error(`Auth failed: ${res.status}`);
  rememberAuthFp();
}

export function ensureAuth(): Promise<void> {
  if (authPromise) return authPromise;
  let pending: Promise<void>;
  pending = authenticate().catch((error: unknown) => {
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
