"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, csrf } from "@/lib/api";
import { applyCalm, isCalm } from "@/components/Legal";
import PushOptIn from "@/components/PushOptIn";
import PushPrefs from "@/components/PushPrefs";
import PaywallSheet, { Plan } from "@/components/PaywallSheet";

// Профиль: лимиты + тарифы + реферальный код (см. T18, 02-functional/05/06).
type Ent = { plan: string; free_left: number; love_left_week: number; valid_until: string | null; winback_eligible?: boolean };
type Ref = { code: string; invited: number; bonus_days: number };

export default function ProfilePage() {
  const [ent, setEnt] = useState<Ent | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [ref, setRef] = useState<Ref | null>(null);
  const [showPaywall, setShowPaywall] = useState(false);
  const [applyCode, setApplyCode] = useState("");
  const [applyMsg, setApplyMsg] = useState("");
  const [calm, setCalm] = useState(false);
  const [deleted, setDeleted] = useState(false);

  useEffect(() => {
    api.get<Ent>("/entitlements/me").then(setEnt).catch(() => undefined);
    api.get<Plan[]>("/plans").then(setPlans).catch(() => undefined);
    api.get<Ref>("/referral/me").then(setRef).catch(() => undefined);
    setCalm(isCalm());
  }, []);

  async function apply() {
    setApplyMsg("");
    try {
      await api.post("/referral/apply", { code: applyCode.trim().toUpperCase() });
      setApplyMsg("Код принят! Бонус придет после первого расклада.");
    } catch (e: unknown) {
      setApplyMsg((e as Error).message);
    }
  }

  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-3xl font-display font-semibold text-paper">Профиль</h1>
      <p className="mt-2">
        <Link href="/diary" className="text-sm text-gold">
          Дневник →
        </Link>
      </p>
      <section className="mt-6 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Тариф</p>
        <p className="mt-1 text-lg text-paper">
          {ent ? (ent.plan === "premium" ? "Безлимит" : `Гость — осталось сегодня: ${ent.free_left}`) : "…"}
        </p>
        {ent?.valid_until && (
          <p className="mt-1 text-sm text-mist">
            до {new Date(ent.valid_until).toLocaleDateString("ru-RU")}
          </p>
        )}
        <button
          onClick={() => setShowPaywall(true)}
          className="mt-4 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
        >
          Тарифы
        </button>
        {ent?.plan === "premium" && (
          <p className="mt-3 rounded-2xl border border-gold/40 p-3 text-sm text-paper">
            Годовой безлимит выгоднее −30% — загляни в тарифы 🌙
          </p>
        )}
        {ent?.winback_eligible && (
          <p className="mt-3 rounded-2xl border border-gold/40 p-3 text-sm text-paper">
            Скучали! Для возвращения доступен тариф со скидкой — спроси в тарифах.
          </p>
        )}
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Пригласи подругу — +3 дня обоим</p>
        <p className="mt-1 text-sm text-mist">
          Подруга делает первый расклад — вы обе получаете +3 дня безлимита.
        </p>
        <p className="mt-1 text-lg text-gold">{ref ? ref.code : "…"}</p>
        <p className="mt-1 text-sm text-mist">
          Приглашено: {ref?.invited ?? "…"} · заработано дней: {ref?.bonus_days ?? "…"}
        </p>
        <div className="mt-3 flex gap-2">
          <input
            value={applyCode}
            aria-label="Чужой реферальный код"
            onChange={(e) => setApplyCode(e.target.value)}
            placeholder="Чужой код"
            className="min-w-0 flex-1 rounded-2xl border border-white/10 bg-deep p-3 text-base text-paper placeholder:text-mist"
          />
          <button onClick={apply} className="rounded-2xl border border-gold/40 px-4 text-sm font-semibold text-gold">
            OK
          </button>
        </div>
        {applyMsg && <p className="mt-2 text-sm text-mist">{applyMsg}</p>}
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Спокойный режим</p>        <button
          onClick={() => {
            const next = !calm;
            setCalm(next);
            applyCalm(next);
          }}
          aria-pressed={calm}
          className="mt-2 rounded-2xl border border-white/10 px-4 py-2 text-sm text-paper"
        >
          {calm ? "Включен" : "Выключен"}
        </button>
        <button
          onClick={async () => {
            if (!confirm("Удалить все мои данные? Это необратимо.")) return;
            if (prompt("Введи СЛОВО DELETE для подтверждения:") !== "DELETE") return;
            const res = await fetch("/api/me", {
              method: "DELETE",
              credentials: "include",
              headers: { "Content-Type": "application/json", "X-CSRF": csrf() },
              body: JSON.stringify({ confirm: "DELETE" }),
            }).catch(() => undefined);
            if (!res?.ok) return;
            try {
              localStorage.clear();
            } catch {
              /* ignore */
            }
            setDeleted(true);
          }}
          className="mt-4 block w-full py-2 text-center text-sm text-mist"
        >
          Удалить мои данные
        </button>
        {deleted && (
          <p className="mt-2 text-sm text-mist">Данные удалены. Обнови страницу для нового начала.</p>
        )}
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Напоминания</p>
        <div className="mt-2">
          <PushOptIn />
        </div>
        <PushPrefs />
      </section>
      {showPaywall && <PaywallSheet plans={plans} onClose={() => setShowPaywall(false)} />}
    </main>
  );
}
