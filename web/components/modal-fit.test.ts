// Модальные окна: должны помещаться в экран целиком и не давать прокручивать
// страницу под собой.
//
// Проверяем по исходникам: это правка разметки, она молча откатывается, и
// ни один сборочный шаг не скажет «окно снова не влезло».
import path from "node:path";
import fs from "node:fs";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const read = (p: string) => fs.readFileSync(path.join(root, p), "utf8");
const paywall = read("components/PaywallSheet.tsx");
const onboarding = read("components/Onboarding.tsx");
const legal = read("components/Legal.tsx");
const profile = read("app/profile/page.tsx");
const lock = read("lib/useScrollLock.ts");

const dialogs: [string, string][] = [
  ["тарифы", paywall],
  ["знакомство", onboarding],
  ["важная информация", legal],
  ["удаление данных", profile],
];

describe("окно тарифов помещается в экран целиком", () => {
  it("ограничено по высоте вьюпорта", () => {
    // Без max-h окно растёт вниз от нижнего края и уезжает за верх экрана:
    // заголовок и чекбокс 18+ становятся недоступны.
    expect(paywall).toMatch(/max-h-\[90dvh\]/);
  });

  it("прокручивается внутри, а не обрезается", () => {
    expect(paywall).toMatch(/min-h-0 flex-1[\s\S]{0,80}?overflow-y-auto/);
  });

  it("шапка и подвал зафиксированы", () => {
    // Заголовок, чекбокс 18+ и кнопка «Продолжить бесплатно завтра» не должны
    // уезжать при прокрутке списка тарифов.
    const shrink = paywall.match(/className="shrink-0[^"]*"/g) ?? [];
    expect(shrink.length).toBeGreaterThanOrEqual(2);
  });

  // Литералы сравниваем toContain, а не регуляркой: в тексте есть «?» и «−»,
  // и экранирование регулярки раньше тихо ломало проверку.
  it("заголовок и чекбокс остались на месте", () => {
    expect(paywall).toContain("Заглянем глубже?");
    expect(paywall).toContain("Мне есть 18");
    expect(paywall).toContain("Продолжить бесплатно завтра");
    expect(paywall).toContain("На сегодня бесплатные карты закончились");
  });

  it("все три тарифа на месте", () => {
    expect(paywall).toContain("Безлимит на месяц");
    expect(paywall).toContain("Разовый premium-расклад");
    expect(paywall).toContain("Безлимит на год");
  });
});

describe("страница не прокручивается под окнами", () => {
  for (const [name, src] of dialogs) {
    it(`${name}: блокирует скролл`, () => {
      expect(src, `${name}: нет useScrollLock`).toMatch(/useScrollLock\(/);
    });
  }

  it("хук восстанавливает скролл и не сбрасывает чужое состояние", () => {
    // Возврат в '' вместо прежнего значения оставил бы вложенное окно без
    // защиты после закрытия верхнего.
    expect(lock).toMatch(/body\.style\.overflow = previousOverflow/);
    expect(lock).toMatch(/paddingRight = previousPadding/);
  });

  it("компенсирует ширину скроллбара", () => {
    expect(lock).toMatch(/window\.innerWidth - document\.documentElement\.clientWidth/);
  });

  it("вызывается до раннего return (правило хуков)", () => {
    // Иначе порядок хуков ломается между рендерами.
    for (const [name, src] of dialogs) {
      const iHook = src.indexOf("useScrollLock(");
      const iReturn = src.indexOf("return null;");
      if (iReturn === -1) continue;
      expect(iHook, `${name}: useScrollLock после раннего return`).toBeLessThan(iReturn);
    }
  });
});
