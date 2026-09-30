// A20/F-13: 19 из 22 роутов Next собирали ответ руками и копировали только
// content-type. Терялись:
//   - Set-Cookie — вместе с ним ротация CSRF и обновление сессии;
//   - Retry-After — при 429 клиент не знал, когда повторить;
//   - Content-Disposition, X-Request-Id и любые будущие заголовки Go.
//
// Тест один и табличный: он вызывает КАЖДЫЙ экспортированный HTTP-метод каждого
// роута с подменённым fetch, который отдаёт ответ Go с cookie и Retry-After, и
// требует, чтобы оба заголовка дошли до ответа роута. Новый роут без relay
// ломает таблицу, а не «случайно» теряет заголовки в проде.
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { NextRequest } from "next/server";
import path from "node:path";
import { fileURLToPath } from "node:url";
import fs from "node:fs";

// Корень проекта, а не каталог теста: тест лежит в lib/, роуты — в app/api.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const apiRoot = path.join(root, "app", "api");

type Handler = (req: NextRequest, ctx?: unknown) => Promise<Response>;

type RouteSpec = { file: string; method: string; handler: Handler };

// params для [id]/[token]-роутов: подставляем заведомо валидные значения, чтобы
// проверка дошла до relay, а не остановилась на локальной валидации 422.
const PARAMS: Record<string, string> = {
  id: "0195f2c1-1111-4222-8333-444455556666",
  token: "0123456789abcdef0123456789abcdef",
};

function requestFor(method: string, file: string): NextRequest {
  const url = `http://127.0.0.1:3000/api/${routePath(file)}`;
  return new NextRequest(url, {
    method,
    headers: {
      cookie: "taro_jwt=jwt",
      "x-csrf": "csrf",
      "x-real-ip": "203.0.113.9",
      "content-type": "application/json",
    },
    body: method === "GET" || method === "DELETE" ? undefined : "{}",
  });
}

function routePath(file: string): string {
  return path
    .relative(apiRoot, path.join(root, file))
    .replace(/\/route\.ts$/, "")
    .replace(/\[(\w+)\]/g, (_, key) => PARAMS[key] || "x");
}

function routeFiles(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name === "route.ts") out.push(full);
    }
  };
  walk(apiRoot);
  return out.sort();
}

async function loadRoutes(): Promise<RouteSpec[]> {
  const specs: RouteSpec[] = [];
  for (const file of routeFiles()) {
    const relative = path.relative(root, file);
    const mod = (await import(/* @vite-ignore */ path.join(root, relative))) as Record<
      string,
      unknown
    >;
    for (const method of ["GET", "POST", "PUT", "DELETE", "PATCH"]) {
      const handler = mod[method];
      if (typeof handler === "function") {
        specs.push({ file: relative, method, handler: handler as Handler });
      }
    }
  }
  return specs;
}

// goResponse — ответ Go, который обязан дойти до браузера целиком.
function goResponse(): Response {
  const res = new Response(JSON.stringify({ ok: true }), {
    status: 429,
    headers: {
      "content-type": "application/json",
      "retry-after": "60",
      "x-request-id": "req-1",
    },
  });
  res.headers.append("set-cookie", "taro_jwt=jwt; Path=/; HttpOnly");
  res.headers.append("set-cookie", "taro_csrf=rotated; Path=/");
  res.headers.append("set-cookie", "taro_fp=fp; Path=/; HttpOnly");
  return res;
}

// setCookieList достаёт cookie ОТДЕЛЬНЫМИ заголовками. Через `get()` их не
// различить: и append трёх значений, и set одной склеенной строки дают
// одинаковый текст через запятую. `getSetCookie()` показывает реальную
// структуру, а именно она решает судьбу логаута: браузер не считает запятую
// разделителем в Set-Cookie, поэтому склейка = потеря всех cookie кроме первой.
function setCookieList(headers: Headers): string[] {
  const source = headers as Headers & { getSetCookie?: () => string[] };
  if (typeof source.getSetCookie === "function") {
    try {
      return source.getSetCookie();
    } catch {
      return [];
    }
  }
  const value = headers.get("set-cookie");
  return value ? [value] : [];
}

const COOKIES = ["taro_jwt=jwt; Path=/; HttpOnly", "taro_csrf=rotated; Path=/", "taro_fp=fp; Path=/; HttpOnly"];

describe("routeHeaderSurvival", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn(async () => goResponse()) as unknown as typeof fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("finds every route in app/api", async () => {
    const specs = await loadRoutes();
    // Ровно 21 роут: 22 было до удаления POST /api/diary/export — экспорт
    // дневника убран по требованию владельца (кнопка «Скачать все записи (JSON)»),
    // вместе с роутом и Go-обработчиком. Счётчик важен: новый роут обязан попасть
    // в таблицу, а «молчаливо уехавший» файл не должен делать тест пустым.
    expect(specs.length).toBeGreaterThanOrEqual(22);
    expect(new Set(specs.map((s) => s.file)).size).toBe(21);
    // Удалённый роут не должен вернуться молча.
    expect(specs.map((s) => s.file)).not.toContain(path.join("app", "api", "diary", "export", "route.ts"));
  });

  it("keeps Set-Cookie, Retry-After and status on every route", async () => {
    const specs = await loadRoutes();
    expect(specs.length).toBeGreaterThan(0);
    const failures: string[] = [];

    for (const spec of specs) {
      const req = requestFor(spec.method, spec.file);
      const ctx = { params: Promise.resolve({ ...PARAMS }) };
      let out: Response;
      try {
        out = await spec.handler(req, ctx);
      } catch (error) {
        failures.push(`${spec.method} ${spec.file}: handler threw ${String(error)}`);
        continue;
      }
      const cookies = out.headers.get("set-cookie") || "";
      const missing: string[] = [];
      if (out.status !== 429) missing.push(`status ${out.status} != 429`);
      if (out.headers.get("retry-after") !== "60") missing.push("no Retry-After");
      if (out.headers.get("x-request-id") !== "req-1") missing.push("no X-Request-Id");
      for (const cookie of COOKIES) {
        if (!cookies.includes(cookie)) missing.push(`cookie lost: ${cookie}`);
      }
      if (missing.length) {
        failures.push(`${spec.method} ${spec.file}: ${missing.join("; ")}`);
      }
    }

    expect(failures, failures.join("\n")).toEqual([]);
  });

  it("keeps every cookie as its own header (joining breaks logout)", async () => {
    const specs = await loadRoutes();
    const failures: string[] = [];
    for (const spec of specs) {
      const out = await spec.handler(requestFor(spec.method, spec.file), {
        params: Promise.resolve({ ...PARAMS }),
      });
      const list = setCookieList(out.headers);
      if (list.length !== COOKIES.length) {
        failures.push(
          `${spec.method} ${spec.file}: ${list.length} Set-Cookie header(s), want ${COOKIES.length}: ${JSON.stringify(list)}`
        );
        continue;
      }
      for (const cookie of COOKIES) {
        if (!list.includes(cookie)) failures.push(`${spec.method} ${spec.file}: lost ${cookie}`);
      }
    }
    expect(failures, failures.join("\n")).toEqual([]);
  });

  it("never copies hop-by-hop headers", async () => {
    const specs = await loadRoutes();
    globalThis.fetch = vi.fn(async () => {
      const res = goResponse();
      res.headers.set("connection", "keep-alive");
      res.headers.set("transfer-encoding", "chunked");
      return res;
    }) as unknown as typeof fetch;
    for (const spec of specs) {
      const out = await spec.handler(requestFor(spec.method, spec.file), {
        params: Promise.resolve({ ...PARAMS }),
      });
      expect(out.headers.get("connection"), `${spec.file}`).toBeNull();
      expect(out.headers.get("transfer-encoding"), `${spec.file}`).toBeNull();
    }
  });
});

// Тест потока: буферизация SSE съела бы «живой» поток — клиент получил бы все
// токены только после конца генерации, и рамка «жди ответ, потом читай» на
// стороне браузера работала бы вхолостую. Поэтому проверяем не «тело равно»,
// а ПОРЯДОК: первый чанк обязан прийти ДО того, как вверх отдадут второй.
describe("readings SSE stream is not buffered", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("delivers the first chunk before upstream finishes", async () => {
    let releaseSecond: (() => void) | null = null;
    const gate = new Promise<void>((resolve) => {
      releaseSecond = resolve;
    });
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode("data: {\"token\":\"Карты\"}\n\n"));
        void gate.then(() => {
          controller.enqueue(encoder.encode("data: {\"done\":true}\n\n"));
          controller.close();
        });
      },
    });
    globalThis.fetch = vi.fn(async () => {
      const res = new Response(body, {
        status: 200,
        headers: { "content-type": "text/event-stream", "x-request-id": "req-sse" },
      });
      res.headers.append("set-cookie", "taro_csrf=rotated; Path=/");
      return res;
    }) as unknown as typeof fetch;

    const mod = await import("@/app/api/readings/route");
    const req = new NextRequest("http://127.0.0.1:3000/api/readings", {
      method: "POST",
      headers: {
        accept: "text/event-stream",
        "content-type": "application/json",
        cookie: "taro_jwt=jwt",
        "x-csrf": "csrf",
      },
      body: "{}",
    });
    const out = await mod.POST(req);

    // Заголовки теряться не должны даже в потоковом пути.
    expect(out.headers.get("x-request-id")).toBe("req-sse");
    expect(setCookieList(out.headers)).toEqual(["taro_csrf=rotated; Path=/"]);

    const reader = out.body?.getReader();
    expect(reader).toBeTruthy();
    const first = await Promise.race([
      reader!.read(),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("first chunk waited for the rest of the stream")), 1500)
      ),
    ]);
    expect(new TextDecoder().decode(first.value)).toContain("Карты");
    // Второй чанк отпускаем только после проверки.
    releaseSecond!();
    await reader!.read();
  });
});
