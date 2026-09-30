import { describe, it, expect } from "vitest";
import { NextRequest } from "next/server";
import { fwdHeaders, realIP } from "@/lib/proxy";

// X-Real-IP доезжал только до auth/anon, а остальные 11 прокси-роутов его
// теряли. Последствия: IP-антиферма рефералки работала вхолостую (юнит-тесты
// Go были зелёные — они звали хендлер напрямую с заголовком, и поймать это
// мог только прогон живой стеки), а ratelimit с byUser=false ключевал по
// пустому IP — то есть все анонимные юзеры делили один бакет.
describe("fwdHeaders: проброс реального IP", () => {
  const req = (headers: Record<string, string>) =>
    new NextRequest("http://taro.local/api/referral/apply", { headers });

  it("берёт IP из X-Real-IP от nginx", () => {
    expect(realIP(req({ "x-real-ip": "203.0.113.7" }))).toBe("203.0.113.7");
  });

  it("берёт первый элемент, если nginx дописал цепочку", () => {
    expect(realIP(req({ "x-real-ip": "203.0.113.7, 10.0.0.1" }))).toBe("203.0.113.7");
  });

  it("не доверяет X-Forwarded-For: клиент подделывает первый элемент", () => {
    // Если бы мы читали XFF, атакующий подставил бы что угодно.
    expect(realIP(req({ "x-forwarded-for": "1.2.3.4" }))).toBe("unknown");
  });

  it("пустой/мусорный заголовок даёт unknown, а не пустую строку", () => {
    // Пустая строка склеила бы всех анонимных юзеров в один ключ ratelimit.
    expect(realIP(req({}))).toBe("unknown");
    expect(realIP(req({ "x-real-ip": "   " }))).toBe("unknown");
  });

  it("fwdHeaders всегда несёт непустой X-Real-IP", () => {
    expect(fwdHeaders(req({ "x-real-ip": "198.51.100.9" }))["X-Real-IP"]).toBe("198.51.100.9");
    expect(fwdHeaders(req({}))["X-Real-IP"]).toBe("unknown");
  });

  it("не теряет cookie и CSRF", () => {
    const h = fwdHeaders(req({ "x-real-ip": "198.51.100.9", cookie: "taro_jwt=x", "x-csrf": "y" }));
    expect(h.Cookie).toBe("taro_jwt=x");
    expect(h["X-CSRF"]).toBe("y");
  });
});
