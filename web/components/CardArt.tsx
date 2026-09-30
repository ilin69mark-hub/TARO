"use client";

import { useState } from "react";
import Image from "next/image";

// CardArt: арт по image_key из БД (см. T24). Файлов ещё нет — graceful fallback:
// procedural рубашка (CSS), без битой картинки. Когда .webp лягут в public/cards —
// подхватятся без кода (контракт имён = image_key, см. public/cards/README.md).
// Allowlist имён артов (см. аудит B): image_key из БД обязан совпадать,
// иначе fallback-рубашка (путь конкатенируется — без allowlist возможен traversal).
// Префикс `cards/` обязателен: все 78 сид-ключей имеют именно такой вид
// (`cards/major-00-fool.webp`), и src собирается как `/` + image_key, то есть
// файл ожидается в `web/public/cards/`. Без префикса контракт был невыполним
// ни для одной карты (A05/F-04); golden-тест на все 78 ключей — в CardArt.test.tsx.
const IMAGE_KEY_RE =
  /^cards\/(major-[0-9]{2}-[a-z-]+|minor-[a-z]+-[a-z0-9]+|card-back)\.webp$/;

export default function CardArt({
  imageKey,
  name,
  width = 160,
  className,
}: {
  imageKey?: string;
  name: string;
  width?: number;
  // className — дополнение к базовым классам, а не замена: раньше компонент
  // жёстко задавал className, и центрировать карту в сетке расклада было нечем.
  className?: string;
}) {
  const [miss, setMiss] = useState(false);
  const base = "rounded-2xl border border-gold/40";
  if (!imageKey || miss || !IMAGE_KEY_RE.test(imageKey)) {
    return (
      <div
        role="img"
        aria-label={name}
        style={{ width, aspectRatio: "5 / 8" }}
        className={`${base} bg-card p-3 text-center ${className || ""}`}
      >
        <p className="mt-8 text-sm text-paper">{name}</p>
      </div>
    );
  }
  return (
    <Image
      src={`/${imageKey}`}
      alt={name}
      width={width}
      height={Math.round((width * 8) / 5)}
      className={`${base} ${className || ""}`}
      onError={() => setMiss(true)}
    />
  );
}
