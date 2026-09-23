import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// POST /api/push/subscribe → Go (cookie дальше, см. U21).
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/push/subscribe`, {
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
