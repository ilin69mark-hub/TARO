"use client";

import { useState } from "react";
import Image from "next/image";

// CardArt: арт по image_key из БД (см. T24). Файлов еще нет — graceful fallback:
// procedural рубашка (CSS), без битой картинки. Когда .webp лягут в public/cards —
// подхватятся без кода (контракт имен = image_key, см. public/cards/README.md).
// Allowlist имён артов (см. аудит B): image_key из БД обязан совпадать,
// иначе fallback-рубашка (путь конкатенируется — без allowlist возможен traversal).
const IMAGE_KEY_RE =
  /^(major-[0-9]{2}-[a-z-]+|minor-[a-z]+-[a-z0-9]+|card-back)\.webp$/;

export default function CardArt({
  imageKey,
  name,
  width = 160,
}: {
  imageKey?: string;
  name: string;
  width?: number;
}) {
  const [miss, setMiss] = useState(false);
  if (!imageKey || miss || !IMAGE_KEY_RE.test(imageKey)) {
    return (
      <div
        role="img"
        aria-label={name}
        style={{ width, aspectRatio: "5 / 8" }}
        className="rounded-2xl border border-gold/40 bg-card p-3 text-center"
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
      className="rounded-2xl border border-gold/40"
      onError={() => setMiss(true)}
    />
  );
}
