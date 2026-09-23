import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/readings?limit&offset&q → Go (cookie дальше, q только premium — 403 иначе).
export async function GET(req: NextRequest) {
  const qs = req.nextUrl.searchParams.toString();
  const res = await fetch(`${GO}/v1/readings${qs ? `?${qs}` : ""}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
// POST /api/readings → Go. SSE проксируется потоком (no-store),
// JSON — как есть. Idempotency-Key уходит дальше (см. T12).
export async function POST(req: NextRequest) {
  const body = await req.text();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-CSRF": "1",
    Cookie: req.headers.get("cookie") || "",
  };
  const idem = req.headers.get("idempotency-key");
  if (idem) headers["Idempotency-Key"] = idem;
  const accept = req.headers.get("accept") || "";
  if (accept.includes("text/event-stream")) headers["Accept"] = accept;
  const res = await fetch(`${GO}/v1/readings`, { method: "POST", headers, body });
  if (!res.body) return new NextResponse(null, { status: res.status });
  const out = new NextResponse(res.body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "cache-control"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  out.headers.set("Cache-Control", "no-store");
  return out;
}
