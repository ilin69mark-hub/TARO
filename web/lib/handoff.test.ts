// Перенос покупки в Telegram: клиентская логика.
//
// Файл читает и исходники, и импортирует саму функцию. Разделение не
// случайное: правила «кнопка не рендерится вне Telegram» живут в условиях
// рендера, и без браузера их видно только в тексте, а вот разбор start_param и
// сборка ссылки — обычные чистые функции, их надо проверять значениями.
//
// Что здесь ловится и почему это дорого:
//
//   1) Ссылка собирается из значения, вшитого на сборке, а не из ответа
//      сервера. Серверный URL — вектор фишинга: скомпрометированный бэкенд увёл
//      бы человека с поддельной страницей вместе с токеном переноса, а по
//      токену — к покупке. То же правило в проекте уже применено к invoice_link
//      (PaywallSheet, «Аудит B»).
//   2) Токен едет в startapp, а не в query. Причина техническая: параметры к
//      t.me в Mini App не попадают, ссылка с «?t=» просто не сработала бы. Плюс
//      токен не оседает в адресной строке, истории и $current_url PostHog.
//   3) Вне Telegram переход идёт через location.replace: обычная навигация
//      оставила бы ссылку с токеном в истории браузера.
//   4) Токен уходит на сервер ровно один раз. Повтор дал бы HANDOFF_EXPIRED
//      вместо результата — на экране был бы отказ при успешном слиянии.
import { beforeEach, describe, expect, it, vi } from "vitest";
import fs from "node:fs";
import path from "node:path";

import { handoffToken, handoffStartLink, startHandoff, startParam } from "./me";

const read = (p: string) => fs.readFileSync(path.resolve(__dirname, "..", p), "utf8");
const handoffPage = read("app/handoff/page.tsx");
const me = read("lib/me.ts");
const api = read("lib/api.ts");

const TOKEN = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG";

type TG = { initData?: string; initDataUnsafe?: { start_param?: string }; openTelegramLink?: (u: string) => void };

function fakeTelegram(tg: TG): void {
  (window as unknown as { Telegram?: { WebApp?: TG } }).Telegram = { WebApp: tg };
}

describe("Разбор start_param", () => {
  beforeEach(() => {
    delete (window as unknown as { Telegram?: unknown }).Telegram;
  });

  it("префикс pay_ отбрасывается", () => {
    fakeTelegram({ initDataUnsafe: { start_param: `pay_${TOKEN}` } });
    expect(startParam()).toBe(`pay_${TOKEN}`);
    expect(handoffToken()).toBe(TOKEN);
  });

  it("чужой start_param переносом не считается", () => {
    // Бот умеет передавать start_param и для других целей (например, код
    // реферала). Считать любой параметр переносом значило бы молча сжечь
    // токен на чужом входе и показать человеку отказ.
    fakeTelegram({ initDataUnsafe: { start_param: "ref_abc123" } });
    expect(handoffToken()).toBe("");
  });

  it("отсутствие Telegram не роняет страницу", () => {
    expect(startParam()).toBe("");
    expect(handoffToken()).toBe("");
  });

  it("initDataUnsafe без start_param — это не перенос", () => {
    fakeTelegram({ initDataUnsafe: {} });
    expect(handoffToken()).toBe("");
  });
});

describe("Ссылка на перенос", () => {
  it("собирается из NEXT_PUBLIC_TG_APP_URL, а не из ответа сервера", () => {
    const old = process.env.NEXT_PUBLIC_TG_APP_URL;
    process.env.NEXT_PUBLIC_TG_APP_URL = "https://t.me/taro_bot/app";
    try {
      const link = handoffStartLink(TOKEN);
      expect(link).toBe(`https://t.me/taro_bot/app?startapp=pay_${TOKEN}`);
      // Ссылка не должна браться ни из чего, кроме сборки.
      expect(me).toMatch(/process\.env\.NEXT_PUBLIC_TG_APP_URL/);
    } finally {
      process.env.NEXT_PUBLIC_TG_APP_URL = old;
    }
  });

  it("не собирается, если адрес бота не задан на сборке", () => {
    const old = process.env.NEXT_PUBLIC_TG_APP_URL;
    process.env.NEXT_PUBLIC_TG_APP_URL = "";
    try {
      // Пустая ссылка, а не «ссылка на корень»: иначе человек уйдёт в
      // приложение без переноса и решит, что всё прошло.
      expect(handoffStartLink(TOKEN)).toBe("");
    } finally {
      process.env.NEXT_PUBLIC_TG_APP_URL = old;
    }
  });

  it("пустой токен не даёт ссылки", () => {
    const old = process.env.NEXT_PUBLIC_TG_APP_URL;
    process.env.NEXT_PUBLIC_TG_APP_URL = "https://t.me/taro_bot/app";
    try {
      expect(handoffStartLink("")).toBe("");
    } finally {
      process.env.NEXT_PUBLIC_TG_APP_URL = old;
    }
  });

  it("сервер не присылает ссылку в ответе", () => {
    // URL от сервера здесь был бы и лишним, и опасным: поле осталось бы в
    // контракте, и рано или поздно его начали бы заполнять.
    expect(me).not.toMatch(/api<[^>]*>\("\/auth\/handoff"[\s\S]{0,200}url/);
  });
});

describe("Переход в Telegram", () => {
  beforeEach(() => {
    delete (window as unknown as { Telegram?: unknown }).Telegram;
    process.env.NEXT_PUBLIC_TG_APP_URL = "https://t.me/taro_bot/app";
    vi.stubGlobal("fetch", vi.fn());
  });

  it("внутри Telegram открывается внутри Telegram и несёт токен", async () => {
    // Поведение, а не чтение исходника: после клика deep-link обязан уйти
    // через WebApp, иначе Telegram открылся бы новой вкладкой в браузере,
    // где нет ни initData, ни авторизации.
    const openTelegramLink = vi.fn();
    fakeTelegram({ initData: "x", openTelegramLink });
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ token: TOKEN, expires_in: 300 }), { status: 200 })
    );
    const ok = await startHandoff("month_299");
    expect(ok).toBe(true);
    expect(openTelegramLink).toHaveBeenCalledTimes(1);
    expect(openTelegramLink.mock.calls[0][0]).toBe(`https://t.me/taro_bot/app?startapp=pay_${TOKEN}`);
  });

  it("вне Telegram — переход, который не оставляет токен в истории", () => {
    // Поведение location.replace в jsdom не проверяется (там не настраивается
    // location), поэтому здесь фиксируется хотя бы решение: обычная навигация
    // оставила бы ссылку с токеном в истории браузера, а replace — нет.
    expect(me).toMatch(/window\.location\.replace\(link\)/);
    expect(me).not.toMatch(/window\.open\(link/);
  });

  it("перенос не начинается, если токен не выдали", async () => {
    // 429/503/404 от handoff — это отказ. Уводить человека в Telegram без
    // токена бессмысленно: он увидит «ссылка неполная» и не поймёт почему.
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "NOT_FOUND", message_ru: "Перенос недоступен" } }), {
        status: 404,
      })
    );
    expect(await startHandoff("month_299")).toBe(false);
  });

  it("токен не попадает в query-строку Mini App", () => {
    // Проверка на источнике: страница переноса обязана брать токен из
    // start_param. Чтение из location.search оставило бы токен в access-логе
    // nginx и в $current_url PostHog.
    expect(handoffPage).toMatch(/handoffToken\(\)/);
    expect(handoffPage).not.toMatch(/location\.search|useSearchParams/);
  });
});

describe("Одноразовость переноса на экране", () => {
  it("повторная отправка токена заблокирована", () => {
    // Токен одноразовый: вторая отправка получила бы HANDOFF_EXPIRED, и
    // человек увидел бы отказ при успешном слиянии.
    expect(handoffPage).toMatch(/useRef/);
    expect(handoffPage).toMatch(/if \(started\.current\) return/);
  });

  it("разные ошибки переноса говорят разные слова", () => {
    // HANDOFF_IP_MISMATCH — почти всегда смена сети, это не вина человека;
    // HANDOFF_EXPIRED — надо начать заново. Один текст на оба случая отправил
    // бы половину людей переделывать перенос по инструкции для другого.
    expect(handoffPage).toMatch(/HANDOFF_IP_MISMATCH/);
    expect(handoffPage).toMatch(/HANDOFF_EXPIRED/);
  });

  it("о предупреждении не переслать ссылку сказано словами, а не только значком", () => {
    expect(handoffPage).toMatch(/Не пересылайте эту ссылку/);
  });
});

describe("Код ошибки доезжает до клиента", () => {
  it("api отдаёт код из тела ошибки", () => {
    // Без этого linkTelegram() не отличил бы ALREADY_LINKED (не ошибка) от
    // FP_MISMATCH, и ветки в клиенте были мёртвыми: любой отказ выглядел
    // одинаково, а перенос не смог бы тихо перевыдать токен.
    expect(api).toMatch(/err\.code = body\?\.error\?\.code/);
  });
});
