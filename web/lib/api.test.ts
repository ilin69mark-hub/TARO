// Unit: SSE-парсинг, paywall-счетчик, события (см. D-покрытие, T16/U14).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { postReadingSSE } from "@/lib/api";
import { events } from "@/lib/analytics";

function sseBody(frames: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder();
  const chunks = frames.map((f) => enc.encode(`data: ${f}\n\n`));
  return new ReadableStream({
    start(c) {
      for (const ch of chunks) c.enqueue(ch);
      c.close();
    },
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  vi.stubGlobal("localStorage", {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
    clear: () => undefined,
  } as unknown as Storage);
});

describe("postReadingSSE", () => {
  it("собирает токены и финал", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        status: 200,
        body: sseBody([
          `{"token":"Карты "}`,
          `{"token":"вытянуты"}`,
          `{"done":true,"reading_id":"r1","status":"done"}`,
        ]),
      }))
    );
    const tokens: string[] = [];
    const res = await postReadingSSE(
      { spread_code: "daily", idempotency_key: "k" },
      (t) => tokens.push(t)
    );
    expect(tokens.join("")).toBe("Карты вытянуты");
    expect(res.reading_id).toBe("r1");
    expect(res.status).toBe("done");
  });

  it("402 → LIMIT_EXCEEDED", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 402, json: async () => ({}) }))
    );
    await expect(
      postReadingSSE({ spread_code: "daily", idempotency_key: "k" }, () => undefined)
    ).rejects.toMatchObject({ code: "LIMIT_EXCEEDED" });
  });
});

describe("events", () => {
  it("имена заморожены спекой 08", () => {
    expect(events).toMatchObject({
      visit: "visit",
      spreadOpen: "spread_open",
      readingDone: "reading_done",
      paywallShow: "paywall_show",
      shareDone: "share_done",
    });
  });
});
