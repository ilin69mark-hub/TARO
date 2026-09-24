import { afterEach, describe, expect, it, vi } from "vitest";
import type { NextRequest } from "next/server";
import { copyResponseHeaders, fwdHeaders, passThrough } from "@/lib/proxy";

function sourceWithCookies(cookies: string[], values: Record<string, string> = {}): Headers {
  const source = {
    getSetCookie: () => cookies,
    get: (key: string) => {
      if (key.toLowerCase() === "set-cookie") return cookies.join(", ");
      return values[key] || null;
    },
    forEach: (callback: (value: string, key: string) => void) => {
      for (const [key, value] of Object.entries(values)) callback(value, key);
      if (cookies.length) callback(cookies.join(", "), "set-cookie");
    },
  };
  return source as unknown as Headers;
}

function targetHeaders(): Headers {
  return { set: vi.fn(), append: vi.fn() } as unknown as Headers;
}

describe("copyResponseHeaders", () => {
  it("preserves ordinary headers and appends every cookie", () => {
    const source = sourceWithCookies(
      ["taro_jwt=jwt; Path=/; HttpOnly", "taro_fp=fp; Path=/; HttpOnly", "taro_csrf=csrf; Path=/"],
      { "content-type": "application/json", "cache-control": "no-store", connection: "keep-alive" }
    );
    const target = targetHeaders();

    copyResponseHeaders(source, target);

    expect(target.set).toHaveBeenCalledWith("content-type", "application/json");
    expect(target.set).toHaveBeenCalledWith("cache-control", "no-store");
    expect(target.set).not.toHaveBeenCalledWith("connection", "keep-alive");
    expect(target.append).toHaveBeenNthCalledWith(1, "set-cookie", "taro_jwt=jwt; Path=/; HttpOnly");
    expect(target.append).toHaveBeenNthCalledWith(2, "set-cookie", "taro_fp=fp; Path=/; HttpOnly");
    expect(target.append).toHaveBeenNthCalledWith(3, "set-cookie", "taro_csrf=csrf; Path=/");
  });

  it("uses the header fallback when getSetCookie is unavailable", () => {
    const source = {
      get: (key: string) => (key.toLowerCase() === "set-cookie" ? "a=1; Path=/" : null),
      forEach: (callback: (value: string, key: string) => void) => callback("a=1; Path=/", "set-cookie"),
    } as unknown as Headers;
    const target = targetHeaders();

    copyResponseHeaders(source, target);

    expect(target.append).toHaveBeenCalledWith("set-cookie", "a=1; Path=/");
  });

  it("keeps all cookies in a NextResponse", async () => {
    const source = new Response("{}", {
      status: 201,
      headers: { "content-type": "application/json", "x-trace": "trace-1" },
    });
    source.headers.append("set-cookie", "taro_jwt=jwt; Path=/; HttpOnly");
    source.headers.append("set-cookie", "taro_fp=fp; Path=/; HttpOnly");
    source.headers.append("set-cookie", "taro_csrf=csrf; Path=/");

    const result = await passThrough(source);
    const cookies = result.headers.get("set-cookie") || "";

    expect(cookies).toContain("taro_jwt=jwt; Path=/; HttpOnly");
    expect(cookies).toContain("taro_fp=fp; Path=/; HttpOnly");
    expect(cookies).toContain("taro_csrf=csrf; Path=/");
    expect(result.headers.get("x-trace")).toBe("trace-1");
  });
});

describe("fwdHeaders", () => {
  const originalPublicOrigin = process.env.PUBLIC_ORIGIN;

  afterEach(() => {
    if (originalPublicOrigin === undefined) delete process.env.PUBLIC_ORIGIN;
    else process.env.PUBLIC_ORIGIN = originalPublicOrigin;
  });

  it("uses a configured origin instead of client host headers", () => {
    process.env.PUBLIC_ORIGIN = "https://taro.example/base";
    const req = {
      headers: new Headers({
        cookie: "taro_jwt=jwt",
        origin: "https://evil.example",
        referer: "https://evil.example/private",
        "x-forwarded-host": "evil.example",
        host: "evil.example",
        "x-csrf": "csrf",
        "idempotency-key": "idem-1",
      }),
    } as unknown as NextRequest;

    const headers = fwdHeaders(req);

    expect(headers.Origin).toBe("https://taro.example");
    expect(headers.Referer).toBe("https://taro.example");
    expect(headers["X-Forwarded-Host"]).toBe("taro.example");
    expect(headers.Cookie).toBe("taro_jwt=jwt");
    expect(headers["X-CSRF"]).toBe("csrf");
    expect(headers["Idempotency-Key"]).toBe("idem-1");
  });
});
