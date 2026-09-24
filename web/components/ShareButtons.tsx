"use client";

import { useEffect, useState } from "react";
import { track, events } from "@/lib/analytics";

// Кнопки шеринга результата (см. U13): TG-поделиться + копировать ссылку.
// Приватная ссылка /share/:token (без PII в URL); пока токен грузится — legacy ?q=.
import { csrf } from "@/lib/api";

export default function ShareButtons({
  question,
  spread,
  readingId,
}: {
  question: string;
  spread: string;
  readingId: string;
}) {
  const [copied, setCopied] = useState(false);
  const [token, setToken] = useState("");

  // Токен стабилен на расклад (сервер), StrictMode double-effect безопасен.
  useEffect(() => {
    let live = true;
    fetch("/api/share", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
      body: JSON.stringify({ reading_id: readingId }),
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((j) => {
        if (live && j?.token) setToken(j.token);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [readingId]);

  function link(): string {
    if (token) return new URL(`/share/${token}`, window.location.origin).toString();
    const url = new URL("/share", window.location.origin);
    if (question) url.searchParams.set("q", question.slice(0, 80));
    url.searchParams.set("s", spread.slice(0, 40));
    return url.toString();
  }

  function done() {
    track(events.shareDone, { spread });
    try {
      const n = Number(localStorage.getItem("taro_shares") || 0) + 1;
      localStorage.setItem("taro_shares", String(n));
    } catch {
      /* ignore */
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(link());
      setCopied(true);
      done();
    } catch {
      setCopied(false);
    }
  }

  const tg = `https://t.me/share/url?url=${encodeURIComponent(typeof window !== "undefined" ? link() : "")}`;

  return (
    <div className="mt-4 flex gap-2">
      <a
        href={tg}
        target="_blank"
        rel="noopener"
        onClick={done}
        className="rounded-2xl border border-gold/40 px-4 py-2 text-sm font-semibold text-gold"
      >
        Поделиться в TG
      </a>
      <button
        onClick={copy}
        className="rounded-2xl border border-white/10 px-4 py-2 text-sm text-paper"
      >
        {copied ? "Скопировано!" : "Скопировать ссылку"}
      </button>
    </div>
  );
}
