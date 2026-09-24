import { ImageResponse } from "@vercel/og";
import { NextRequest } from "next/server";

// OG динамический (query q/s), без prerender.
export const dynamic = "force-dynamic";

// OG-картинка 1200×630 (см. U11). Только вопрос + название расклада из query,
// БЕЗ толкования (приватность: толкование видит только владелец, см. U12).
export const runtime = "nodejs";

export async function GET(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  // Токен-шеринг: вопрос тянем сервером по токену (в URL вопроса нет).
  const t = searchParams.get("t") || "";
  let title = (searchParams.get("q") || "Мой расклад").slice(0, 80);
  let spread = (searchParams.get("s") || "Таро").slice(0, 40);
  if (/^[0-9a-f]{32}$/.test(t)) {
    try {
      const base = process.env.API_INTERNAL_URL || "http://localhost:8080";
      const r = await fetch(`${base}/v1/share/${encodeURIComponent(t)}`, { cache: "no-store" });
      if (r.ok) {
        const j = await r.json();
        if (typeof j?.question === "string" && j.question) title = j.question.slice(0, 80);
        if (typeof j?.spread === "string" && j.spread) spread = j.spread.slice(0, 40);
      }
    } catch {
      /* fallback на дефолт */
    }
  }
  // Аудит C: рендер детерминирован от q/s — кэшируем сутки (CPU-DoS mitigation + nginx zone og).
  const res = new ImageResponse(
    (
      <div
        style={{
          width: "1200px",
          height: "630px",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: "#0B0B14",
          color: "#F5F0E6",
        }}
      >
        <div style={{ fontSize: 32, letterSpacing: 8, color: "#D4AF37" }}>ОНЛАЙН ТАРО</div>
        <div style={{ fontSize: 64, marginTop: 24, maxWidth: 900, textAlign: "center" }}>{title}</div>
        <div style={{ fontSize: 32, marginTop: 16, color: "#A8A8C0" }}>{spread}</div>
        <div style={{ fontSize: 24, marginTop: 32, color: "#D4AF37" }}>
          Задай вопрос. Вытяни карты. Услышь себя.
        </div>
      </div>
    ),
    { width: 1200, height: 630 }
  );
  res.headers.set("Cache-Control", "public, max-age=86400, immutable");
  return res;
}
