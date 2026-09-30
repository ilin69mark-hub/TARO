"use client";

import { useEffect, useState, Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { api } from "@/lib/api";
import { ensureAuth } from "@/lib/auth";
import CardArt from "@/components/CardArt";

// Дневник: список + новая запись (см. V07). Черновик — автосейв локально.
export type DiaryEntry = {
  id: string;
  reading_id: string | null;
  body: string;
  mood: string | null;
  created_at: string;
  // Снимок расклада (LEFT JOIN в Go): чтобы из дневника можно было и прочитать
  // толкование, и снова посмотреть карты, не открывая исходное чтение.
  question?: string | null;
  spread_code?: string | null;
  cards?: { card_id: number; position: number; name_ru: string; image_key: string; reversed: boolean }[];
  interpretation?: string | null;
  reading_locked?: boolean | null;
};

// Настроение хранится в БД по-английски (CHECK в миграции 008) — менять схему
// ради локализации незачем. Переводим только интерфейс.
const MOODS = [
  { key: "up", label: "Хорошо" },
  { key: "down", label: "Тяжело" },
  { key: "calm", label: "Спокойно" },
  { key: "anxious", label: "Тревожно" },
  { key: "grateful", label: "Благодарно" },
] as const;

const MOOD_LABEL: Record<string, string> = Object.fromEntries(
  MOODS.map((m) => [m.key, m.label])
);

// artCards — карты, у которых есть арт. У части чтений (созданных до поставки
// артов) в JSON лежит только card_id без image_key: рисовать их нельзя,
// CardArt нарисует пустую рубашку и испортит вид записи.
function artCards(e: DiaryEntry) {
  return (e.cards || []).filter((c) => !!c.image_key);
}

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
    void ensureAuth().then(() => load()).catch(() => undefined);
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

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-center text-3xl font-display font-semibold text-paper">Дневник</h1>
      {readingId ? (
        <p className="mt-2 text-sm text-gold">
          Запись к раскладу ·{" "}
          <Link href={`/reading/${readingId}`} className="underline">
            вернуться к раскладу
          </Link>
        </p>
      ) : (
        <p className="mt-2 text-sm text-mist">
          Написать о раскладе можно из самого расклада — кнопкой «Записать мысли».
        </p>
      )}
      <textarea
        value={body}
        aria-label="Текст записи"
        onChange={(e) => draft(e.target.value.slice(0, 10000))}
        placeholder="Мысли после расклада…"
        rows={4}
        className="mt-6 w-full rounded-2xl border border-white/10 bg-card p-4 text-base text-paper placeholder:text-mist"
      />
      <div className="mt-2 flex flex-wrap gap-2" role="group" aria-label="Настроение">
        {MOODS.map((m) => (
          <button
            key={m.key}
            onClick={() => setMood(mood === m.key ? "" : m.key)}
            aria-pressed={mood === m.key}
            className={`rounded-full border px-3 py-1 text-sm ${
              mood === m.key ? "border-gold text-gold" : "border-white/10 text-mist"
            }`}
          >
            {m.label}
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
          <div className="flex flex-wrap gap-2" role="group" aria-label="Фильтр по настроению">
            <span className="py-1 text-sm text-mist">Фильтр:</span>
            <button
              onClick={() => {
                setFilter("");
                load("");
              }}
              aria-pressed={filter === ""}
              className={`rounded-full border px-3 py-1 text-sm ${
                filter === "" ? "border-gold text-gold" : "border-white/10 text-mist"
              }`}
            >
              все
            </button>
            {MOODS.map((m) => (
              <button
                key={m.key}
                onClick={() => {
                  setFilter(m.key);
                  load(`&mood=${m.key}`);
                }}
                aria-pressed={filter === m.key}
                className={`rounded-full border px-3 py-1 text-sm ${
                  filter === m.key ? "border-gold text-gold" : "border-white/10 text-mist"
                }`}
              >
                {m.label}
              </button>
            ))}
          </div>
        </li>
        {items.length === 0 && (
          <li className="text-sm text-mist">Записей пока нет. Они появятся здесь после сохранения.</li>
        )}
        {items.map((it) => (
          <li key={it.id} className="rounded-2xl border border-white/10 bg-card p-5">
            <p className="whitespace-pre-wrap text-base text-paper">{it.body}</p>
            {it.mood && <p className="mt-1 text-sm text-gold">{MOOD_LABEL[it.mood] || it.mood}</p>}
            {/* Снимок расклада: карты и толкование видны прямо в записи,
                поэтому мысли читаются вместе с тем, что их вызвало. */}
            {it.reading_id && (artCards(it).length || it.question) && (
              <div className="mt-3 border-t border-white/10 pt-3">
                {it.question && <p className="text-sm text-mist">Расклад: {it.question}</p>}
                {/* Карты без image_key (чтения, созданные до поставки артов)
                    не рисуем: CardArt показал бы пустую рубашку. */}
                {artCards(it).length > 0 && (
                  <ul className="mt-2 flex flex-wrap gap-2">
                    {artCards(it).map((c) => (
                      <li key={`${it.id}-${c.position}`} className="w-16">
                        <CardArt
                          name={c.name_ru || `Карта ${c.card_id}`}
                          imageKey={c.image_key}
                          width={64}
                          className="h-auto w-full"
                        />
                        <p className="mt-0.5 text-center text-[0.65rem] text-mist">
                          {c.reversed ? "↺" : ""}
                        </p>
                      </li>
                    ))}
                  </ul>
                )}
                {it.interpretation && (
                  <p className="mt-2 whitespace-pre-wrap text-sm leading-relaxed text-paper/90">
                    {it.interpretation}
                  </p>
                )}
                <Link
                  href={`/reading/${it.reading_id}`}
                  className="mt-2 inline-block text-sm text-gold underline"
                >
                  Открыть расклад целиком →
                </Link>
              </div>
            )}
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
