import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// POST /api/me/age → Go (подтверждение 18+, см. T15).
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/me/age`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF": "1",
      Cookie: req.headers.get("cookie") || "",
    },
    body,
  });
  const buf = await res.arrayBuffer();
  return new NextResponse(buf, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
