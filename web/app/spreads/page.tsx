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
  // FREE слева, PREMIUM справа (владелец). Каталог без скролла: 100dvh +
  // overflow hidden, колонки делят высоту поровну. Если раскладов станет больше,
  // чем влезает, колонки сожмут карточки, но полосы прокрутки не будет.
  const free = spreads.filter((s) => !s.is_premium);
  const premium = spreads.filter((s) => s.is_premium);
  const column = (items: Spread[], title: string, tone: "free" | "premium") => (
    <section className="flex h-fit min-h-0 max-h-full flex-col">
      {/* Заголовок стоит НАД карточками, а не в самом верху экрана: колонки
          тянутся на всю высоту, и «шапка экрана» отрывалась от своего списка.
          content-start (не center) выравнивает верхние карточки обеих колонок
          по одной линии — при разном числе раскладов center разносил их. */}
      <h2
        className={`shrink-0 border-b border-white/10 pb-2 text-center text-sm uppercase tracking-[0.2em] ${
          tone === "premium" ? "text-gold" : "text-mist"
        }`}
      >
        {title}
      </h2>
      <ul className="mt-3 grid min-h-0 flex-1 auto-rows-min content-start gap-3 overflow-y-auto">
        {items.map((s) => (
          <li key={s.code} className="rounded-2xl border border-white/10 bg-card p-4">
            <Link href={`/spreads/${s.code}`} className="block text-center">
              <p className="text-base leading-snug text-paper">{s.name}</p>
              <p className={`mt-0.5 text-xs ${tone === "premium" ? "text-gold" : "text-mist"}`}>
                {tone === "premium" ? "премиум" : "бесплатно"}
              </p>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
  return (
    <main className="px-4 pt-6">
      <div className="mx-auto flex h-[100dvh] max-w-3xl flex-col pb-20">
        <header className="shrink-0 text-center">
          <h1 className="font-display text-2xl font-semibold text-paper">Расклады</h1>
          <StreakBadge />
        </header>
        {spreads.length === 0 ? (
          <p className="mt-8 text-center text-base text-mist">Каталог недоступен — загляни позже.</p>
        ) : (
          // content-center центрирует БЛОК колонок по вертикали (владелец:
          // «не сверху, а по центру»). Внутри колонок остаётся content-start —
          // иначе верхние карточки FREE и PREMIUM снова разъедутся по вертикали,
          // ведь раскладов в колонках разное количество.
          <div className="mt-4 grid min-h-0 flex-1 auto-rows-min content-center grid-cols-1 gap-4 sm:grid-cols-2 sm:auto-rows-auto">
            {column(free, "Бесплатные", "free")}
            {column(premium, "Премиум", "premium")}
          </div>
        )}
      </div>
    </main>
  );
}
