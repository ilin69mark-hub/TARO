import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, bodyTooLarge, tooLarge } from "@/lib/proxy";

// POST /api/share {reading_id} → Go (создать токен; auth+CSRF дальше).
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/share`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
    cache: "no-store",
  });
  const out = await res.arrayBuffer();
  return new NextResponse(out, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
