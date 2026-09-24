import Link from "next/link";
import { Reading } from "@/lib/api";
import { headers } from "next/headers";
import CinematicBackdrop from "@/components/cinematic/CinematicBackdrop";
import CardArt from "@/components/CardArt";
import ShareButtons from "@/components/ShareButtons";

// Экран результата (см. 02-functional/03, T16). locked — blur старых для free (см. T12).
// id — UUID чтения: allowlist до фетча (аудит B: без него traversal/query-smuggling в Go).
const READING_ID_RE = /^[0-9a-fA-F-]{8,64}$/;
async function load(id: string): Promise<Reading | null> {
  if (!READING_ID_RE.test(id)) return null;
  const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
  try {
    const res = await fetch(`${base}/v1/readings/${encodeURIComponent(id)}`, {
      headers: { Cookie: headers().get("cookie") || "" },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Reading;
  } catch {
    return null;
  }
}

export default async function ReadingPage({ params }: { params: { id: string } }) {
  const r = await load(params.id);
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
    <main className="relative mx-auto max-w-md px-4 pt-8">
      <CinematicBackdrop />
      <div className="relative">
      <p className="text-sm uppercase tracking-[0.2em] text-gold">Расклад</p>
      {r.question && <h1 className="mt-2 text-2xl font-display font-semibold text-paper">{r.question}</h1>}
      <ul className="mt-6 flex flex-wrap gap-3">
        {r.cards.map((c) => (
          <li key={c.position} className="ca-flip">
            <CardArt name={c.name_ru || `Карта ${c.card_id}`} imageKey={c.image_key} width={112} />
            <p className="mt-1 text-center text-xs text-mist">
              {c.reversed ? "перевернутая" : "прямая"}
            </p>
          </li>
        ))}
      </ul>
      <div className="mt-6 rounded-2xl border border-white/10 bg-card/60 p-5 backdrop-blur-xl">
        {r.locked ? (
          <p className="text-base text-mist">
            Полный текст доступен в день расклада или с безлимитом.{" "}
            <Link href="/profile" className="text-gold">
              Тарифы →
            </Link>
          </p>
        ) : (
          <p className="whitespace-pre-wrap text-base leading-relaxed text-paper">
            {r.interpretation}
          </p>
        )}
      </div>
      {!r.locked && <ShareButtons question={r.question} spread={r.spread} readingId={r.id} />}
      {!r.locked && (
        <Link
          href={`/diary?reading=${r.id}`}
          className="mt-2 inline-block rounded-2xl border border-white/10 px-4 py-2 text-sm text-paper"
        >
          Записать мысли →
        </Link>
      )}
      </div>
    </main>
  );
}
