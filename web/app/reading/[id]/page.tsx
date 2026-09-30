import Link from "next/link";
import { Reading } from "@/lib/api";
import { headers } from "next/headers";
import CinematicBackdrop from "@/components/cinematic/CinematicBackdrop";
import CardArt from "@/components/CardArt";
import ShareButtons from "@/components/ShareButtons";
import InterpretationLive from "@/components/InterpretationLive";

// Экран результата (см. 02-functional/03, T16). locked — blur старых для free (см. T12).
// id — UUID чтения: allowlist до фетча (аудит B: без него traversal/query-smuggling в Go).
const READING_ID_RE = /^[0-9a-fA-F-]{8,64}$/;

// cardWidth — ширина одной карты в пикселях для next/image (нужна для
// оптимизатора: он строит srcset по заявленной ширине, а не по фактической).
// Реальный размер рисует CSS (w-full в колонке), число нужно только как
// верхняя граница. Растёт с числом карт: 10 карт кельтского креста в узкой
// колонке превращали страницу в длинную ленту, поэтому при большем раскладе
// карта крупнее и сетка складывается в несколько рядов.
// cardWidth — ширина карты для next/image (нужна оптимизатору для srcset) и
// одновременно верхний предел на <li>. Считана так, чтобы расклад ВСЕГДА
// влезал в один экран: карта 5:8 плюс подпись «прямая», рядов тем больше,
// чем больше карт, поэтому при 10 картах они заметно мельче.
function cardWidth(count: number): number {
  if (count <= 1) return 250;
  if (count <= 3) return 200;
  if (count <= 5) return 150;
  return 110;
}
async function load(id: string): Promise<Reading | null> {
  if (!READING_ID_RE.test(id)) return null;
  const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
  try {
    const requestHeaders = await headers();
    const res = await fetch(`${base}/v1/readings/${encodeURIComponent(id)}`, {
      headers: { Cookie: requestHeaders.get("cookie") || "" },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Reading;
  } catch {
    return null;
  }
}

export default async function ReadingPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const r = await load(id);
  if (!r) {
    return (
      <main className="mx-auto max-w-md px-4 pt-8">
        <p className="text-base text-mist">Расклад не найден.</p>
        <Link href="/spreads" className="text-sm text-gold">
          ← К раскладам
        </Link>
      </main>
    );
  }
  return (
    <main className="relative w-full px-4 pt-4">
      {/* muted: 3D-фон приглушён — настоящие карты не должны перекрываться
          декоративным веером рубашек (см. FoilFan в SceneInner). */}
      <CinematicBackdrop muted />
      {/* Три колонки на широком экране: слева действия, по центру карты,
          справа толкование. Владелец попросил именно такую раскладку.
          h-[100dvh] + overflow-hidden — страница БЕЗ скролла: расклад целиком
          помещается в экран, толкование читается в своей колонке.
          dvh, а не vh: мобильные браузеры адресную строку прячут/показывают,
          на vh это даёт обрезку низа и появление скролла. */}
      <div className="relative mx-auto grid h-[100dvh] max-w-[110rem] grid-cols-1 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden gap-3 pb-20 lg:grid-cols-[13rem_minmax(0,1fr)_26rem] lg:grid-rows-1 lg:gap-4">
        {/* Левая колонка — действия. Компактные, чтобы не перетягивали
            внимание с карт: узкая колонка, мелкий кегль, без заливки.
            Ширины 10rem не хватало: подпись с разрядкой «Действия»
            обрезалась слева. */}
        <aside className="order-2 flex flex-col items-start gap-2 lg:order-1 lg:justify-center">
          <p className="text-xs uppercase tracking-widest text-mist">Действия</p>
          {!r.locked && <ShareButtons question={r.question} spread={r.spread} readingId={r.id} compact />}
          {!r.locked && (
            <Link
              href={`/diary?reading=${r.id}`}
              className="inline-block rounded-lg border border-white/10 px-3 py-1.5 text-xs text-paper"
            >
              Записать мысли
            </Link>
          )}
          <Link href="/spreads" className="text-xs text-mist hover:text-paper">
            ← Другие расклады
          </Link>
        </aside>

        {/* Центр — карты и вопрос. */}
        <section className="order-1 flex min-h-0 flex-col lg:order-2">
          <header className="shrink-0 text-center">
            <p className="text-sm uppercase tracking-[0.2em] text-gold">Расклад</p>
            {r.question && (
              <h1 className="mt-1 truncate font-display text-2xl font-semibold text-paper">
                {r.question}
              </h1>
            )}
          </header>
          {/* Сетка: auto-fit + ограничение ширины карты сверху. Без верхнего
              предела при одной карте колонка растягивается во весь экран.
              Высоту задаёт flex-1 + overflow-hidden, а НЕ max-height: раньше
              жёсткий предел срезал верхний ряд. justify-content:center держит
              карты в середине полосы, а не прижимает к заголовку. */}
          <ul className="mt-4 grid min-h-0 flex-1 auto-rows-min grid-cols-[repeat(auto-fit,minmax(6rem,1fr))] content-center items-center justify-items-center gap-3 overflow-hidden">
            {r.cards.map((c) => (
              <li
                key={c.position}
                className="ca-flip mx-auto w-full"
                style={{ maxWidth: cardWidth(r.cards.length) }}
              >
                <CardArt
                  name={c.name_ru || `Карта ${c.card_id}`}
                  imageKey={c.image_key}
                  width={cardWidth(r.cards.length)}
                  className="h-auto w-full"
                />
                <p className="mt-1 truncate text-center text-[0.7rem] text-mist">
                  {c.reversed ? "перевернутая" : "прямая"}
                </p>
              </li>
            ))}
          </ul>
        </section>

        {/* Правая колонка — описание расклада. Скроллится ВНУТРИ себя, если
            текст не влезает: страница целиком остаётся без скролла. */}
        <aside className="order-3 min-h-0 overflow-y-auto">
          <div className="rounded-2xl border border-white/10 bg-card/60 p-4 backdrop-blur-xl">
            {/* Мы попадаем сюда сразу после создания расклада, толкование ещё
                генерируется — компонент дожидается его опросом. */}
            <InterpretationLive
              readingId={r.id}
              initial={r.interpretation}
              locked={r.locked}
            />
          </div>
        </aside>
      </div>
    </main>
  );
}
