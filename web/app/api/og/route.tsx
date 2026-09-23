import { ImageResponse } from "@vercel/og";
import { NextRequest } from "next/server";

// OG-картинка 1200×630 (см. U11). Только вопрос + название расклада из query,
// БЕЗ толкования (приватность: толкование видит только владелец, см. U12).
export const runtime = "nodejs";

export async function GET(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const title = (searchParams.get("q") || "Мой расклад").slice(0, 80);
  const spread = (searchParams.get("s") || "Таро").slice(0, 40);
  return new ImageResponse(
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
}
