import { Metadata } from "next";

// U16: 78 страниц значений карт из БД, SSR (см. 03-nonfunctional/05).
// force-dynamic: build-time Go недоступен, рендерим при запросе (HTML полный для curl).
export const dynamic = "force-dynamic";
type Card = { id: number; name_ru: string; upright_ru: string; reversed_ru: string; image_key: string };

async function load(id: string): Promise<Card | null> {
  const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
  try {
    // публичного GET /v1/cards/:id нет (карты — только SELECT в чтениях);
    // U16 тянет напрямую из PG? Нет — web не ходит в PG (см. 04-architecture/02).
    // Поэтому страница строится через внутренний прокси-роут /api/cards/[id] (см. ниже).
    const res = await fetch(`${base}/v1/cards/${id}`, { next: { revalidate: 86400 } });
    if (!res.ok) return null;
    return (await res.json()) as Card;
  } catch {
    return null;
  }
}

export async function generateStaticParams() {
  return [];
}

export async function generateMetadata({ params }: { params: { id: string } }): Promise<Metadata> {
  const c = await load(params.id);
  if (!c) return { title: "Карта — Онлайн Таро" };
  return {
    title: `${c.name_ru} — значение карты | Онлайн Таро`,
    description: c.upright_ru.slice(0, 160),
  };
}

export default async function CardPage({ params }: { params: { id: string } }) {
  const c = await load(params.id);
  if (!c) {
    return (
      <main className="mx-auto max-w-md px-4 pt-8">
        <p className="text-base text-mist">Карта не найдена.</p>
      </main>
    );
  }
  return (
    <main className="mx-auto max-w-md px-4 pt-8">
      <p className="text-sm uppercase tracking-[0.2em] text-gold">Значение карты</p>
      <h1 className="mt-2 text-3xl font-display font-semibold text-paper">{c.name_ru}</h1>
      <section className="mt-6 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Прямая</p>
        <p className="mt-1 text-base leading-relaxed text-paper">{c.upright_ru}</p>
      </section>
      <section className="mt-4 rounded-2xl border border-white/10 bg-card p-5">
        <p className="text-sm uppercase tracking-wider text-mist">Перевернутая</p>
        <p className="mt-1 text-base leading-relaxed text-paper">{c.reversed_ru}</p>
      </section>
    </main>
  );
}
