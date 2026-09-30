// Правки по наблюдениям владельца на главной, в каталоге раскладов и профиле.
// Проверяем по исходникам: это правка разметки, и она молча откатывается —
// ни один сборочный шаг не ловит «дисклеймер вернулся» или «кнопка удаления
// переехала наверх».
import path from "node:path";
import fs from "node:fs";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);

const root = path.resolve(__dirname, "..");
const read = (p: string) => fs.readFileSync(path.join(root, p), "utf8");
const home = read("app/page.tsx");
// Главная разбита на документ (page.tsx) и экран (HomeHero.tsx): сцена, заставка
// и контент живут в компоненте, в page.tsx осталась только разметка документа.
const hero = read("components/HomeHero.tsx");
const spreads = read("app/spreads/page.tsx");
const profile = read("app/profile/page.tsx");
const tabbar = read("components/TabBar.tsx");

describe("главная: дисклеймер не дублируется", () => {
  it("текста-дубля под кнопкой нет", () => {
    // Он же есть в подвале layout и на модалке 18+ (Legal.tsx).
    expect(home).not.toMatch(/Это инструмент самопознания и рефлексии/);
    expect(home).not.toMatch(/не медицинская/);
  });

  it("кнопка «Выбрать расклад» осталась", () => {
    // Кнопка переехала в компонент экрана, но контракт страницы — тот же.
    expect(hero).toMatch(/Выбрать расклад/);
    expect(hero).toMatch(/href="\/spreads"/);
  });

  it("дисклеймер по-прежнему не дублируется на экране", () => {
    expect(hero).not.toMatch(/Это инструмент самопознания и рефлексии/);
    expect(hero).not.toMatch(/не медицинская/);
  });
});

describe("каталог раскладов: free слева, premium справа, без скролла", () => {
  it("расклады разделены на две колонки", () => {
    expect(spreads).toMatch(/const free = spreads\.filter\(\(s\) => !s\.is_premium\)/);
    expect(spreads).toMatch(/const premium = spreads\.filter\(\(s\) => s\.is_premium\)/);
    // free ПЕРВАЯ колонка, premium ВТОРАЯ — порядок задаётся порядком вызовов.
    const iFree = spreads.indexOf("column(free");
    const iPremium = spreads.indexOf("column(premium");
    expect(iFree).toBeGreaterThan(-1);
    expect(iPremium).toBeGreaterThan(iFree);
  });

  it("на телефоне колонки становятся строками, на широком — двумя", () => {
    expect(spreads).toMatch(/content-center grid-cols-1/);
    expect(spreads).toMatch(/sm:grid-cols-2/);
    // Строки не фиксируем: content-center центрирует блок целиком, поэтому
    // фиксированные grid-rows-1/-2 прижимали колонки к верху.
    expect(spreads).not.toMatch(/grid-rows-/);
  });

  it("страница без скролла: 100dvh + overflow внутри колонок", () => {
    expect(spreads).toMatch(/h-\[100dvh\]/);
    expect(spreads).toMatch(/overflow-y-auto/);
    // dvh, а не vh: мобильный браузер прячет адресную строку.
    expect(spreads).not.toMatch(/h-\[100vh\]/);
  });

  it("подписи колонок русские", () => {
    expect(spreads).toMatch(/Бесплатные/);
    // Название колонки — аргумент функции column(), а не текстовый узел.
    expect(spreads).toMatch(/column\(premium, "Премиум", "premium"\)/);
    expect(spreads).toMatch(/column\(free, "Бесплатные", "free"\)/);
    expect(spreads).toMatch(/бесплатно/);
  });

  it("JSON-LD и мета не пострадали", () => {
    expect(spreads).toMatch(/export const metadata/);
    expect(spreads).toMatch(/force-dynamic/);
  });
});

describe("меню: дневник стал пятым пунктом", () => {
  it("в меню пять пунктов, дневник среди них", () => {
    const tabs = [...tabbar.matchAll(/\{ href: "([^"]+)", label: "([^"]+)"/g)].map((m) => m[2]);
    expect(tabs).toHaveLength(5);
    expect(tabs).toContain("Дневник");
  });

  it("сетка рассчитана на пять колонок", () => {
    expect(tabbar).toMatch(/grid-cols-5/);
  });

  it("все пункты ведут на существующие маршруты", () => {
    // Ошибка была: добавил «Карта» на /reading, а такого роута нет —
    // маршрут /reading/[id] требует UUID. Пункт без страницы = 404.
    const hrefs = [...tabbar.matchAll(/\{ href: "([^"]+)"/g)].map((m) => m[1]);
    expect(hrefs).toEqual(["/", "/spreads", "/history", "/diary", "/profile"]);
  });

  it("в профиле ссылка на дневник убрана", () => {
    expect(profile).not.toMatch(/Дневник →/);
    expect(profile).not.toMatch(/href="\/diary"/);
  });
});

describe("профиль: блок «друг или подруга»", () => {
  it("заголовок переименован и объясняет механику", () => {
    expect(profile).toMatch(/Пригласи друга/);
    expect(profile).toMatch(/Приглашённый делает первый расклад/);
    expect(profile).toMatch(/\+3 дня/);
  });

  it("код виден и подписан", () => {
    expect(profile).toMatch(/ref \? ref\.code/);
    // «Чужой код» звучало загадочно: непонятно, чей и зачем.
    expect(profile).not.toMatch(/Чужой код/);
    expect(profile).toMatch(/Есть код\? Впишите его/);
    expect(profile).toMatch(/Код из 8 символов/);
  });

  it("кнопка называет действие, а не «OK»", () => {
    expect(profile).not.toMatch(/>\s*OK\s*</);
    expect(profile).toMatch(/Применить/);
  });

  it("у поля ввода есть подпись (label), а не только aria-label", () => {
    expect(profile).toMatch(/htmlFor="ref-code"/);
    expect(profile).toMatch(/id="ref-code"/);
  });
});

describe("профиль: спокойный режим по центру", () => {
  it("блок и кнопка центрированы", () => {
    const iCalm = profile.indexOf("Спокойный режим");
    const cardBefore = profile.slice(Math.max(0, iCalm - 300), iCalm);
    expect(cardBefore, "карточка «Спокойный режим» не отцентрована").toMatch(/text-center/);
  });

  it("кнопка отделена от подписи пустым местом", () => {
    // Раньше </p> и <button> слипались в одну строку без отступа.
    expect(profile).toMatch(/Спокойный режим<\/p>\s*<p/);
    expect(profile).toMatch(/className="mt-3 rounded-2xl/);
  });
});

describe("профиль: удаление данных", () => {
  it("кнопка красная и в самом низу — после блока «Приложение»", () => {
    const iApp = profile.indexOf("Приложение");
    const iDel = profile.indexOf("Удалить мои данные");
    expect(iApp).toBeGreaterThan(-1);
    expect(iDel).toBeGreaterThan(iApp);
    expect(profile).toMatch(/text-red-400/);
    expect(profile).toMatch(/border-red-500\/30/);
  });

  it("есть модалка подтверждения с ролью dialog", () => {
    expect(profile).toMatch(/role="dialog"/);
    expect(profile).toMatch(/aria-modal="true"/);
    expect(profile).toMatch(/Удалить все мои данные\?/);
  });

  it("«Отмена» подсвечена и идёт первой", () => {
    const modal = profile.slice(profile.indexOf('role="dialog"'));
    const iDelete = modal.indexOf("Удалить навсегда");
    // В модалке «Отмена» встречается дважды: в подсказке «нажмите Отмена» и в самой
    // кнопке. Кнопка — последнее вхождение, её className идёт ПЕРЕД текстом (JSX).
    const iBtn = modal.lastIndexOf("Отмена");
    expect(iBtn, "в модалке нет кнопки «Отмена»").toBeGreaterThan(-1);
    // Отмена — первая кнопка: ошибочное нажатие не должно удалять.
    expect(iBtn, "«Отмена» идёт не первой кнопкой").toBeLessThan(iDelete);
    const near = modal.slice(Math.max(0, iBtn - 400), iBtn);
    expect(near, "«Отмена» без золотой подсветки").toMatch(/border-gold\/60/);
    expect(modal).toMatch(/autoFocus/);
  });

  it("системных confirm/prompt больше нет", () => {
    // Два окна подряд, неуправляемый ввод — плохая практика удаления.
    // В комментарии слово осталось намеренно, поэтому режем комментарии.
    const code = profile.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    expect(code).not.toMatch(/\bconfirm\s*\(/);
    expect(code).not.toMatch(/\bprompt\s*\(/);
  });

  it("требуется явное слово для необратимого действия", () => {
    expect(profile).toMatch(/confirmText/);
    expect(profile).toMatch(/УДАЛИТЬ/);
  });
});

// Второй заход замечаний владельца: выравнивание колонок, нейтральные
// формулировки, центрирование. Это ровно те вещи, которые молча откатываются
// при следующей правке вёрстки.
describe("каталог: верхние карточки обеих колонок на одной линии", () => {
  it("списки прижаты к верху, а не разнесены по центру", () => {
    // content-center ВНУТРИ списка разносил колонки с разным числом раскладов:
    // верхняя карточка премиума не совпадала с верхней бесплатной. Проверяем
    // именно теги <ul>, не весь файл: на обёртке content-center нужен (центр).
    const lists = spreads.match(/<ul className="[^"]*"/g) ?? [];
    expect(lists.length).toBeGreaterThan(0);
    for (const l of lists) expect(l, l).toMatch(/content-start/);
  });

  it("заголовок колонки стоит над карточками, а не в шапке экрана", () => {
    // Отвязываем заголовок от верха экрана: он принадлежит своему списку.
    const h2Class = spreads.slice(spreads.indexOf("<h2"), spreads.indexOf("</h2>"));
    expect(h2Class, "у заголовка колонки нет подчёркивания-разделителя").toMatch(/border-b/);
    expect(h2Class, "заголовок колонки не отцентрован").toMatch(/text-center/);
    // Заголовок должен быть НИЖЕ по коду, чем список, который он озаглавливает,
    // и над ним — отступ, разделяющий их визуально.
    const h2 = spreads.indexOf("<h2");
    const ul = spreads.indexOf("<ul");
    expect(h2).toBeGreaterThan(-1);
    expect(ul).toBeGreaterThan(h2);
    const ulTag = spreads.slice(ul, spreads.indexOf(">", ul));
    expect(ulTag, "список карточек не отделён от своего заголовка").toMatch(/mt-3/);
  });
});

describe("профиль: заголовок и карточки по центру", () => {
  it("слово «Профиль» отцентровано", () => {
    expect(profile).toMatch(/<h1 className="text-center[^"]*">\s*Профиль<\/h1>/);
  });

  it("каждая карточка профиля отцентрована", () => {
    // Считаем карточки (section с bg-card) и text-center на них.
    const cards = profile.match(/<section className="[^"]*bg-card[^"]*"/g) ?? [];
    expect(cards.length).toBeGreaterThanOrEqual(5);
    const withCenter = profile.match(/<section className="[^"]*bg-card[^"]*text-center"/g) ?? [];
    expect(withCenter.length).toBe(cards.length);
  });

  it("лишние text-center внутри центрированной карточки убраны", () => {
    // Двойное центрирование — мусор в разметке.
    const code = profile.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    expect(code).not.toMatch(/text-center text-2xl/);
  });
});

describe("профиль: формулировки без отсылки к полу", () => {
  const visible = (m?: string) => (m ? profile.slice(m.indexOf(m[0]) - 300, m.indexOf(m[0]) + 300) : "");

  it("нет отсылки к «её» в объяснении бонуса", () => {
    // Владелец: отсылка к девушке, с этим боролись. Формулировка нейтральная.
    expect(profile).not.toMatch(/после её/);
    expect(profile).not.toMatch(/первого расклада\s+приглашённой/);
    expect(profile).toMatch(/после первого расклада приглашённого/);
  });

  it("в подписи блока нет «друг или подруга»", () => {
    // Заголовок оставлен (он сам по себе нейтрален), ругалась подпись под ним.
    expect(profile).not.toMatch(/Он или она делает первый расклад/);
    expect(profile).toMatch(/вы оба получаете/);
  });

  it("«тяжело тянет 3D» заменено на понятное «высокая нагрузка»", () => {
    // Сленг разработчика в пользовательском тексте — не то, что читает человек.
    expect(profile).not.toMatch(/тянет 3D/);
    expect(profile).not.toMatch(/тяжело тянет/);
    expect(profile).toMatch(/Автоматически включается при высокой\s+нагрузке/);
  });
});

describe("модалка удаления: прямое указание на «Отмена»", () => {
  it("предлагает нажать «Отмена», а не «закройте окно»", () => {
    // «Закройте окно» — недействующий совет: в браузере его нечем закрыть.
    const i = profile.indexOf('role="dialog"');
    expect(i).toBeGreaterThan(-1);
    const modal = profile.slice(i, i + 2600);
    expect(modal).toMatch(/нажмите «Отмена»/);
    expect(modal).not.toMatch(/просто закройте окно/);
  });
});

describe("модалка удаления: подтверждение вводом — рабочее", () => {
  const modal = () => {
    const i = profile.indexOf('role="dialog"');
    return profile.slice(i);
  };

  it("поле для ввода слова УДАЛИТЬ существует и связано с проверкой", () => {
    // Баг: remove() сверяет confirmText, но поля для его ввода не было —
    // удаление невозможно было завершить, кнопка молча ничего не делала.
    expect(modal()).toMatch(/<input[\s\S]{0,200}?id="del-confirm"/);
    expect(modal()).toMatch(/value=\{confirmText\}/);
    expect(modal()).toMatch(/setConfirmText\(e\.target\.value\)/);
  });

  it("слово УДАЛИТЬ в подсказке совпадает с проверкой", () => {
    // Подсказка и код сверки разойдутся — пользователь не сможет удалить.
    expect(modal()).toMatch(/впишите УДАЛИТЬ/);
    expect(profile).toMatch(/confirmText\.trim\(\)\.toUpperCase\(\) !== "УДАЛИТЬ"/);
  });

  it("поле подписано, а не голое", () => {
    expect(modal()).toMatch(/htmlFor="del-confirm"/);
  });
});

// Третий заход: центрирование. Владелец трижды возвращался к этому —
// значит, причина системная (блоки без mx-auto / без justify-center),
// а не разовая.
describe("каталог: блок колонок по центру экрана", () => {
  it("колонки центрируются по вертикали, а не прижаты к верху", () => {
    // flex-1 растягивал сетку на всю высоту, и блок прилегал к верху.
    expect(spreads).toMatch(/content-center grid-cols-1/);
  });

  it("колонки не растягиваются по высоте (иначе центр не работает)", () => {
    // h-fit: колонка по содержимому, max-h-full: при нехватке места — скролл.
    expect(spreads).toMatch(/<section className="flex h-fit min-h-0 max-h-full flex-col">/);
  });

  it("внутри колонок верх карточек всё ещё совпадает", () => {
    // content-center на уровне колонок НЕ должен возвращаться внутрь списков.
    expect(spreads).toMatch(/content-start gap-3/);
  });
});

describe("профиль: текст и напоминания по центру", () => {
  it("текст «Спокойного режима» центрирован, а не прижат влево", () => {
    // max-w без mx-auto: текст внутри центрирован, но сам блок стоит слева —
    // владелец это видел как «текст снизу не по центру».
    expect(profile).toMatch(/mx-auto mt-1 max-w-xs text-sm text-mist/);
  });

  it("блок «Напоминаний» отцентрован", () => {
    expect(profile).toMatch(/mx-auto max-w-sm text-center">\s*<PushPrefs \/>/);
  });

  it("строка «Час / Со звуком / ОК» центрирована как группа", () => {
    // justify-center в самом компоненте: обёртка карточки не помогает,
    // потому что flex-строка тянется на всю ширину.
    const prefs = read("components/PushPrefs.tsx");
    expect(prefs).toMatch(/flex flex-wrap items-center justify-center gap-2/);
  });
});

describe("дневник: название по центру", () => {
  it("заголовок «Дневник» отцентрован", () => {
    const diary = read("app/diary/page.tsx");
    expect(diary).toMatch(/<h1 className="text-center[^"]*">Дневник<\/h1>/);
  });
});

// Проверки выше читают исходники как ТЕКСТ. Так они не ловят синтаксис:
// комментарий в позиции выражения тернарника роняет компиляцию, а тесты
// остаются зелёными. Поэтому страницы, которые мы правили руками, должны
// ещё и компилироваться.
describe("правленые страницы компилируются", () => {
  const pages = [
    "app/page.tsx",
    "app/spreads/page.tsx",
    "app/profile/page.tsx",
    "app/diary/page.tsx",
    "components/TabBar.tsx",
    "components/PushPrefs.tsx",
  ];

  for (const p of pages) {
    it(p, () => {
      // typescript уже есть в devDependencies — новую зависимость не тянем.
      const ts = require("typescript") as typeof import("typescript");
      // transpileModule с reportDiagnostics даёт СИНТАКСИЧЕСКИЕ ошибки —
      // ровно те, что роняют сборку, не разбирая типы.
      const out = ts.transpileModule(read(p), {
        fileName: p,
        reportDiagnostics: true,
        compilerOptions: {
          jsx: ts.JsxEmit.ReactJSX,
          target: ts.ScriptTarget.ESNext,
          module: ts.ModuleKind.ESNext,
        },
      });
      const msgs = (out.diagnostics ?? []).map((d) => ts.flattenDiagnosticMessageText(d.messageText, " "));
      expect(msgs, `${p} не компилируется`).toEqual([]);
    });
  }

  it("в JSX-детях нет одиночного блока-комментария", () => {
    // `{/* ... */}` внутри тернарника (вне JSX-детей) — синтаксическая ошибка.
    const spreads = read("app/spreads/page.tsx");
    expect(spreads).not.toMatch(/\)\s*:\s*\(\s*\{\s*\/\*/);
  });
});
