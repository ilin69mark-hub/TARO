// Хронометраж книги магии.
//
// Анимация без проверки ломается молча: страницы начинают листаться из
// закрытой книги, книга раскрывается после посадки вместо подхода, или
// поворот крышки на 90° превращает разворот в «лодку». Всё это видно только
// на экране и только в определённой секунде, поэтому порядок фаз и их
// величины зафиксированы здесь числами.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const src = fs.readFileSync(
  path.resolve(__dirname, "../components/cinematic/MagicBook.tsx"),
  "utf8"
);

/** Достаёт числовое значение константы из исходника. */
const num = (name: string) => {
  const m = src.match(new RegExp(`const ${name} = ([-\\d.]+);`));
  if (!m) throw new Error(`не нашли константу ${name}`);
  return Number(m[1]);
};

const T = (name: string) => {
  const m = src.match(new RegExp(`${name}: ([\\d.]+),`));
  if (!m) throw new Error(`не нашли фазу ${name}`);
  return Number(m[1]);
};

describe("Порядок фаз", () => {
  it("появление → парение → посадка → покой", () => {
    expect(T("appear")).toBeLessThan(T("hover"));
    expect(T("hover")).toBeLessThan(T("land"));
    expect(T("land")).toBeLessThan(T("rest"));
  });

  it("книга раскрывается НА ПОДХОДЕ, а не после посадки", () => {
    // По замыслу: «при приближении к алтарю раскрывается». Если open0 позже
    // land — книга сначала сядет и только потом откроется, и это уже другое
    // зрелище, чем задумано.
    expect(T("open0")).toBeLessThan(T("land"));
    expect(T("open1")).toBeGreaterThan(T("land"));
  });

  it("раскрытие заканчивается ДО перелистывания", () => {
    // Иначе первая страница отрывается от блока, который ещё ходит.
    expect(T("open1")).toBeLessThanOrEqual(T("flip0"));
  });

  it("последняя страница ложится до наступления покоя", () => {
    const pages = src.match(/const PAGES = (\d+);/);
    const step = num("FLIP_STEP");
    const dur = num("FLIP_DUR");
    expect(pages).not.toBeNull();
    const last = T("flip0") + (Number(pages![1]) - 1) * step + dur;
    expect(last).toBeLessThanOrEqual(T("rest"));
  });
});

describe("Раскрытие — это разворот, а не «лодка»", () => {
  it("правая половина поворачивается на 180°", () => {
    // Поворот на 35-40° даёт две доски, торчащие из корешка. Настоящая
    // книга раскрывается разворотом: правая половина ложится на левую.
    expect(src).toMatch(/right\.current\.rotation\.z = Math\.PI \* \(1 - open\)/);
  });

  it("ось правой половины — на верхней грани стопки", () => {
    // Ось ниже или выше стопки: при повороте на 180° половина либо
    // проваливается в левую, либо взлетает над ней и книга «зависает» в
    // воздухе двумя половинами.
    const pivot = src.match(/const RIGHT_PIVOT = ([A-Z_]+);/);
    expect(pivot).not.toBeNull();
    expect(pivot![1]).toBe("TOP_Y");
    const top = src.match(/const TOP_Y = ([^;]+);/);
    expect(top![1]).toMatch(/COVER_T \+ BLOCK_T/);
  });
});

describe("Левитация и посадка", () => {
  it("парит заметно выше алтаря, а не на сантиметр", () => {
    expect(num("HOVER_Y")).toBeGreaterThan(0.4);
  });

  it("вращение гаснет к посадке, а не продолжается", () => {
    // Угол отсчитывается от нуля и домножается на (1 - land), а не
    // накапливается: иначе книта прокрутит лишний круг при посадке.
    expect(src).toMatch(/rotation\.y = t < T\.hover \? t \* SPIN : T\.hover \* SPIN \* \(1 - land\)/);
  });

  it("подъём компенсирует наклон к зрителю", () => {
    // При наклоне передний край уходит вниз; без подъёма книга наполовину
    // утоплена в алтаре.
    expect(src).toMatch(/const REST_LIFT = Math\.sin\(TILT_REST\) \* \(D \/ 2\)/);
    expect(src).toMatch(/REST_LIFT \* land/);
  });

  it("наклон в покое положительный: страницы развёрнуты к зрителю", () => {
    // Отрицательный наклон отворачивает разворот от камеры: книга
    // раскрыта, но её не видно.
    expect(num("TILT_REST")).toBeGreaterThan(0);
  });
});

describe("Перелистывание", () => {
  it("лист идёт с правой стопки на левую", () => {
    expect(src).toMatch(/f\.rotation\.z = Math\.PI \* p/);
  });

  it("лист приподнимается в середине хода", () => {
    // Плоский поворот читается как картон. Подъём + изгиб в двух шарнирах
    // дают дугу.
    expect(src).toMatch(/Math\.sin\(p \* Math\.PI\) \* 0\.05/);
    const hinges = src.match(/bends\.current\[i \* 2 \+ 1\]/);
    expect(hinges).not.toBeNull();
  });

  it("до своего черёда лист не существует", () => {
    // Иначе он лежит внутри закрытой книги и торчит сквозь крышку.
    expect(src).toMatch(/f\.visible = t >= start/);
  });
});

describe("Ассетов нет — только процедурная геометрия", () => {
  it("никаких внешних моделей и загрузчиков", () => {
    // Проект грузит только 78 карт WebP. Возврат к GLB без решения по
    // лицензии и бюджету — это регресс, а не улучшение.
    expect(src).not.toMatch(/useGLTF|\.glb|KTX2|Draco/);
  });
});
