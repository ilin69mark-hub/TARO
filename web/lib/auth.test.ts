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

// Анонимная личность была невосстановима: fingerprint живёт в httpOnly-cookie,
// localStorage хранит только маркер готовности. Потеря cookie => getFp() == "" =>
// 403 FP_REQUIRED навсегда, потому что генерировать новый fingerprint строка
// `if (fingerprintReady()) return ""` не даёт, а сервер на новый ответил бы
// FP_MISMATCH (в БД у uuid записан старый). Пользователь оставался заперт в
// стартовой сессии. Теперь 403 от привязки сбрасывает личность и заводит новую.
describe("recovery from an unrecoverable fingerprint", () => {
  const READY = { taro_fp_ready: "1", taro_uuid: "00000000-0000-4000-8000-00000000000a" };

  function fpLockout(code: string): Response {
    return new Response(JSON.stringify({ error: { code, message_ru: "Устройство не узнано" } }), {
      status: 403,
      headers: { "content-type": "application/json" },
    });
  }

  function stubCrypto() {
    vi.stubGlobal("crypto", {
      randomUUID: () => "00000000-0000-4000-8000-00000000000b",
      getRandomValues: vi.fn((bytes: Uint8Array) => {
        bytes.fill(9);
        return bytes;
      }),
    });
  }

  for (const code of ["FP_REQUIRED", "FP_MISMATCH"]) {
    it(`resets the identity and retries once on ${code}`, async () => {
      const stores = installStorage({ ...READY });
      stubCrypto();
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(fpLockout(code))
        .mockResolvedValueOnce(response(true));
      vi.stubGlobal("fetch", fetchMock);

      const { ensureAuth } = await import("@/lib/auth");
      await ensureAuth();

      expect(fetchMock).toHaveBeenCalledTimes(2);
      // Первая попытка шла с запертым состоянием (без fingerprint), вторая — с
      // новым uuid И новым fingerprint, то есть это новая личность, не подделка.
      expect(bodyOf(fetchMock.mock.calls[0])).not.toHaveProperty("fingerprint");
      expect(bodyOf(fetchMock.mock.calls[1])).toMatchObject({
        uuid: "00000000-0000-4000-8000-00000000000b",
        fingerprint: "09090909090909090909090909090909",
      });
      expect(stores.local.values.taro_fp_ready).toBe("1");
    });
  }

  it("does not loop when the reset identity is rejected too", async () => {
    installStorage({ ...READY });
    stubCrypto();
    const fetchMock = vi.fn().mockResolvedValue(fpLockout("FP_REQUIRED"));
    vi.stubGlobal("fetch", fetchMock);

    const { ensureAuth } = await import("@/lib/auth");
    await expect(ensureAuth()).rejects.toThrow("Auth failed: 403");
    // Ровно две попытки: исходная и одна с новой личностью. Третьей быть не должно.
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps the identity for 403 that is not a fingerprint lockout", async () => {
    const stores = installStorage({ ...READY });
    stubCrypto();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: "FORBIDDEN", message_ru: "Неверный CSRF-токен" } }), {
          status: 403,
          headers: { "content-type": "application/json" },
        })
      )
      .mockResolvedValueOnce(response(true));
    vi.stubGlobal("fetch", fetchMock);

    const { ensureAuth } = await import("@/lib/auth");
    await expect(ensureAuth()).rejects.toThrow("Auth failed: 403");

    // Чужой 403 не должен стирать личность: uuid на месте, попыток ровно одна.
    expect(stores.local.values.taro_uuid).toBe("00000000-0000-4000-8000-00000000000a");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
