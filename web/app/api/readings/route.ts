import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, relayJSON, relayStream, bodyTooLarge, tooLarge } from "@/lib/proxy";

// GET /api/readings?limit&offset&q → Go (cookie дальше, q только premium — 403 иначе).
export async function GET(req: NextRequest) {
  const qs = req.nextUrl.searchParams.toString();
  const res = await fetch(`${GO}/v1/readings${qs ? `?${qs}` : ""}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return relayJSON(res);
}

// POST /api/readings → Go. SSE проксируется потоком (relayStream, без
// буферизации), JSON — как есть. Idempotency-Key уходит дальше (см. T12).
//
// Раньше здесь копировались только content-type и cache-control: при 429
// терялся Retry-After, а при ротации CSRF — Set-Cookie (A20/F-13).
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const headers: Record<string, string> = fwdHeaders(req);
  const accept = req.headers.get("accept") || "";
  const streaming = accept.includes("text/event-stream");
  if (accept) headers["Accept"] = accept;
  const res = await fetch(`${GO}/v1/readings`, { method: "POST", headers, body });
  if (streaming) {
    if (!res.body) return new NextResponse(null, { status: res.status });
    return relayStream(res);
  }
  return relayJSON(res);
}
