import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

const TOKEN_RE = /^[0-9a-f]{32}$/;

// GET /api/share/:token → Go (публичное превью, токен-allowlist).
export async function GET(req: NextRequest, { params }: { params: { token: string } }) {
  if (!TOKEN_RE.test(params.token)) {
    return NextResponse.json({ error: { message_ru: "Некорректная ссылка" } }, { status: 422 });
  }
  const res = await fetch(`${GO}/v1/share/${encodeURIComponent(params.token)}`, {
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
