import Link from "next/link";
import { Metadata } from "next";

// Приватный шеринг по токену (без PII в URL, без толкования).
// Legacy /share?q= остался для старых ссылок (см. share/page.tsx).
type Shared = {
  question: string;
  spread: string;
  cards: { card_id: number; name_ru?: string }[];
  created_at: string;
};

async function load(token: string): Promise<Shared | null> {
  if (!/^[0-9a-f]{32}$/.test(token)) return null;
  try {
    // напрямую в Go (серверный fetch, как reading/[id]) — без лишнего хопа
    const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
    const res = await fetch(`${base}/v1/share/${encodeURIComponent(token)}`, {
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Shared;
  } catch {
    return null;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ token: string }>;
}): Promise<Metadata> {
  const { token } = await params;
  const s = await load(token);
  const title = s?.question ? s.question.slice(0, 80) : "Расклад Таро";
  return {
    title: `${title} — Онлайн Таро`,
    robots: { index: false, follow: false },
    referrer: "no-referrer",
    openGraph: {
      title,
      description: s?.spread || "Таро",
      images: [`/api/og?t=${encodeURIComponent(token)}`],
    },
  };
}

export default async function ShareTokenPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  const s = await load(token);
  if (!s) {
    return (
      <main className="mx-auto max-w-md px-4 pt-8 text-center">
        <p className="text-base text-mist">Ссылка не найдена.</p>
        <Link href="/spreads" className="text-sm text-gold">
          ← К раскладам
        </Link>
      </main>
    );
  }
  return (
    <main className="mx-auto max-w-md px-4 pt-8 text-center">
      <p className="text-sm uppercase tracking-[0.2em] text-gold">Онлайн Таро · шеринг</p>
      <h1 className="mt-4 text-2xl font-semibold text-paper">{s.question || "Мой расклад"}</h1>
      <p className="mt-1 text-sm text-mist">{s.spread}</p>
      <ul className="mt-4 space-y-1">
        {s.cards.map((c, i) => (
          <li key={i} className="text-sm text-paper">
            {c.name_ru || `Карта ${c.card_id}`}
          </li>
        ))}
      </ul>
      <Link
        href="/spreads"
        className="mt-6 inline-block rounded-2xl border border-gold/40 px-4 py-2 text-sm font-semibold text-gold"
      >
        Вытянуть свои карты
      </Link>
    </main>
  );
}
