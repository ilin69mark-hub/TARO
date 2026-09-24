import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

import { fwdHeaders, bodyTooLarge, tooLarge } from "@/lib/proxy";

function hdrs(req: NextRequest) {
  return fwdHeaders(req);
}

// GET /api/push/prefs → Go (cookie дальше, см. V24).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/push/prefs`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}

// POST /api/push/prefs → Go.
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/push/prefs`, {
    method: "POST",
    headers: hdrs(req),
    body,
  });
  const buf = await res.arrayBuffer();
  return new NextResponse(buf, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
