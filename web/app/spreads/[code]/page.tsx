"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { postReadingSSE, api, Spread } from "@/lib/api";
import { ensureAuth } from "@/lib/auth";
import { track, events } from "@/lib/analytics";
import { bumpReadingCount } from "@/components/PWA";
import PaywallSheet, { Plan } from "@/components/PaywallSheet";
import DrawMagic from "@/components/DrawMagic";

// Экран расклада: вопрос → «магия» → сразу страница с картами (см. 02-functional/03, T16).
// 3D и анимации вытягивания — T21–T28; здесь Lite-флоу ≤3 клика.
export default function SpreadDetail() {
  const params = useParams<{ code: string }>();
  const router = useRouter();
  const code = params.code;
  const [question, setQuestion] = useState("");
  const [text, setText] = useState("");
  const [readingId, setReadingId] = useState("");
  const [busy, setBusy] = useState(false);
  const [paywall, setPaywall] = useState(false);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [abPrice, setAbPrice] = useState(0);
  const [error, setError] = useState("");
  // Аудит C: ключ попытки живёт до успеха — retry после обрыва НЕ жрёт квоту повторно.
  // Новый ключ — только явным сбросом (newAttempt после успеха/провала с paywall).
  const [attemptKey, setAttemptKey] = useState("");
  const [spreadName, setSpreadName] = useState(code);
  const [jumped, setJumped] = useState(false);

  // Переход на карты: id получен — идём. Повторный вызов (финальный кадр
  // дублирует id) блокируем флагом, иначе router.push дёрнется дважды.
  function jumpToReading(id: string) {
    if (jumped || !id) return;
    setJumped(true);
    router.push(`/reading/${id}`);
  }

  useEffect(() => {
    track(events.spreadOpen, { spread_code: code });
    api
      .get<Spread[]>("/spreads")
      .then((list) => {
        const found = list.find((s) => s.code === code);
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

  async function draw(isRetry = false) {
    setBusy(true);
    setText("");
    setPaywall(false);
    setError("");
    setReadingId("");
    try {
      await ensureAuth();
      // retry тем же ключом (идемпотентность сервера), новая попытка — новым
      const key = isRetry && attemptKey ? attemptKey : crypto.randomUUID();
      setAttemptKey(key);
      // id приходит первым кадром: переходим на страницу с картами сразу,
      // не дожидаясь генерации толкования (владелец: «сразу переходить»).
      // Если id не придёт (старый кэш API, JSON-ответ) — останемся здесь.
      const res = await postReadingSSE(
        { spread_code: code, question: question || undefined, idempotency_key: key },
        (t) => setText((prev) => prev + t),
        (id) => jumpToReading(id)
      );
      setReadingId(res.reading_id);
      if (!res.reading_id) setError("Расклад не создался. Попробуй ещё раз.");
      setAttemptKey(""); // успех — ключ отработан
      bumpReadingCount(); // install-промпт после 2-го (см. T19)
    } catch (e: unknown) {
      const err = e as { code?: string; message?: string };
      if (err.code === "LIMIT_EXCEEDED") {
        setAttemptKey(""); // paywall — это финал попытки, не retry
        openPaywall();
      } else setError(err.message || "Не получилось вытянуть карты");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      {/* «Все расклады» — на прежнем месте (слева вверху), но теперь это
      заметная кнопка, а не безымянная ссылка. */}
      <Link
        href="/spreads"
        className="inline-flex items-center gap-1.5 rounded-full border border-white/15 bg-card/60 px-4 py-2 text-sm text-mist backdrop-blur-xl"
      >
        <span aria-hidden="true">←</span> Все расклады
      </Link>
      <h1 className="mt-4 text-center text-3xl font-semibold text-paper">{spreadName}</h1>
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
        onClick={() => {
          void draw();
        }}
        disabled={busy}
        className="mt-4 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95 disabled:opacity-50"
      >
        {busy ? "Тянем карты…" : "Вытянуть карты"}
      </button>
      {paywall && <PaywallSheet plans={plans} abPrice={abPrice} onClose={() => setPaywall(false)} />}
      {/* Оверлей живёт ровно пока идёт запрос: busy гаснет в finally на любом
          исходе. Раньше magic ставился в true по клику и не сбрасывался —
          при 402 пейволл открывался ПОД залипшим затемнением, и страница
          выглядела замороженной. */}
      {busy && !jumped && !paywall && <DrawMagic />}
      {error && (
        <>
          <p role="alert" className="mt-6 text-base text-mist">
            {error}
          </p>
          {attemptKey && (
            <button
              onClick={() => {
                void draw(true);
              }}
              disabled={busy}
              className="mt-2 text-sm text-gold disabled:opacity-50"
            >
              Попробовать снова (без повторного списания)
            </button>
          )}
        </>
      )}
      {/* Фолбэк: толкование стримится сюда только если id не пришёл и переход
          не состоялся (старый кэш API, JSON-ответ). При обычном ходе сюда
          не успевают — пользователь уже на странице с картами. */}
      {text && (
        <div
          aria-live="polite"
          className="mt-6 rounded-2xl border border-white/10 bg-card/60 p-5 backdrop-blur-xl"
        >
          <p className="whitespace-pre-wrap text-base leading-relaxed text-paper">{text}</p>
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
