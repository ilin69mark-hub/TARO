import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/entitlements/me → Go (cookie уходит дальше как есть).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/entitlements/me`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
