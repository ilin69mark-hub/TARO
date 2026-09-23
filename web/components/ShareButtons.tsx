"use client";

import { useState } from "react";
import { track, events } from "@/lib/analytics";

// Кнопки шеринга результата (см. U13): TG-поделиться + копировать ссылку.
// Ссылка ведет на /share (превью без толкования, см. U12). share_done — в аналитику (U14).
export default function ShareButtons({ question, spread }: { question: string; spread: string }) {
  const [copied, setCopied] = useState(false);

  function link(): string {
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
