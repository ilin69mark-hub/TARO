// Четыре правки дневника по наблюдениям владельца на стенде:
//   1) убрать «Скачать все записи (JSON)»;
//   2) фильтры и настроения — на русском;
//   3) из дневника должен быть возврат к раскладу;
//   4) расклад должен сохраняться в записи, чтобы мысли читались вместе с ним
//      и карты можно было посмотреть снова.
//
// Пункты 1–2 проверяются по исходнику страницы (это чистая разметка), пункт 4 —
// по контракту типа и по тому, что страница рисует снимок из ответа API.
import path from "node:path";
import fs from "node:fs";
import { describe, expect, it } from "vitest";

const here = __dirname;
const src = fs.readFileSync(path.join(here, "page.tsx"), "utf8");

describe("дневник: экспорт убран", () => {
  it("нет кнопки скачивания", () => {
    expect(src).not.toMatch(/Скачать все записи/);
    expect(src).not.toMatch(/diary\/export/);
  });

  it("нет функции download — мёртвый код тоже убираем", () => {
    expect(src).not.toMatch(/async function download/);
    expect(src).not.toMatch(/createObjectURL/);
  });
});

describe("дневник: настроения на русском", () => {
  it("в интерфейсе нет английских подписей", () => {
    // Латиница в разметке была: {m} рендерил "up"/"anxious" прямо в кнопку.
    for (const label of [">up<", ">down<", ">calm<", ">anxious<", ">grateful<"]) {
      expect(src, `кнопка с подписью ${label}`).not.toMatch(new RegExp(label));
    }
  });

  it("словарь содержит русские названия", () => {
    for (const w of ["Хорошо", "Тяжело", "Спокойно", "Тревожно", "Благодарно"]) {
      expect(src, `нет подписи «${w}»`).toContain(`"${w}"`);
    }
  });

  it("в БД по-прежнему английские ключи — схему не трогаем", () => {
    // CHECK в миграции 008 допускает только up/down/calm/anxious/grateful.
    // Локализация — слой интерфейса, ключи в запрос уходят прежние.
    expect(src).toMatch(/mood: mood \|\| undefined/);
    expect(src).toMatch(/&mood=\$\{m\.key\}/);
  });

  it("подпись настроения в записи тоже переведена", () => {
    expect(src).toMatch(/MOOD_LABEL\[it\.mood\]/);
  });
});

describe("дневник: возврат к раскладу", () => {
  it("при переходе из расклада есть ссылка обратно", () => {
    // Раньше был только текст «Запись к раскладу» — вернуться было некуда.
    expect(src).toMatch(/вернуться к раскладу/);
    expect(src).toMatch(/href=\{`\/reading\/\$\{readingId\}`\}/);
  });

  it("у каждой записи с раскладом есть ссылка на него", () => {
    expect(src).toMatch(/Открыть расклад целиком/);
  });

  it("подсказка ведёт к раскладу, а не в пустоту", () => {
    // Без ?reading= пользователь не понимает, откуда писать запись.
    expect(src).toMatch(/кнопкой «Записать мысли»/);
  });
});

describe("дневник: снимок расклада в записи", () => {
  it("тип записи объявляет снимок расклада", () => {
    expect(src).toMatch(/question\?:\s*string \| null/);
    expect(src).toMatch(/interpretation\?:\s*string \| null/);
    expect(src).toMatch(/cards\?:/);
  });

  it("запись рисует карты из снимка", () => {
    expect(src).toMatch(/artCards\(it\)\.map/);
    expect(src).toMatch(/<CardArt/);
    expect(src).toMatch(/imageKey=\{c\.image_key\}/);
  });

  it("запись показывает толкование расклада рядом с мыслями", () => {
    expect(src).toMatch(/it\.interpretation &&/);
  });

  it("снимок показывается только когда он есть", () => {
    // Проверка на оба поля: иначе запись без расклада нарисует пустую рамку.
    expect(src).toMatch(/it\.reading_id && \(artCards\(it\)\.length \|\| it\.question\)/);
  });

  it("карты без image_key не рисуются — была пустая рубашка", () => {
    // Часть чтений создана до поставки артов: в cards только card_id.
    // CardArt нарисует для них заглушку и испортит вид записи.
    expect(src).toMatch(/function artCards/);
    expect(src).toMatch(/filter\(\(c\) => !!c\.image_key\)/);
    expect(src).toMatch(/artCards\(it\)\.length > 0 &&/);
  });

  it("пустой дневник объясняет себя, а не выглядит сломанным", () => {
    expect(src).toMatch(/Записей пока нет/);
  });
});
