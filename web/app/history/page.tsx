"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";
import { ensureAuth } from "@/lib/auth";

// История: последние 20, locked blur для старых free, поиск только premium (см. T12/T17).
type Item = { id: string; spread: string; question: string; preview: string; created_at: string };

export default function HistoryPage() {
  const [items, setItems] = useState<Item[]>([]);
  const [q, setQ] = useState("");
  const [qDenied, setQDenied] = useState(false);

  async function load(query: string) {
    setQDenied(false);
    try {
      const list = await api.get<Item[]>(`/readings?limit=20${query ? `&q=${encodeURIComponent(query)}` : ""}`);
      setItems(list);
    } catch (e: unknown) {
      if ((e as Error).message.includes("Поиск")) setQDenied(true);
    }
  }

  useEffect(() => {
    void ensureAuth().then(() => load("")).catch(() => undefined);
  }, []);

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-3xl font-display font-semibold text-paper">История</h1>
      <input
        value={q}
        aria-label="Поиск по вопросу"
        onChange={(e) => setQ(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && load(q)}
        placeholder="Поиск по вопросу (premium)"
        className="mt-4 w-full rounded-2xl border border-white/10 bg-card p-3 text-base text-paper placeholder:text-mist"
      />
      {qDenied && <p className="mt-2 text-sm text-gold">Поиск — для premium.</p>}
      {items.length === 0 && (
        <p className="mt-6 text-base text-mist">Пока пусто. Первый расклад появится здесь.</p>
      )}
      <ul className="mt-6 space-y-4">
        {items.map((it) => (
          <li key={it.id} className="rounded-2xl border border-white/10 bg-card p-5">
            <Link href={`/reading/${it.id}`} className="block">
              <p className="text-base text-paper">{it.question || it.spread}</p>
              <p className="mt-1 line-clamp-2 text-sm text-mist">{it.preview}</p>
            </Link>
          </li>
        ))}
      </ul>
    </main>
  );
}
