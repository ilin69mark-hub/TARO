import Link from "next/link";
import { Metadata } from "next";
import { Spread } from "@/lib/api";
import StreakBadge from "@/components/StreakBadge";

// U15: SSR-каталог с RU-метой (curl без JS видит полный HTML).
// force-dynamic: данные живьем из Go при каждом запросе (build-time Go недоступен).
export const dynamic = "force-dynamic";
export const metadata: Metadata = {
  title: "Расклады Таро — карта дня, отношения, решение | Онлайн Таро",
  description:
    "5 раскладов: карта дня, прошлое-настоящее-будущее, отношения, решение, кельтский крест. Русские толкования с ИИ.",
  openGraph: {
    title: "Расклады Таро",
    description: "Выбери ритуал на сегодня — толкование за 2 минуты.",
  },
};

// Каталог — живые данные из Go напрямую (server component, см. T05/T16).
async function load(): Promise<Spread[]> {
  const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
  try {
    const res = await fetch(`${base}/v1/spreads`, { next: { revalidate: 60 } });
    if (!res.ok) return [];
    return (await res.json()) as Spread[];
  } catch {
    return [];
  }
}

export default async function SpreadsPage() {
  const spreads = await load();
  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <h1 className="text-3xl font-display font-semibold text-paper">Расклады</h1>
      <StreakBadge />
      <p className="mt-2 text-sm text-mist">Выбери ритуал на сегодня</p>
      {spreads.length === 0 && (
        <p className="mt-6 text-base text-mist">Каталог недоступен — загляни позже.</p>
      )}
      <ul className="mt-6 space-y-4">
        {spreads.map((s) => (
          <li key={s.code} className="rounded-2xl border border-white/10 bg-card p-5">
            <Link href={`/spreads/${s.code}`} className="block">
              <p className="text-lg text-paper">{s.name}</p>
              <p className="mt-1 text-sm text-gold">
                {s.is_premium ? "premium" : "free"}
              </p>
            </Link>
          </li>
        ))}
      </ul>
    </main>
  );
}
