import { Metadata } from "next";
import CardArt from "@/components/CardArt";

// U16: 78 страниц значений карт из БД, SSR (см. 03-nonfunctional/05).
// force-dynamic: build-time Go недоступен, рендерим при запросе (HTML полный для curl).
export const dynamic = "force-dynamic";
type Card = { id: number; name_ru: string; upright_ru: string; reversed_ru: string; image_key: string };

// id — числовой id карты 0..77: allowlist до фетча (аудит B).
const CARD_ID_RE = /^[0-9]{1,3}$/;
async function load(id: string): Promise<Card | null> {
  if (!CARD_ID_RE.test(id)) return null;
  const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
  try {
    // Комментарий, который здесь стоял, утверждал, что публичного
    // GET /v1/cards/:id не существует и страница ходит «через внутренний
    // прокси-роут /api/cards/[id]». Это было неверно: роут есть
    // (api/cmd/api/main.go, GET /v1/cards/{id}), страница дёргает его напрямую
    // строкой ниже, а самого /api/cards/[id] в web/app/api нет вообще.
    // Читатель комментария потратил бы час на поиск несуществующего прокси.
    //
    // Заметим: base — это API_INTERNAL_URL, то есть адрес api-public, а не
    // same-origin /api. Запрос уходит с сервера Next (это server-компонент),
    // поэтому cookie пользователя тут не нужны и не передаются.
    const res = await fetch(`${base}/v1/cards/${encodeURIComponent(id)}`, { next: { revalidate: 86400 } });
    if (!res.ok) return null;
    return (await res.json()) as Card;
  } catch {
    return null;
  }
}

export async function generateStaticParams() {
  return [];
}

export async function generateMetadata({ params }: { params: Promise<{ id: string }> }): Promise<Metadata> {
  const { id } = await params;
  const c = await load(id);
  if (!c) return { title: "Карта — Онлайн Таро" };
  return {
    title: `${c.name_ru} — значение карты | Онлайн Таро`,
    description: c.upright_ru.slice(0, 160),
  };
}

export default async function CardPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const c = await load(id);
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
      {/* Арт карты. Раньше страница рендерила только текст: image_key из БД не
          использовался, поэтому /cards/[id] оставалась единственной страницей
          каталога без картинки (CardArt жил только в /reading/[id]). */}
      <div className="ca-flip mt-6 flex justify-center">
        <CardArt name={c.name_ru} imageKey={c.image_key} width={200} />
      </div>
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
