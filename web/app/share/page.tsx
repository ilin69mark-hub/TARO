import Link from "next/link";
import { Metadata } from "next";

// Публичный шеринг расклада (см. U12): превью без входа, БЕЗ толкования (приватность).
// noindex — шеринги не для поиска (см. 03-nonfunctional/05).
// Мета — только через generateMetadata ниже (OG + noindex).

export default function SharePage({ searchParams }: { searchParams: { q?: string; s?: string } }) {
  const q = (searchParams.q || "Мой расклад").slice(0, 80);
  const s = (searchParams.s || "Таро").slice(0, 40);
  const og = `/api/og?q=${encodeURIComponent(q)}&s=${encodeURIComponent(s)}`;
  return (
    <main className="mx-auto max-w-md px-4 pt-8 text-center">
      <p className="text-sm uppercase tracking-[0.2em] text-gold">Онлайн Таро · шеринг</p>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={og} alt="Превью расклада" className="mt-4 w-full rounded-2xl border border-gold/40" />
      <h1 className="mt-4 text-2xl font-semibold text-paper">{q}</h1>
      <p className="mt-1 text-sm text-mist">{s}</p>
      <Link
        href="/spreads"
        className="mt-6 inline-flex h-12 items-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft px-8 text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
      >
        Вытянуть свои карты
      </Link>
    </main>
  );
}

// OG-мета для /share генерируется динамически — generateMetadata здесь упрощен:
// og:image указывает на /api/og с теми же params (см. U12).
export async function generateMetadata({
  searchParams,
}: {
  searchParams: { q?: string; s?: string };
}): Promise<Metadata> {
  const q = (searchParams.q || "Мой расклад").slice(0, 80);
  const s = (searchParams.s || "Таро").slice(0, 40);
  return {
    title: `${q} — Онлайн Таро`,
    robots: { index: false, follow: false },
    // Аудит B: вопрос уже в URL — хотя бы не течём дальше через Referer (токен-шеринг — follow-up).
    referrer: "no-referrer",
    openGraph: {
      title: q,
      description: s,
      images: [`/api/og?q=${encodeURIComponent(q)}&s=${encodeURIComponent(s)}`],
    },
  };
}
