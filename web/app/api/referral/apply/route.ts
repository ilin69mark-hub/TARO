import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders } from "@/lib/proxy";

// POST /api/referral/apply → Go.
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/referral/apply`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
  });
  const buf = await res.arrayBuffer();
  const out = new Response(buf, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}
