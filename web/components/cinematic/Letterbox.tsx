"use client";

// Letterbox-интро 1.2с, skippable тапом (см. 05-animations.md).
// Только при активной 3D-сцене (CinematicBackdrop рендерит вне Lite).
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";

/**
 * Почему слой не на весь экран.
 *
 * Раньше оверлей висел `pointer-events-auto absolute inset-0` и перехватывал
 * ЛЮБОЙ тап первые 1.2с. На раскладе это значит: открыл расклад, хотел сразу
 * скопировать ссылку или поделиться — тап съедался на снятие полос, и кнопка
 * не срабатывала. Приём работал «по тапу где угодно», но ценой отнятых у
 * контента кликов.
 *
 * Теперь тап снимает полосы только в самих полосах (верх/низ 8vh), а центр
 * экрана — зона контента: он остаётся кликабельным с первого кадра. Спека
 * («skippable тапом») соблюдена — тап по полосе пропускает интро, просто он
 * больше не ворует тапы у кнопок.
 *
 * Сами полосы — pointer-events-none: декорация не должна быть мишенью вообще.
 * Клик ловит две невидимые зоны по краям.
 */
export default function Letterbox() {
  const [gone, setGone] = useState(false);
  return (
    <AnimatePresence>
      {!gone && (
        <>
          {/* Зоны «пропустить интро». Верхняя и нижняя полосы — 8vh каждая,
              зоны чуть выше, чтобы попадание по границе не промахивалось. */}
          <div
            aria-hidden
            data-testid="letterbox-skip-top"
            className="absolute inset-x-0 top-0 h-[12vh] cursor-pointer"
            onClick={() => setGone(true)}
          />
          <div
            aria-hidden
            data-testid="letterbox-skip-bottom"
            className="absolute inset-x-0 bottom-0 h-[12vh] cursor-pointer"
            onClick={() => setGone(true)}
          />
          <motion.div aria-hidden className="pointer-events-none absolute inset-0 z-10" exit={{ opacity: 0 }}>
            <motion.div
              className="absolute inset-x-0 top-0 bg-black"
              initial={{ height: 0 }}
              animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
              transition={{ duration: 1.2, times: [0, 0.25, 0.75, 1], ease: [0.22, 1, 0.36, 1] }}
              onAnimationComplete={() => setGone(true)}
            />
            <motion.div
              className="absolute inset-x-0 bottom-0 bg-black"
              initial={{ height: 0 }}
              animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
              transition={{ duration: 1.2, times: [0, 0.25, 0.75, 1], ease: [0.22, 1, 0.36, 1] }}
            />
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}
