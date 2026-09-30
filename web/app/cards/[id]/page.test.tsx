// Страница значения карты /cards/[id] обязана отдавать арт.
//
// Дефект: страница брала из БД `image_key`, но нигде его не использовала —
// рендерила только текст. CardArt жил исключительно в /reading/[id], поэтому
// единственная страница каталога карт оставалась без картинки. И это молчало:
// страница отдаёт 200, вёрстка выглядит нормально, сломан только ассет.
//
// Тест рендерит серверный компонент страницы напрямую (он async, поэтому
// через ReactDOM-рендер не идёт) и проверяет, что в выводе есть <img> с
// путём = /<image_key>, а не fallback-рубашка. fetch подменён, поэтому тест
// живёт без Go и без Postgres.
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import CardPage from "@/app/cards/[id]/page";

vi.mock("next/image", () => ({
  default: (props: { src: string; alt: string; width: number; height: number }) => (
    // eslint-disable-next-line @next/next/no-img-element -- нужен обычный <img>, чтобы читать src
    <img src={props.src} alt={props.alt} width={props.width} height={props.height} />
  ),
}));

const CARD = {
  id: 0,
  name_ru: "Дурак",
  upright_ru: "Новое начало, шаг в неизвестность.",
  reversed_ru: "Безрассудство или страх сделать шаг.",
  image_key: "cards/major-00-fool.webp",
};

const originalFetch = globalThis.fetch;

function mockGo(body: unknown, ok = true) {
  globalThis.fetch = vi.fn(async () =>
    new Response(JSON.stringify(body), {
      status: ok ? 200 : 404,
      headers: { "content-type": "application/json" },
    })
  ) as unknown as typeof fetch;
}

async function renderPage(id: string) {
  const element = await CardPage({ params: Promise.resolve({ id }) });
  return renderToStaticMarkup(element);
}

describe("/cards/[id] рендерит арт карты", () => {
  beforeEach(() => mockGo(CARD));
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("отдаёт <img> с src = /<image_key>, а не fallback", async () => {
    const html = await renderPage("0");
    expect(html).toContain(`src="/${CARD.image_key}"`);
    // alt = name_ru: картинка должна быть подписана, а не пустой <img>.
    expect(html).toContain(`alt="${CARD.name_ru}"`);
  });

  it("меняет арт вместе с картой, а не рисует всегда первую", async () => {
    const second = { ...CARD, id: 13, name_ru: "Смерть", image_key: "cards/major-13-death.webp" };
    mockGo(second);
    const html = await renderPage("13");
    expect(html).toContain('src="/cards/major-13-death.webp"');
    expect(html).not.toContain(CARD.image_key);
  });

  it("размер = width и пропорция 5:8 (height = width * 8 / 5)", async () => {
    const html = await renderPage("0");
    expect(html).toMatch(/width="200"/);
    expect(html).toMatch(/height="320"/);
  });

  it("не падает и показывает «Карта не найдена», если Go не ответил", async () => {
    mockGo({ error: { message_ru: "Карта не найдена" } }, false);
    const html = await renderPage("0");
    expect(html).toContain("Карта не найдена");
    expect(html).not.toContain("<img");
  });

  it("не фетчит невалидный id — allowlist до запроса", async () => {
    await renderPage("../etc/passwd");
    expect(globalThis.fetch).not.toHaveBeenCalled();
  });
});
