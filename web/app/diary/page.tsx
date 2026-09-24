"use client";

import { useEffect, useState, Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { api } from "@/lib/api";

// Дневник: список + новая запись (см. V07). Черновик — автосейв локально.
export type DiaryEntry = {
  id: string;
  reading_id: string | null;
  body: string;
  mood: string | null;
  created_at: string;
};

const MOODS = ["up", "down", "calm", "anxious", "grateful"] as const;

export default function DiaryPage() {
  return (
    <Suspense>
      <DiaryInner />
    </Suspense>
  );
}

// V08: ?reading=ID привязывает запись к раскладу.
function DiaryInner() {
  const search = useSearchParams();
  const readingId = search.get("reading");
  const [items, setItems] = useState<DiaryEntry[]>([]);
  const [body, setBody] = useState("");
  const [mood, setMood] = useState<string>("");
  const [filter, setFilter] = useState<string>("");

  async function load(query = "") {
    try {
      setItems(await api.get<DiaryEntry[]>(`/diary?limit=20${query}`));
    } catch {
      setItems([]);
    }
  }

  useEffect(() => {
    try {
      setBody(localStorage.getItem("taro_draft") || "");
    } catch {
      /* ignore */
    }
    load();
  }, []);

  function draft(v: string) {
    setBody(v);
    try {
      localStorage.setItem("taro_draft", v);
    } catch {
      /* ignore */
    }
  }

  async function save() {
    if (!body.trim()) return;
    await api.post("/diary", {
      body: body.slice(0, 10000),
      mood: mood || undefined,
      reading_id: readingId || undefined,
    });
    try {
      localStorage.removeItem("taro_draft");
    } catch {
      /* ignore */
    }
    setBody("");
    load();
  }

  async function download() {
    const { csrf } = await import("@/lib/api");
    const res = await fetch("/api/diary/export", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
      body: "{}",
    });
    if (!res.ok) return;
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "diary.json";
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-3xl font-display font-semibold text-paper">Дневник</h1>
      <p className="mt-2">
        <button onClick={download} className="text-sm text-gold">
          Скачать все записи (JSON)
        </button>
      </p>
      {readingId && (
        <p className="mt-2 text-sm text-gold">Запись к раскладу · мысли сохранятся рядом с картами</p>
      )}
      <textarea
        value={body}
        aria-label="Текст записи"
        onChange={(e) => draft(e.target.value.slice(0, 10000))}
        placeholder="Мысли после расклада…"
        rows={4}
        className="mt-6 w-full rounded-2xl border border-white/10 bg-card p-4 text-base text-paper placeholder:text-mist"
      />
      <div className="mt-2 flex flex-wrap gap-2">
        {MOODS.map((m) => (
          <button
            key={m}
            onClick={() => setMood(mood === m ? "" : m)}
            aria-pressed={mood === m}
            className={`rounded-full border px-3 py-1 text-sm ${
              mood === m ? "border-gold text-gold" : "border-white/10 text-mist"
            }`}
          >
            {m}
          </button>
        ))}
      </div>
      <button
        onClick={save}
        className="mt-4 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
      >
        Сохранить
      </button>
      <ul className="mt-6 space-y-4">
        <li>
          <div className="flex flex-wrap gap-2">
            <span className="py-1 text-sm text-mist">Фильтр:</span>
            {["", ...MOODS].map((m) => (
              <button
                key={m || "all"}
                onClick={() => {
                  setFilter(m);
                  load(m ? `&mood=${m}` : "");
                }}
                className={`rounded-full border px-3 py-1 text-sm ${
                  filter === m ? "border-gold text-gold" : "border-white/10 text-mist"
                }`}
              >
                {m || "все"}
              </button>
            ))}
          </div>
        </li>
        {items.map((it) => (
          <li key={it.id} className="rounded-2xl border border-white/10 bg-card p-5">
            <p className="whitespace-pre-wrap text-base text-paper">{it.body}</p>
            {it.mood && <p className="mt-1 text-sm text-gold">{it.mood}</p>}
          </li>
        ))}
      </ul>
      <p className="mt-4 text-center text-sm text-mist">
        <Link href="/profile" className="text-gold">
          Назад
        </Link>
      </p>
    </main>
  );
}
