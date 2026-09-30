import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { relayJSON } from "@/lib/proxy";

const TOKEN_RE = /^[0-9a-f]{32}$/;

// GET /api/share/:token → Go (публичное превью, токен-allowlist).
export async function GET(req: NextRequest, { params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  if (!TOKEN_RE.test(token)) {
    return NextResponse.json({ error: { message_ru: "Некорректная ссылка" } }, { status: 422 });
  }
  const res = await fetch(`${GO}/v1/share/${encodeURIComponent(token)}`, {
    cache: "no-store",
  });
  return relayJSON(res);
}
