// Расклад перестроен в три колонки: слева действия, по центру карты, справа
// толкование. Владелец: «описание карты из нижней части в боковую справа,
// кнопки слева, не сильно крупно, страница без скрола».
//
// Что тут ловится, чего не видно на глаз:
//   1) Страница без скролла держится на `100dvh` + `overflow-hidden`. Если
//      вернуть `vh`, на мобильных адресная строка срежет низ и скролл вернётся.
//   2) Толкование ушло в колонку фиксированной ширины, поэтому длинный текст
//      НЕ должен растягивать сетку. Скролл допустим только ВНУТРИ колонки.
//   3) Кнопки должны остаться компактными: большая заливка/крупный кегль
//      перетягивает внимание с карт — ровно то, чего просил избежать владелец.
//   4) Порядок колонок на мобильном: карты первыми, действия ниже. Иначе на
//      телефоне пользователь видит кнопки вместо карт.
import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import ReadingPage from "@/app/reading/[id]/page";

vi.mock("next/image", () => ({
  default: (p: { src: string; alt: string; width: number; height: number }) => (
    // eslint-disable-next-line @next/next/no-img-element -- нужен обычный <img>, чтобы читать src
    <img src={p.src} alt={p.alt} width={p.width} height={p.height} />
  ),
}));
vi.mock("next/headers", () => ({ headers: async () => new Headers() }));
vi.mock("@/components/cinematic/CinematicBackdrop", () => ({
  default: () => <div data-testid="backdrop" />,
}));
// ShareButtons на SSR всегда возвращает null: токен приватной ссылки приходит
// из /api/share уже на клиенте. Мокаем, чтобы проверять РАЗМЕТКУ кнопок, а не
// их наличие после сетевого запроса.
vi.mock("@/components/ShareButtons", () => ({
  default: (_p: { compact?: boolean }) => (
    <div data-testid="share" className={_p?.compact ? "share-compact" : "share-wide"}>
      <a className="share-tg">Поделиться в TG</a>
      <button className="share-copy">Скопировать ссылку</button>
    </div>
  ),
}));

const READING_ID = "0195f2c1-1111-4222-8333-444455556666";
const originalFetch = globalThis.fetch;

type Card = { card_id: number; name_ru: string; image_key: string; position: number; reversed?: boolean };

function cards(n: number): Card[] {
  return Array.from({ length: n }, (_, i) => ({
    card_id: i,
    name_ru: `Карта ${i}`,
    image_key: `cards/major-0${i}-fool.webp`,
    position: i + 1,
  }));
}

function mockReading(n: number, locked = false) {
  const body = {
    id: READING_ID,
    question: "Проверка",
    spread: "three",
    locked,
    interpretation: "Толкование расклада.",
    cards: cards(n),
  };
  globalThis.fetch = vi.fn(async () =>
    new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  ) as unknown as typeof fetch;
}

async function render(n: number, locked = false) {
  mockReading(n, locked);
  const el = await ReadingPage({ params: Promise.resolve({ id: READING_ID }) });
  return renderToStaticMarkup(el);
}

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe("расклад в три колонки без скролла", () => {
  it("страница занимает ровно один экран: 100dvh + overflow-hidden", async () => {
    const html = await render(3);
    // dvh, а не vh: мобильный браузер прячет адресную строку, vh даёт обрезку.
    expect(html).toMatch(/h-\[100dvh\]/);
    // vh без dvh — признак регрессии.
    expect(html).not.toMatch(/h-\[100vh\]/);
  });

  it("контейнер раскладки реально обрезан: без overflow-hidden вернётся скролл", async () => {
    const html = await render(3);
    // Ищем overflow-hidden именно на grid-контейнере раскладки. Раньше проверка
    // была `expect(html).toMatch(/overflow-hidden/)` — и она проходила, даже когда
    // с контейнера класс убрали, потому что строка встречалась в другом месте.
    const grid = html.slice(0, html.indexOf("</main>"));
    const gridClass = /class="([^"]*grid h-\[100dvh\][^"]*)"/.exec(grid)?.[1] ?? "";
    expect(gridClass, "не найден grid-контейнер раскладки").not.toBe("");
    expect(gridClass, "overflow-hidden снят с контейнера раскладки").toMatch(/overflow-hidden/);
  });

  it("толкование ушло в правую колонку фиксированной ширины, а не вниз под карты", async () => {
    const html = await render(3);
    // Правая колонка задаётся в grid: [действия | карты | описание].
    // 13rem слева — подпись «Действия» с разрядкой обрезалась в 10rem.
    expect(html).toMatch(/lg:grid-cols-\[13rem_minmax\(0,1fr\)_26rem\]/);
    const iText = html.indexOf("Толкование расклада.");
    const iLastCard = html.lastIndexOf("major-0");
    expect(iText).toBeGreaterThan(iLastCard);
  });

  it("кнопки действий в левой колонке, ДО карт", async () => {
    const html = await render(3);
    // Сверяем по разметке, а не по тексту: текст «Карта 0» дублируется в
    // preload-ссылках в начале документа, а нужна вторая картинка в раскладке.
    const iActions = html.indexOf("Действия");
    const iFirstCard = html.indexOf('alt="Карта 0"');
    expect(iActions).toBeGreaterThan(-1);
    expect(iActions).toBeLessThan(iFirstCard);
  });

  it("кнопки компактные: помечены compact и не перетягивают внимание", async () => {
    const html = await render(3);
    // ShareButtons обязан получить compact — иначе расклад вернёт крупные
    // кнопки с font-semibold и заливкой, ровно то, что просил убрать владелец.
    expect(html).toContain("share-compact");
    expect(html).not.toContain("share-wide");
  });

  it("на мобильном карты идут ПЕРВЫМИ, действия ниже", async () => {
    const html = await render(3);
    // Точные значения на КАЖДОЙ из трёх колонок, а не «где-то есть order-N»:
    // иначе смена 2→1 на колонке действий проходила незамеченной.
    const actions = /class="(order-\d[^"]*flex flex-col items-start[^"]*)"/.exec(html)?.[1] ?? "";
    const cardsCol = /class="(order-\d[^"]*flex min-h-0 flex-col[^"]*)"/.exec(html)?.[1] ?? "";
    const textCol = /class="(order-\d[^"]*min-h-0 overflow-y-auto[^"]*)"/.exec(html)?.[1] ?? "";
    expect(actions, "не найдена колонка действий").not.toBe("");
    expect(cardsCol, "не найдена колонка карт").not.toBe("");
    expect(textCol, "не найдена колонка описания").not.toBe("");
    // Карта дня: карты → описание → действия.
    expect(cardsCol).toMatch(/^order-1\b/);
    expect(textCol).toMatch(/^order-3\b/);
    expect(actions).toMatch(/^order-2\b/);
  });

  it("длинное толкование скроллится внутри своей колонки, а не двигает страницу", async () => {
    const html = await render(3);
    // overflow-y-auto именно на колонке описания.
    expect(html).toMatch(/order-3 min-h-0 overflow-y-auto/);
  });

  it("карты остаются в сетке с ограничением ширины сверху", async () => {
    for (const n of [1, 3, 5, 10]) {
      const html = await render(n);
      const limits = [...html.matchAll(/max-width:(\d+)px/g)].map((m) => Number(m[1]));
      expect(limits.length, `${n} карт: ограничение не найдено`).toBe(n);
      for (const px of limits) {
        // Верхняя граница обязана быть такой, чтобы расклад влез в ОДИН экран:
        // карта 5:8 => высота = ширина * 1.6, плюс подпись. Для 10 карт в два
        // ряда это ~2*1.6*W + заголовок, поэтому W держим в пределах 260..70.
        expect(px, `${n} карт: ${px}px велика`).toBeLessThanOrEqual(260);
        expect(px, `${n} карт: ${px}px мала`).toBeGreaterThanOrEqual(70);
      }
    }
  });

  it("высоту полосы карт задаёт flex, а НЕ жёсткий max-height", async () => {
    const html = await render(10);
    // Раньше стоял max-height в vw — он срезал верхний ряд, карты уезжали под
    // заголовок. Теперь высоту даёт flex-1 + overflow-hidden, а сетка центрирует
    // содержимое. Возврат жёсткого предела — регрессия.
    expect(html).not.toMatch(/max-height:\d+vw/);
    expect(html).toMatch(/flex-1[^"]*overflow-hidden/);
    expect(html).toMatch(/content-center/);
  });

  it("карты центрируются по вертикали, а не липнут к заголовку", async () => {
    const html = await render(10);
    // justify-items:center держит карты по центру колонки, auto-rows-min не
    // даёт строкам раздуться на всю высоту.
    expect(html).toMatch(/justify-items-center/);
    expect(html).toMatch(/auto-rows-min/);
  });

  it("чем больше карт, тем они мельче", async () => {
    const widthOf = async (n: number) => {
      const html = await render(n);
      return Number(/max-width:(\d+)px/.exec(html)?.[1] ?? "0");
    };
    expect(await widthOf(1)).toBeGreaterThan(await widthOf(3));
    expect(await widthOf(3)).toBeGreaterThan(await widthOf(5));
    expect(await widthOf(5)).toBeGreaterThan(await widthOf(10));
  });

  it("пропорция карты 5:8 соблюдается", async () => {
    const html = await render(3);
    for (const m of html.matchAll(/width="(\d+)" height="(\d+)"/g)) {
      expect(Number(m[2])).toBe(Math.round((Number(m[1]) * 8) / 5));
    }
  });

  it("при locked кнопок нет, но paywall с тарифами остаётся", async () => {
    const html = await render(3, true);
    expect(html).not.toContain("Поделиться в TG");
    expect(html).not.toContain("Записать мысли");
    expect(html).toContain("Тарифы");
  });

  it("длинный вопрос не ломает высоту — обрезается одной строкой", async () => {
    const html = await render(3);
    expect(html).toMatch(/truncate/);
  });
});
