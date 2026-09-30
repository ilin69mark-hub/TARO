"use client";

import Image from "next/image";

// Оверлей «магии» на время вытягивания карт: рубашки расходятся веером.
// Монтируется родителем ровно на время запроса и размонтируется на любом
// исходе — успех (переход на карты), пейволл (402) или ошибка. Своего
// состояния у компонента намеренно нет: именно его отсутствие и было причиной
// залипания, когда оверлем управлял отдельный флаг на родителе.
//
// Движение выключается настройкой ОС: глобальное правило prefers-reduced-motion
// в globals.css уже гасит анимации, дублировать его в JS не нужно.
export default function DrawMagic() {
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="draw-magic"
      className="fixed inset-0 z-30 flex flex-col items-center justify-center gap-6 bg-deep/85 backdrop-blur-md"
    >
      <div className="relative h-40 w-32" aria-hidden="true">
        {[-26, -13, 0, 13, 26].map((deg, i) => (
          <Image
            key={deg}
            src="/cards/card-back.webp"
            alt=""
            width={200}
            height={320}
            className="draw-magic-card"
            style={{ ["--draw-deg" as string]: `${deg}deg`, animationDelay: `${i * 60}ms` }}
          />
        ))}
      </div>
      <p className="text-sm uppercase tracking-[0.25em] text-gold">Карты ложатся…</p>
    </div>
  );
}
