"use client";

// Заставка главной (см. docs/project-book/05-design/08-ultra-cinematic.md §1).
//
// Чего этот компонент НЕ делает и почему — это важнее того, что он делает:
//
// 1. БЕЗ 3D. Гейт производительности жёсткий: LCP лендинга <2.5s на mobile 4G
//    (fail в CI при >2.8s), «интро не блокирует LCP», «autoplay 3D запрещён до
//    LCP», первый paint — CTA/текст без ожидания 3D. R3F-храм на главной эти
//    гейты проигрывает, поэтому здесь только CSS/Framer: тот же визуальный
//    язык (полосы, подсветка, виньетка), но без второго WebGL-контекста.
//
// 2. БЕЗ перекрывающего экрана. Туман-шимер из спеки «расходится от точки тапа»
//    закрывает собой заголовок, а значит откладывает LCP до конца анимации.
//    Здесь вуаль — подсветка ПОД текстом (z-index ниже контента), поэтому
//    заголовок виден с первого кадра и LCP засчитывается сразу.
//
// 3. Заголовок двигается ТОЛЬКО через transform и анимируется CSS-классом, а не
//    Framer. Причина конкретная: фаза заставки решается в эффекте (нужен
//    sessionStorage), а `initial` у Framer читается только при монтировании и
//    не перечитывается при смене props. Отсюда был реальный баг: текст
//    рендерился с `initial={false}` и НИКОГДА не появлялся.
//    CSS-класс же применяется с первого paint серверного HTML — эффекта
//    «появилось → прыгнуло назад → поехало» не бывает, и LCP не сдвигается:
//    transform не отменяет засчёт элемента, opacity:0 — отменяет.
//    Глобальное правило prefers-reduced-motion в globals.css гасит такие
//    анимации автоматически (в отличие от Framer, который идёт через JS/WAAPI).
//
// 4. Один раз за сессию — только ПОЛОСЫ. Текст и «кино-зал» повторяются всегда:
//    кинематографический вход на каждом reload утомляет, а деликатный подъём
//    заголовка нет. Константа PLAYED_KEY меняет поведение одним словом.

import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { motion } from "framer-motion";
import { shouldUseLite } from "@/lib/device";

/** Ключ «заставка уже показана» — вкладка, не браузер. */
const PLAYED_KEY = "taro_intro_played";

/** Полосы 8vh, въезд/выезд суммарно 1.2с — столько же, сколько Letterbox расклада. */
export const INTRO_DURATION = 1.2;

const EASE = [0.22, 1, 0.36, 1] as const;

function alreadyPlayed(): boolean {
  try {
    return sessionStorage.getItem(PLAYED_KEY) === "1";
  } catch {
    // Приватный режим / отключённое хранилище: лучше показать лишний раз,
    // чем не показать вовсе.
    return false;
  }
}

function markPlayed(): void {
  try {
    sessionStorage.setItem(PLAYED_KEY, "1");
  } catch {
    /* ignore */
  }
}

type Phase = "play" | "done";

const IntroCtx = createContext<{ phase: Phase }>({ phase: "done" });

/**
 * useIntro — фаза для потомков. Нужна компонентам, которые решают, рендерить ли
 * клиентскую часть (полосы) вообще: на SSR их быть не должно, иначе полосы
 * мигнули бы при гидрации.
 */
export function useIntro(): { phase: Phase } {
  return useContext(IntroCtx);
}

export default function LandingIntro({ children }: { children?: ReactNode }) {
  // null = ещё не решали: SSR отдаёт статику, и клиент не мигает появлением.
  const [phase, setPhase] = useState<Phase | null>(null);

  useEffect(() => {
    if (shouldUseLite() || alreadyPlayed()) {
      setPhase("done");
      return;
    }
    setPhase("play");
    markPlayed();
    const t = setTimeout(() => setPhase("done"), INTRO_DURATION * 1000);
    return () => clearTimeout(t);
  }, []);

  return (
    <IntroCtx.Provider value={{ phase: phase ?? "done" }}>
      {/* Постоянный «кинозал»: подсветка за заголовком, тёплое ядро, виньетка.
          Живёт всегда, а не только в фазе play: лёгкий «зал» должен остаться и
          после заставки, как остаётся виньетка на раскладе. Дрейф — CSS, он
          сам гасится правилом prefers-reduced-motion. */}
      <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden">
        <div
          data-testid="intro-glow"
          className="intro-drift absolute left-1/2 top-1/2 h-[70vmin] w-[70vmin] -translate-x-1/2 -translate-y-1/2"
          style={{
            background:
              "radial-gradient(circle, rgba(124,92,255,0.16) 0%, rgba(212,175,55,0.06) 38%, transparent 68%)",
          }}
        />
        <div
          className="intro-breathe absolute left-1/2 top-[38%] h-[38vmin] w-[38vmin] -translate-x-1/2 -translate-y-1/2"
          style={{ background: "radial-gradient(circle, rgba(212,175,55,0.13) 0%, transparent 62%)" }}
        />
        <div
          className="absolute inset-0"
          style={{
            background:
              "radial-gradient(120% 90% at 50% 45%, transparent 42%, rgba(11,11,20,0.55) 88%, rgba(11,11,20,0.8) 100%)",
          }}
        />
      </div>

      {children}

      {/* Полосы — единственный клиентский кадр заставки, поэтому гейт на них и
          висит. pointer-events-none: полосы занимают верх/низ 8vh и не должны
          перехватывать тап по кнопке в центре. Прежний Letterbox на раскладе
          перекрывал весь экран и глушил первый тап по контенту. */}
      {phase === "play" && (
        <div aria-hidden className="pointer-events-none fixed inset-0 z-20 overflow-hidden">
          <motion.div
            data-testid="intro-bar-top"
            className="absolute inset-x-0 top-0 bg-black"
            initial={{ height: "0vh" }}
            animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
            transition={{ duration: INTRO_DURATION, times: [0, 0.25, 0.75, 1], ease: EASE }}
          />
          <motion.div
            data-testid="intro-bar-bottom"
            className="absolute inset-x-0 bottom-0 bg-black"
            initial={{ height: "0vh" }}
            animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
            transition={{ duration: INTRO_DURATION, times: [0, 0.25, 0.75, 1], ease: EASE }}
          />
        </div>
      )}
    </IntroCtx.Provider>
  );
}

/**
 * Надпись над заголовком. Единственный элемент, которому разрешено стартовать
 * с opacity:0, — мелкая подпись: на LCP она не влияет. Раскрывается из
 * разрежённого трекинга в нормальный — «вывеска проявляется».
 */
export function IntroEyebrow({ text = "Онлайн Таро" }: { text?: string }) {
  return (
    <p data-testid="intro-eyebrow" className="intro-eyebrow text-sm uppercase text-gold">
      {text}
    </p>
  );
}

/**
 * Заголовок. Анимируется CSS-классом `.intro-rise` (transform + text-shadow),
 * БЕЗ opacity — это LCP-элемент, и его исчезновение на первом кадре сдвинуло
 * бы LCP на длительность анимации.
 */
export function IntroHeadline({ children }: { children: ReactNode }) {
  return (
    <h1
      data-testid="intro-headline"
      className="intro-emerge mt-4 max-w-xl font-display text-4xl font-semibold leading-tight text-paper"
    >
      {children}
    </h1>
  );
}

/** Подзаголовок. opacity:0 разрешён — на LCP не влияет. */
export function IntroSub({ children }: { children: ReactNode }) {
  return (
    <div
      data-testid="intro-sub"
      className="intro-sub mt-4 max-w-md text-base leading-relaxed text-mist"
    >
      {children}
    </div>
  );
}
