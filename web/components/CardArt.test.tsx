// Unit: контракт артов (A05/F-04). image_key из БД → путь /<image_key> в public/.
// Тест держит два свойства, которые нельзя увидеть в UI:
//   1) каждый из 78 сид-ключей проходит allowlist (иначе карта молча рисует рубашку);
//   2) allowlist по-прежнему отсекает traversal и абсолютные пути.
import React from "react";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import CardArt from "@/components/CardArt";

vi.mock("next/image", () => ({
  default: (props: {
    src: string;
    alt: string;
    width: number;
    height: number;
    onError?: () => void;
  }) => (
    // eslint-disable-next-line @next/next/no-img-element -- в тесте нужен обычный <img>, чтобы читать src
    <img src={props.src} alt={props.alt} width={props.width} height={props.height} onError={props.onError} />
  ),
}));

// Сид 002 — источник правды по image_key. Читаем файл, а не БД, чтобы тест
// жил в CI без Postgres и ловил расхождение до миграции.
const SEED = path.resolve(__dirname, "../../api/migrations/002_seed.up.sql");
const seedKeys: string[] = [
  ...new Set([...fs.readFileSync(SEED, "utf8").matchAll(/'(cards\/[^']+\.webp)'/g)].map((m) => m[1])),
].sort();

function srcOf(imageKey: string): string | null {
  const { container } = render(<CardArt imageKey={imageKey} name="Карта" width={160} />);
  const img = container.querySelector("img");
  return img ? img.getAttribute("src") : null;
}

function isFallback(imageKey: string): boolean {
  const { container } = render(<CardArt imageKey={imageKey} name="Карта" width={160} />);
  return container.querySelector("img") === null;
}

// Размеры WebP из заголовка RIFF: VP8/VP8L/VP8X (последний — с alpha и через
// chunks, поэтому читаем canvas-байты, а не только первые кадры).
function webpSize(buf: Buffer): { width: number; height: number } | null {
  const tag = buf.subarray(12, 16).toString("latin1");
  if (tag === "VP8 ") {
    return { width: buf.readUInt16LE(26) & 0x3fff, height: buf.readUInt16LE(28) & 0x3fff };
  }
  if (tag === "VP8L") {
    const bits = buf.readUInt32LE(21);
    return { width: (bits & 0x3fff) + 1, height: ((bits >> 14) & 0x3fff) + 1 };
  }
  if (tag === "VP8X") {
    const w = 1 + (buf[24] | (buf[25] << 8) | (buf[26] << 16));
    const h = 1 + (buf[27] | (buf[28] << 8) | (buf[29] << 16));
    return { width: w, height: h };
  }
  return null;
}

describe("CardArt: контракт image_key (A05/F-04)", () => {
  it("сид 002 содержит ровно 78 уникальных ключей со схемой cards/*.webp", () => {
    expect(seedKeys).toHaveLength(78);
    expect(seedKeys.every((k) => k.startsWith("cards/") && k.endsWith(".webp"))).toBe(true);
    expect(seedKeys.filter((k) => k.includes("/major-"))).toHaveLength(22);
    expect(seedKeys.filter((k) => k.includes("/minor-"))).toHaveLength(56);
  });

  it("все 78 сид-ключей проходят allowlist — иначе ни одна карта не отрендерится", () => {
    const rejected = seedKeys.filter((k) => isFallback(k));
    expect(rejected).toEqual([]);
  });

  it("для каждого ключа src собирается как /<image_key> (файл ищется в public/cards/)", () => {
    for (const key of seedKeys) {
      expect(srcOf(key)).toBe(`/${key}`);
    }
  });

  it("allowlist по-прежнему отсекает traversal, абсолютные пути и подмену расширения", () => {
    const attacks = [
      "cards/../../etc/passwd",
      "cards/../../../app/package.json",
      "/etc/passwd",
      "cards/major-00-fool.svg",
      "cards/major-00-fool.webp/../../../x",
      "cards/Major-00-Fool.webp",
      "cards/card-back.png",
      "../../../etc/shadow",
      "cards/major-00-fool.webp?x=1",
      "cards/sub/major-00-fool.webp",
    ];
    for (const attack of attacks) {
      expect(isFallback(attack), `ожидался fallback для ${attack}`).toBe(true);
    }
  });

  it("без image_key — fallback, а не битая картинка", () => {
    expect(isFallback("")).toBe(true);
    const { container } = render(<CardArt name="Карта" width={160} />);
    expect(container.querySelector("img")).toBeNull();
  });

  it("card-back остаётся в allowlist (рубашка/спина карты)", () => {
    expect(srcOf("cards/card-back.webp")).toBe("/cards/card-back.webp");
  });

  // Арты поставлены (E13): имена = image_key, 500x800 WebP <=90KB.
  // Проверка стала настоящей it — раньше была todo, пока файлов не было.
  it("все 78 image_key имеют файл в web/public/cards/ нужного формата", () => {
    const CARDS = path.resolve(__dirname, "../public/cards");
    for (const key of seedKeys) {
      const file = path.join(CARDS, key.replace(/^cards\//, ""));
      expect(fs.existsSync(file), `нет файла арта: ${key}`).toBe(true);
      // Имя с .webp над байтами другого формата отдаст битый Content-Type —
      // поэтому проверяем RIFF/WEBP, а не только расширение.
      const buf = fs.readFileSync(file);
      expect(buf.subarray(0, 4).toString("latin1"), `${key}: не RIFF`).toBe("RIFF");
      expect(buf.subarray(8, 12).toString("latin1"), `${key}: не WEBP`).toBe("WEBP");
      expect(buf.length, `${key}: больше бюджета 90KB`).toBeLessThanOrEqual(90 * 1024);
      // Геометрия читается из самого заголовка: sharp в web/node_modules
      // транзитивный (Next), на него опираться нельзя.
      expect(webpSize(buf), `${key}: не 500x800`).toEqual({ width: 500, height: 800 });
    }
  });
});
