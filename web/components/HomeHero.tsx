"use client";

// Главная: сцена + «зал» + контент.
//
// Слои снизу вверх:
//   1) 3D-сцена (lazy, ssr:false) — тот же храм, что на раскладе, но в режиме
//      `muted`: без веера из трёх рубашек (он конкурировал бы с заголовком),
//      вместо него одна рубашка на алтаре. Плюс вуаль, которая гасит сцену
//      и оставляет контрастным текст — то же средство, что на раскладе.
//   2) Постоянный «кинозал» из LandingIntro — подсветка и виньетка.
//   3) Контент.
//
// Гейт «3D не должен блокировать LCP» (03-nonfunctional/01) соблюдён так же,
// как на раскладе: сцена грузится отдельным чанком ПОСЛЕ гидрации, а h1 лежит
// в серверном HTML и виден с первого кадра. Поэтому здесь нет никакого
// «пока грузится — покажи заглушку»: заголовку нечего ждать.
import dynamic from "next/dynamic";
import { Suspense } from "react";
import Link from "next/link";
import LandingIntro, { IntroEyebrow, IntroHeadline, IntroSub } from "@/components/LandingIntro";
import { shouldUseLite } from "@/lib/device";

/** Lite-фон: когда сцена не поднимается (reduced-motion, спокойный режим, слабое
 *  устройство) — та же графика, что в CinematicBackdrop, но без зависимости от
 *  R3F, чтобы не тянуть three.js ради одного div. */
function StaticHall() {
  return (
    <div
      aria-hidden
      className="absolute inset-0"
      style={{
        background:
          "radial-gradient(70% 50% at 50% 30%, rgba(124,92,255,0.20), transparent 70%)," +
          "radial-gradient(40% 30% at 70% 70%, rgba(212,175,55,0.08), transparent 70%)," +
          "#0B0B14",
      }}
    />
  );
}

const Backdrop = dynamic(
  () => import("@/components/cinematic/CinematicBackdrop"),
  {
    ssr: false,
    loading: () => <StaticHall />,
  }
);

export default function HomeHero() {
  return (
    <main className="relative flex min-h-screen flex-col items-center justify-start overflow-hidden bg-deep px-6 pt-14 text-center">
      {/* Сцена под контентом. muted=true: вуаль гасит храм и держит контраст
          текста, а книга на алтаре остаётся единственным ярким пятном.

          pt-14 + justify-start — не отступ ради отступа. Блок контента стоял
          ровно по центру кадра, и алтарь с книгой попадал точно под кнопку:
          то, ради чего вся сцена и делалась, оказывалось закрыто CTA.
          Содержимое ушло вверх, нижняя треть отдана сцене. */}
      <Suspense fallback={<StaticHall />}>
        <Backdrop muted veil={0.32} />
      </Suspense>

      <LandingIntro>
        <div className="relative z-10 flex flex-col items-center">
          <IntroEyebrow />
          <IntroHeadline>Задай вопрос. Вытяни карты. Услышь себя.</IntroHeadline>
          <IntroSub>
            Карманный вечерний ритуал самопознания за 2 минуты. Бережно, без
            запугиваний.
          </IntroSub>
          <Link
            href="/spreads"
            className="mt-8 inline-flex h-12 items-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft px-8 text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
          >
            Выбрать расклад
          </Link>
        </div>
      </LandingIntro>
    </main>
  );
}

export { shouldUseLite };
