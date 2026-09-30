// A14/F-18.6: вторая сторона SSE-контракта — сам клиент postReadingSSE.
//
// Кадры ниже — ровно те, что шлёт api/internal/readings (проверяется в
// readings/sse_contract_test.go). Смысл теста: клиент обязан вытащить токены и
// reading_id из потока, а НЕ развалиться и НЕ нарисовать пустоту. Раньше SSE
// не был покрыт ни с одной стороны, поэтому смена формы кадра (например
// "token" → "text") тихо ломала расклад в проде.
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { postReadingSSE } from "./api";

function sseStream(frames: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder();
  const body = frames.map((f) => `data: ${f}\n\n`).join("");
  return new ReadableStream<Uint8Array>({
    start(controller) {
      // Режем поток на куски: клиент обязан склеивать части кадра через буфер,
      // иначе границы TCP-чанков ломают JSON.
      const bytes = enc.encode(body);
      controller.enqueue(bytes.slice(0, 17));
      controller.enqueue(bytes.slice(17, 60));
      controller.enqueue(bytes.slice(60));
      controller.close();
    },
  });
}

function mockFetch(body: ReadableStream<Uint8Array>, status = 200) {
  return vi.fn(async () => new Response(body, { status, headers: { "Content-Type": "text/event-stream" } }));
}

const REQ = { spread_code: "daily", question: "что будет?", idempotency_key: "k1" };

describe("postReadingSSE — контракт с api/internal/readings", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", mockFetch(sseStream([
      '{"token":"Карты "}',
      '{"token":"говорят."}',
      '{"done":true,"reading_id":"r-1","status":"done"}',
    ])));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("собирает токены и возвращает reading_id/status", async () => {
    const seen: string[] = [];
    const res = await postReadingSSE(REQ, (t) => seen.push(t));
    expect(seen).toEqual(["Карты ", "говорят."]);
    expect(res).toEqual({ reading_id: "r-1", status: "done" });
  });

  it("не падает и возвращает reading_id на потоке с fallback-кадром", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream([
      '{"fallback":true}',
      '{"done":true,"reading_id":"r-fb","status":"done"}',
    ])));
    const seen: string[] = [];
    const res = await postReadingSSE(REQ, (t) => seen.push(t));
    expect(seen).toEqual([]); // fallback не должен рисовать «токены»
    expect(res.reading_id).toBe("r-fb");
  });

  it("не падает на незнакомом поле и не выдаёт его за токен", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream([
      '{"text":"это не токен"}',
      '{"done":true,"reading_id":"r-2","status":"done"}',
    ])));
    const seen: string[] = [];
    const res = await postReadingSSE(REQ, (t) => seen.push(t));
    // Регрессия контракта: переименование token → text не должно приводить к
    // тихой отрисовке мусора.
    expect(seen).toEqual([]);
    expect(res.reading_id).toBe("r-2");
  });

  it("402 превращается в ошибку с кодом LIMIT_EXCEEDED", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 402 })));
    await expect(postReadingSSE(REQ, () => {})).rejects.toMatchObject({ code: "LIMIT_EXCEEDED" });
  });
});

// Ранний кадр с reading_id: он приходит ДО токенов, чтобы веб мог открыть
// страницу с картами, не дожидаясь генерации толкования. Без этого весь
// флоу «нажми → увидь карты» стоял бы на пустом экране.
describe("postReadingSSE — ранний reading_id", () => {
  afterEach(() => vi.unstubAllGlobals());

  const PENDING_FIRST = [
    '{"reading_id":"r-early","status":"pending"}',
    '{"token":"Карты "}',
    '{"token":"говорят."}',
    '{"done":true,"reading_id":"r-early","status":"done"}',
  ];

  it("сообщает id до генерации — по нему можно перейти сразу", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream(PENDING_FIRST)));
    const ids: string[] = [];
    const tokens: string[] = [];
    const res = await postReadingSSE(REQ, (t) => tokens.push(t), (id) => ids.push(id));
    expect(ids, "id обязан прийти до конца потока").toEqual(["r-early"]);
    expect(tokens).toEqual(["Карты ", "говорят."]);
    expect(res.reading_id).toBe("r-early");
  });

  it("не вызывает колбэк дважды на финальном кадре", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream(PENDING_FIRST)));
    const ids: string[] = [];
    await postReadingSSE(REQ, () => undefined, (id) => ids.push(id));
    // Ранний кадр и финальный несут один id — повторный вызов дёрнул бы
    // router.push второй раз.
    expect(ids).toHaveLength(1);
  });

  it("без колбэка работает как раньше (обратная совместимость)", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream(PENDING_FIRST)));
    const res = await postReadingSSE(REQ, () => undefined);
    expect(res).toEqual({ reading_id: "r-early", status: "done" });
  });

  it("на потоке без раннего кадра колбэк всё равно получит id из done", async () => {
    vi.stubGlobal("fetch", mockFetch(sseStream([
      '{"token":"Карты "}',
      '{"done":true,"reading_id":"r-late","status":"done"}',
    ])));
    const ids: string[] = [];
    const res = await postReadingSSE(REQ, () => undefined, (id) => ids.push(id));
    expect(ids).toEqual(["r-late"]);
    expect(res.reading_id).toBe("r-late");
  });
});
