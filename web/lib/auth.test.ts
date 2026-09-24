import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Values = Record<string, string>;

function makeStorage(initial: Values = {}) {
  const values: Values = { ...initial };
  return {
    values,
    getItem: (key: string) => values[key] ?? null,
    setItem: (key: string, value: string) => {
      values[key] = value;
    },
    removeItem: (key: string) => {
      delete values[key];
    },
    clear: () => {
      for (const key of Object.keys(values)) delete values[key];
    },
  };
}

function installStorage(localValues: Values = {}, sessionValues: Values = {}) {
  const local = makeStorage(localValues);
  const session = makeStorage(sessionValues);
  vi.stubGlobal("localStorage", local);
  vi.stubGlobal("sessionStorage", session);
  vi.stubGlobal("window", {
    localStorage: local,
    sessionStorage: session,
    Telegram: undefined,
  });
  return { local, session };
}

function response(ok: boolean, status = 200): Response {
  return { ok, status } as Response;
}

function bodyOf(call: unknown[]): Record<string, unknown> {
  const init = call[1] as RequestInit;
  return JSON.parse(String(init.body));
}

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("auth bootstrap", () => {
  it("memoizes the request and retries explicitly", async () => {
    installStorage({ taro_uuid: "00000000-0000-4000-8000-000000000001" });
    const fetchMock = vi.fn().mockResolvedValue(response(true));
    vi.stubGlobal("fetch", fetchMock);
    const { ensureAuth, retryAuth } = await import("@/lib/auth");

    const first = ensureAuth();
    const second = ensureAuth();
    expect(first).toBe(second);
    await first;
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await retryAuth();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps legacy fingerprint once and relies on the cookie marker after reload", async () => {
    const stores = installStorage({
      taro_uuid: "00000000-0000-4000-8000-000000000002",
      taro_fp: "legacy-fingerprint",
    });
    const randomValues = vi.fn((bytes: Uint8Array) => {
      bytes.fill(7);
      return bytes;
    });
    vi.stubGlobal("crypto", {
      randomUUID: () => "00000000-0000-4000-8000-000000000003",
      getRandomValues: randomValues,
    });
    const fetchMock = vi.fn().mockResolvedValue(response(true));
    vi.stubGlobal("fetch", fetchMock);

    const first = await import("@/lib/auth");
    await first.ensureAuth();
    expect(bodyOf(fetchMock.mock.calls[0])).toMatchObject({ fingerprint: "legacy-fingerprint" });
    expect(first.getFp()).toBe("legacy-fingerprint");
    expect(stores.local.values.taro_fp).toBeUndefined();
    expect(stores.local.values.taro_fp_ready).toBe("1");

    vi.resetModules();
    const second = await import("@/lib/auth");
    await second.ensureAuth();
    expect(bodyOf(fetchMock.mock.calls[1])).not.toHaveProperty("fingerprint");
    expect(randomValues).not.toHaveBeenCalled();
  });

  it("clears the memoized promise after a failed request", async () => {
    installStorage({ taro_uuid: "00000000-0000-4000-8000-000000000004" });
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(response(true));
    vi.stubGlobal("fetch", fetchMock);
    const { ensureAuth, retryAuth } = await import("@/lib/auth");

    const pending = ensureAuth();
    expect(ensureAuth()).toBe(pending);
    await expect(pending).rejects.toThrow("offline");
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await retryAuth();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
