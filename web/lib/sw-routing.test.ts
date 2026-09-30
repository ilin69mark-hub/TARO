// Роутинг сервис-воркера — это политика кэша, и ошибка в ней молчаливая:
// пользователь получает устаревшую страницу и не понимает почему.
//
// Регрессия: правило `url.pathname.startsWith("/cards/")` ловило под кэш ВСЁ под
// этим префиксом, включая HTML-страницу /cards/<id>. CacheFirst для неизменяемой
// картинки .webp уместен; для страницы значения карты — нет: её текст меняется в БД,
// а кэш живёт до ручного bump VERSION в sw.js. То есть баг не «протухает», он
// залипает навсегда.
//
// Тест реально исполняет sw.js и проверяет, какой путь уходит в кэш, а какой —
// в сеть. Проверять исходник регуляркой было бы слабее: правило может измениться
// по форме и остаться тем же по смыслу.
import { describe, expect, it, vi } from "vitest";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";

const SW_PATH = path.resolve(__dirname, "../public/sw.js");

type Handler = (event: unknown) => void;

function loadServiceWorker() {
  const source = fs.readFileSync(SW_PATH, "utf8");
  const listeners: Record<string, Handler> = {};
  const store = new Map<string, Response>();

  const cachesStub = {
    open: vi.fn(async () => ({
      match: async (req: Request) => store.get(req.url) || undefined,
      put: async (req: Request, res: Response) => {
        store.set(req.url, res);
      },
      addAll: async () => undefined,
    })),
    keys: async () => [store.size ? "taro-v1" : "taro-v1"],
    delete: async () => true,
  };

  const selfStub = {
    addEventListener: (type: string, handler: Handler) => {
      listeners[type] = handler;
    },
    skipWaiting: () => undefined,
    clients: { claim: async () => undefined },
  };

  const fetchMock = vi.fn(async (req: Request) => {
    const body = `network:${new URL(req.url).pathname}`;
    return new Response(body, { status: 200, headers: { "content-type": "text/html" } });
  });

  const sandbox = {
    self: selfStub,
    caches: cachesStub,
    fetch: fetchMock,
    Response,
    Request,
    URL,
    console,
  };
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);

  return { fetchHandler: listeners.fetch, store, fetchMock };
}

// Прогоняет один запрос через зарегистрированный fetch-хендлер.
// Ответ живёт в объекте, а не в переменной: TS сужает переменную, присвоенную
// только внутри колбэка respondWith, до never — и не видит результата.
async function dispatch(
  loaded: ReturnType<typeof loadServiceWorker>,
  url: string,
  mode = "no-cors"
) {
  const captured: { value: Promise<Response> | null } = { value: null };
  const request = new Request(url, { method: "GET" });
  const event = {
    request,
    mode,
    respondWith: (p: Promise<Response> | Response) => {
      captured.value = Promise.resolve(p);
    },
    waitUntil: () => undefined,
  };
  loaded.fetchHandler?.(event);
  return captured.value;
}

describe("service worker cache routing", () => {
  it("registers a fetch handler at all (sanity: sw.js evaluated)", () => {
    expect(loadServiceWorker().fetchHandler).toBeTypeOf("function");
  });

  it("caches card art (.webp) — immutable, адресован по содержимому", async () => {
    const loaded = loadServiceWorker();
    const url = "http://localhost/cards/major-00-fool.webp";

    const first = await dispatch(loaded, url, "cors");
    expect(first?.status).toBe(200);
    expect(await first?.text()).toBe("network:/cards/major-00-fool.webp");
    expect(loaded.store.has(url)).toBe(true);

    // Второй раз — из кэша, сети не касаемся: это и есть смысл CacheFirst.
    const before = loaded.fetchMock.mock.calls.length;
    const second = await dispatch(loaded, url, "cors");
    expect(await second?.text()).toBe("network:/cards/major-00-fool.webp");
    expect(loaded.fetchMock.mock.calls.length).toBe(before);
  });

  it("НЕ кэширует HTML-страницу карты /cards/<id> — иначе она залипает навсегда", async () => {
    const loaded = loadServiceWorker();
    const url = "http://localhost/cards/0";

    await dispatch(loaded, url, "navigate");
    expect(loaded.store.has(url), "страница карты попала в кэш").toBe(false);
  });

  it("НЕ кэширует /cards/[id] даже в режиме cors (не только navigate)", async () => {
    const loaded = loadServiceWorker();
    const url = "http://localhost/cards/13";

    await dispatch(loaded, url, "cors");
    expect(loaded.store.has(url), "страница карты попала в кэш через другое правило").toBe(false);
  });

  it("не кэширует прочие страницы под /cards/ без .webp", async () => {
    const loaded = loadServiceWorker();
    for (const path of ["/cards/", "/cards/0/", "/cards/abc", "/cards/x.png"]) {
      await dispatch(loaded, `http://localhost${path}`, "cors");
      expect(loaded.store.has(`http://localhost${path}`), `кэширован ${path}`).toBe(false);
    }
  });
});
