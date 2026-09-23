"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { postReadingSSE, api, Spread } from "@/lib/api";
import { track, events } from "@/lib/analytics";
import { bumpReadingCount } from "@/components/PWA";
import PaywallSheet, { Plan } from "@/components/PaywallSheet";

// Экран расклада: вопрос → SSE-стрим → результат (см. 02-functional/03, T16).
// 3D и анимации вытягивания — T21–T28; здесь Lite-флоу ≤3 клика.
export default function SpreadDetail({ params }: { params: { code: string } }) {
  const [question, setQuestion] = useState("");
  const [text, setText] = useState("");
  const [readingId, setReadingId] = useState("");
  const [busy, setBusy] = useState(false);
  const [paywall, setPaywall] = useState(false);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [abPrice, setAbPrice] = useState(0);
  const [error, setError] = useState("");
  const [spreadName, setSpreadName] = useState(params.code);

  useEffect(() => {
    track(events.spreadOpen, { spread_code: params.code });
    api
      .get<Spread[]>("/spreads")
      .then((list) => {
        const found = list.find((s) => s.code === params.code);
        if (found) setSpreadName(found.name);
      })
      .catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function openPaywall() {
    setPaywall(true);
    try {
      setPlans(await api.get<Plan[]>("/plans"));
      const ab = await api.get<{ variant: string; price_rub: number }>("/ab/me");
      setAbPrice(ab.price_rub || 0);
    } catch {
      setPlans([]);
    }
  }

  async function draw() {
    setBusy(true);
    setText("");
    setPaywall(false);
    setError("");
    setReadingId("");
    try {
      const key = crypto.randomUUID();
      const res = await postReadingSSE(
        { spread_code: params.code, question: question || undefined, idempotency_key: key },
        (t) => setText((prev) => prev + t)
      );
      setReadingId(res.reading_id);
      bumpReadingCount(); // install-промпт после 2-го (см. T19)
    } catch (e: unknown) {
      const err = e as { code?: string; message?: string };
      if (err.code === "LIMIT_EXCEEDED") openPaywall();
      else setError(err.message || "Не получилось вытянуть карты");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <Link href="/spreads" className="text-sm text-mist">
        ← Все расклады
      </Link>
      <h1 className="mt-2 text-3xl font-semibold text-paper">{spreadName}</h1>
      <label htmlFor="reading-question" className="mt-6 block text-sm text-mist">
        Твой вопрос
      </label>
      <textarea
        id="reading-question"
        value={question}
        onChange={(e) => setQuestion(e.target.value.slice(0, 500))}
        placeholder="Твой вопрос (необязательно, до 500 символов)"
        rows={3}
        maxLength={500}
        className="mt-6 w-full rounded-2xl border border-white/10 bg-card p-4 text-base text-paper placeholder:text-mist"
      />
      <button
        onClick={draw}
        disabled={busy}
        className="mt-4 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95 disabled:opacity-50"
      >
        {busy ? "Тянем карты…" : "Вытянуть карты"}
      </button>
      {paywall && <PaywallSheet plans={plans} abPrice={abPrice} onClose={() => setPaywall(false)} />}
      {error && (
        <p role="alert" className="mt-6 text-base text-mist">
          {error}
        </p>
      )}
      {text && (
        <div
          aria-live="polite"
          className="mt-6 rounded-2xl border border-white/10 bg-card/60 p-5 backdrop-blur-xl"
        >  <p className="whitespace-pre-wrap text-base leading-relaxed text-paper">{text}</p>
          {readingId && (
            <Link href={`/reading/${readingId}`} className="mt-3 inline-block text-sm text-gold">
              Открыть расклад →
            </Link>
          )}
        </div>
      )}
    </main>
  );
}
