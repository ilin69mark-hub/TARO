// Экран расклада: оформление и флоу «нажал → увидел карты».
// Проверяем по исходникам: правки вёрстки и маршрута молча откатываются,
// а сборочный шаг такое не ловит.
import path from "node:path";
import fs from "node:fs";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..", "..", "..");
const read = (p: string) => fs.readFileSync(path.join(root, p), "utf8");
const page = read("app/spreads/[code]/page.tsx");
const magic = read("components/DrawMagic.tsx");
const live = read("components/InterpretationLive.tsx");
const reading = read("app/reading/[id]/page.tsx");
const css = read("app/globals.css");

describe("название расклада по центру", () => {
  it("h1 отцентрован", () => {
    expect(page).toMatch(/<h1 className="[^"]*text-center[^"]*"[^>]*>\{spreadName\}<\/h1>/);
  });

  it("название берётся из каталога, а не из кода маршрута", () => {
    // Иначе до загрузки каталога видно «daily» вместо «Карта дня».
    expect(page).toMatch(/setSpreadName\(found\.name\)/);
  });
});

describe("кнопка «Все расклады» оформлена и осталась на месте", () => {
  it("это скруглённая кнопка с рамкой, а не голая ссылка", () => {
    expect(page).toMatch(/rounded-full border[^"]*px-4 py-2/);
  });

  it("стоит первым элементом страницы, до названия и вопроса", () => {
    const iBack = page.indexOf('href="/spreads"');
    const iTitle = page.indexOf("<h1");
    const iQuestion = page.indexOf("Твой вопрос");
    expect(iBack).toBeGreaterThan(-1);
    expect(iBack, "«Все расклады» должны быть выше названия").toBeLessThan(iTitle);
    expect(iBack).toBeLessThan(iQuestion);
  });

  it("стрелка назад сохранена", () => {
    expect(page).toMatch(/←<\/span> Все расклады/);
  });
});

describe("«Твой вопрос» остался на месте", () => {
  it("подпись и поле на месте, с привязкой", () => {
    // Владелец просил НЕ двигать: меняем только оформление, структура та же.
    expect(page).toMatch(/<label htmlFor="reading-question"[^>]*>\s*Твой вопрос\s*<\/label>/);
    expect(page).toMatch(/<textarea[\s\S]{0,200}?id="reading-question"/);
  });

  it("вопрос уходит в запрос", () => {
    expect(page).toMatch(/question: question \|\| undefined/);
  });
});

describe("«Вытянуть карты» → магия → сразу страница с картами", () => {
  it("переход происходит по id, а не после стриминга", () => {
    expect(page).toMatch(/postReadingSSE\([\s\S]{0,400}?\(id\) => jumpToReading\(id\)/);
    expect(page).toMatch(/router\.push\(`\/reading\/\$\{id\}`\)/);
  });

  it("повторный вызов перехода заблокирован", () => {
    // Финальный кадр дублирует id — без флага router.push дёрнулся бы дважды.
    expect(page).toMatch(/if \(jumped \|\| !id\) return;/);
    expect(page).toMatch(/setJumped\(true\)/);
  });

  it("оверлей включается на время нажатия и снимается при переходе", () => {
    // Оверлей не «включается» флагом по клику: он живёт, пока идёт запрос,
    // и снимается сам. Иначе при 402 он залипает (см. блок ниже).
    expect(page).toMatch(/onClick=\{\(\) => \{\s*void draw\(\);/);
    expect(page).toMatch(/\{busy && !jumped && !paywall && <DrawMagic \/>\}/);
  });

  it("оверлей — рубашки карт с подписью", () => {
    expect(magic).toMatch(/src="\/cards\/card-back\.webp"/);
    expect(magic).toMatch(/Карты ложатся/);
    expect(magic).toMatch(/role="status"/);
  });

  it("анимация объявлена в globals.css, а не инлайном <style>", () => {
    // В проекте keyframes живут в globals.css (ca-flip), иначе дублирование.
    expect(css).toMatch(/@keyframes draw-magic/);
    expect(css).toMatch(/\.draw-magic-card/);
    expect(magic).not.toMatch(/<style/);
  });

  it("анимация без motion-запросов в JS (гасит глобальное правило ОС)", () => {
    // Владелец просил меньше движения; prefers-reduced-motion уже есть в
    // globals.css. Дублировать его через matchMedia — лишнее.
    expect(magic).not.toMatch(/matchMedia/);
    expect(css).toMatch(/prefers-reduced-motion/);
  });
});

describe("страница с картами дожидается толкования", () => {
  it("толкование рисует живой компонент, а не серверный текст", () => {
    expect(reading).toMatch(/<InterpretationLive/);
    expect(reading).toMatch(/initial=\{r\.interpretation\}/);
    expect(reading).toMatch(/locked=\{r\.locked\}/);
  });

  it("опрос идёт по маршруту с cookie, а не напрямую в Go", () => {
    expect(live).toMatch(/api\.get<Reading>\(`\/readings\/\$\{readingId\}`\)/);
  });

  it("готовый текст не опрашивается заново", () => {
    // Иначе открытие старого расклада из истории дёргало бы API каждые 1.2с.
    expect(live).toMatch(/if \(locked \|\| initial\.trim\(\)\) return;/);
  });

  it("опрос прекращается, когда статус больше не pending", () => {
    expect(live).toMatch(/pending_fallback/);
    expect(live).toMatch(/if \(r\.status && !PENDING\.has\(r\.status\)\)/);
  });
});

// Регресс: оверлей залипал. magic ставился в true по клику и не сбрасывался,
// поэтому при 402 (лимит бесплатных исчерпан) пейволл открывался ПОД
// затемнением, и страница выглядела замороженной. Владелец словил это на
// стенде: «вижу изображение, которое застыло "карты ложатся"».
describe("оверлей магии не залипает", () => {
  it("нет отдельного флага magic — источник истины один", () => {
    // Отдельный флаг, который никто не сбрасывает, и есть источник бага.
    expect(page).not.toMatch(/setMagic\(/);
    expect(page).not.toMatch(/useState\(false\);\s*\n\s*\/\/.*magic/i);
  });

  it("оверлей показан ровно пока идёт запрос", () => {
    expect(page).toMatch(/\{busy && !jumped && !paywall && <DrawMagic \/>\}/);
  });

  it("busy гаснет на ЛЮБОМ исходе, включая пейволл", () => {
    // finally обязателен: без него ни успех, ни 402, ни ошибка не снимут оверлей.
    expect(page).toMatch(/finally \{\s*setBusy\(false\);/);
    expect(page).toMatch(/err\.code === "LIMIT_EXCEEDED"[\s\S]{0,200}?openPaywall\(\)/);
  });

  it("после перехода на карты оверлей не рисуется", () => {
    expect(page).toMatch(/!jumped &&/);
    expect(page).toMatch(/setJumped\(true\)/);
  });

  it("оверлей не держит собственного состояния", () => {
    // Компонент без состояния не может «залипнуть» в принципе.
    expect(magic).not.toMatch(/useState/);
    expect(magic).not.toMatch(/useEffect/);
    expect(magic).toMatch(/export default function DrawMagic\(\)/);
  });
});

describe("иконки: нет 404 на favicon", () => {
  it("иконки объявлены в metadata", () => {
    const layout = read("app/layout.tsx");
    expect(layout).toMatch(/icons: \{/);
    expect(layout).toMatch(/apple: "\/icon-192\.png"/);
  });

  it("файлы иконок на месте", () => {
    expect(fs.existsSync(path.join(root, "app/icon.png"))).toBe(true);
    expect(fs.existsSync(path.join(root, "public/favicon.ico"))).toBe(true);
  });
});
