import Link from "next/link";
import SmoothScroll from "../components/SmoothScroll";

// Лендинг — mobile-first 360px (см. 03-nonfunctional/06, 05-design/04).
// TODO(T16): CTA «Вытянуть карту» → POST /v1/readings; free-счетчик из /v1/entitlements/me.
// U18: JSON-LD WebApplication (см. 03-nonfunctional/05).
const JSON_LD = {
  "@context": "https://schema.org",
  "@type": "WebApplication",
  name: "Онлайн Таро",
  applicationCategory: "LifestyleApplication",
  operatingSystem: "Web",
  inLanguage: "ru",
  offers: { "@type": "Offer", price: "0", priceCurrency: "RUB" },
};

export default function Home() {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center bg-deep px-6 text-center">
      <SmoothScroll />
      {/* JSON-LD только из статической константы; user/AI-текст — никогда (аудит B).
          </ экранирован: иначе будущая переменная даст stored-XSS в script-контексте. */}
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(JSON_LD).replace(/</g, "\\u003c") }} />
      <p className="text-sm uppercase tracking-[0.2em] text-gold">Онлайн Таро</p>
      <h1 className="mt-4 max-w-xl font-display text-4xl font-semibold leading-tight text-paper">
        Задай вопрос. Вытяни карты. Услышь себя.
      </h1>
      <p className="mt-4 max-w-md text-base leading-relaxed text-mist">
        Карманный вечерний ритуал самопознания за 2 минуты. Бережно, без
        запугиваний.
      </p>
      <Link
        href="/spreads"
        className="mt-8 inline-flex h-12 items-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft px-8 text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
      >
        Выбрать расклад
      </Link>
      <p className="mt-6 text-xs text-mist">
        Это инструмент самопознания и рефлексии, а не медицинская, психологическая,
        юридическая или финансовая услуга. Решения принимаете вы. 18+
      </p>
    </main>
  );
}
