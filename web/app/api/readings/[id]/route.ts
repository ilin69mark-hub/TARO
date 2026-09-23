import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/readings/:id → Go (cookie дальше).
export async function GET(req: NextRequest, { params }: { params: { id: string } }) {
  const res = await fetch(`${GO}/v1/readings/${params.id}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
