// Ловушка на повторяющийся класс ошибки.
//
// Обратная кавычка внутри комментария, который лежит в GLSL-шаблоне
// `/* glsl */ \`...\``, ТЕРМИНИРУЕТ строку. Компилятор TypeScript падает с
// «',' expected» на строке, не имеющей отношения к делу, и ошибка выглядит
// как синтаксическая чепуха в шейдере. На этом попался дважды за день:
// в FogQuad (комментарий про sin(uTime*1.4)) и в Candles (комментарий про маску).
//
// Здесь ловится за секунду, до сборки.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const dir = path.resolve(__dirname, "../components/cinematic");

describe("GLSL-шаблоны не содержат обратных кавычек", () => {
  const files = fs.readdirSync(dir).filter((f) => f.endsWith(".tsx"));

  it("в пакете есть что проверять", () => {
    expect(files.length, "файлы шейдеров не найдены").toBeGreaterThan(3);
  });

  for (const f of files) {
    it(f, () => {
      const src = fs.readFileSync(path.join(dir, f), "utf8");
      // Блок от /* glsl */ ` до закрывающей кавычки на отдельной строке
      const blocks = [...src.matchAll(/\/\* glsl \*\/ `([\s\S]*?)\n`;/g)];
      for (const [, body] of blocks) {
        expect(
          body,
          `${f}: обратная кавычка внутри GLSL-блока рвёт шаблон`
        ).not.toMatch(/`/);
      }
    });
  }
});
