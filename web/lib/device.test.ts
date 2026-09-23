// Unit: гейты Lite/mobile (см. 08-ultra, 09-premium-temple, D-покрытие).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { shouldUseLite, isFinePointer } from "@/lib/device";

function mockEnv(opts: { reduced?: boolean; calm?: boolean; mem?: number; cpu?: number; pointer?: string }) {
  const store: Record<string, string> = {};
  if (opts.calm) store["taro_calm"] = "1";
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store[k] ?? null,
    setItem: (k: string, v: string) => {
      store[k] = v;
    },
    removeItem: (k: string) => {
      delete store[k];
    },
    clear: () => {
      for (const k of Object.keys(store)) delete store[k];
    },
  } as Storage);
  vi.stubGlobal("matchMedia", (q: string) => ({
    matches:
      (q.includes("prefers-reduced-motion") && !!opts.reduced) ||
      (q.includes("pointer: fine") && opts.pointer === "fine"),
  }));
  vi.stubGlobal("navigator", {
    hardwareConcurrency: opts.cpu ?? 8,
    ...(opts.mem ? { deviceMemory: opts.mem } : {}),
  } as Navigator);
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("shouldUseLite", () => {
  it("мощный десктоп — 3D", () => {
    mockEnv({ pointer: "fine" });
    expect(shouldUseLite()).toBe(false);
  });
  it("reduced-motion — Lite", () => {
    mockEnv({ reduced: true, pointer: "fine" });
    expect(shouldUseLite()).toBe(true);
  });
  it("спокойный режим — Lite", () => {
    mockEnv({ calm: true, pointer: "fine" });
    expect(shouldUseLite()).toBe(true);
  });
  it("слабая память — Lite", () => {
    mockEnv({ mem: 2, pointer: "fine" });
    expect(shouldUseLite()).toBe(true);
  });
  it("мало CPU — Lite", () => {
    mockEnv({ cpu: 2, pointer: "fine" });
    expect(shouldUseLite()).toBe(true);
  });
});

describe("isFinePointer", () => {
  it("fine — true", () => {
    mockEnv({ pointer: "fine" });
    expect(isFinePointer()).toBe(true);
  });
  it("coarse — false", () => {
    mockEnv({ pointer: "coarse" });
    expect(isFinePointer()).toBe(false);
  });
});
