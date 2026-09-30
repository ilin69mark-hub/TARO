"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, Reading } from "@/lib/api";

// Толкование на экране расклада. Мы переходим сюда сразу после получения
// reading_id, а текст ещё генерируется — значит, серверный компонент отдаст
// пустое толкование один раз и больше не обновится. Этот компонент дожидается
// готовности опросом.
//
// Опрос, а не SSE: страница открывается и по прямой ссылке (шэринг, история),
// где потока уже нет. Плюс GET /api/readings/:id — единственный путь, который
// работает без открытого соединения.
const PENDING = new Set(["pending", "pending_fallback"]);

export default function InterpretationLive({
  readingId,
  initial,
  locked,
}: {
  readingId: string;
  initial: string;
  locked: boolean;
}) {
  const [text, setText] = useState(initial);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    // Закрытый расклад всё равно не покажем: опрос ради текста, которого
    // пользователь не увидит, — лишние запросы к API.
    if (locked || initial.trim()) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      try {
        const r = await api.get<Reading>(`/readings/${readingId}`);
        if (!alive) return;
        const ready = r.interpretation?.trim();
        if (ready) {
          setText(r.interpretation);
          return; // готово — опрос прекращаем
        }
        // Статус «filtered»/«done» с пустым текстом — ждать бессмысленно.
        if (r.status && !PENDING.has(r.status)) {
          setFailed(true);
          return;
        }
      } catch {
        if (!alive) return;
        setFailed(true);
        return;
      }
      if (alive) timer = setTimeout(tick, 1200);
    };

    void tick();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [readingId, initial, locked]);

  if (locked) {
    return (
      <p className="text-sm text-mist">
        Полный текст доступен в день расклада или с безлимитом.{" "}
        <Link href="/profile" className="text-gold">
          Тарифы →
        </Link>
      </p>
    );
  }
  if (text.trim()) {
    return <p className="whitespace-pre-wrap text-sm leading-relaxed text-paper">{text}</p>;
  }
  return (
    <p aria-live="polite" className="text-sm leading-relaxed text-mist">
      {failed ? "Толкование не пришло. Обнови страницу чуть позже." : "Читаем карты…"}
    </p>
  );
}
