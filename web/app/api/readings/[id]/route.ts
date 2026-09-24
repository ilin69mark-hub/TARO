import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/readings/:id → Go (cookie дальше).
export async function GET(req: NextRequest, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  if (!/^[0-9a-fA-F-]{8,64}$/.test(id)) {
    return NextResponse.json({ error: { message_ru: "Некорректный id" } }, { status: 422 });
  }
  const res = await fetch(`${GO}/v1/readings/${encodeURIComponent(id)}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
