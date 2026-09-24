"use client";

import { useState } from "react";

import { csrf } from "../lib/api";

// Web-push opt-in (см. U21): подписка через PushManager, VAPID-ключ с /api/push/public.
// Только opt-in, тихий отказ при отсутствии поддержки.
export default function PushOptIn() {
  const [state, setState] = useState<"idle" | "done" | "denied" | "unsupported">("idle");

  async function subscribe() {
    try {
      if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
        setState("unsupported");
        return;
      }
      const perm = await Notification.requestPermission();
      if (perm !== "granted") {
        setState("denied");
        return;
      }
      const reg = await navigator.serviceWorker.ready;
      const existing = await reg.pushManager.getSubscription();
      if (existing) await existing.unsubscribe();
      const { key } = await fetch("/api/push/public").then((r) => r.json());
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlToU8(key),
      });
      const p256dh = b64(sub.getKey("p256dh")!);
      const auth = b64(sub.getKey("auth")!);
      await fetch("/api/push/subscribe", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
        body: JSON.stringify({ endpoint: sub.endpoint, p256dh, auth }),
      });
      setState("done");
    } catch {
      setState("denied");
    }
  }

  if (state === "done") return <p className="text-sm text-mist">Пуши включены 🌙</p>;
  if (state === "unsupported") return null;
  return (
    <button
      onClick={subscribe}
      className="rounded-2xl border border-white/10 px-4 py-2 text-sm text-paper"
    >
      {state === "denied" ? "Пуши недоступны" : "Включить вечерние напоминания"}
    </button>
  );
}

function b64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function urlToU8(base64: string): Uint8Array {
  const pad = "=".repeat((4 - (base64.length % 4)) % 4);
  const b = (base64 + pad).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
