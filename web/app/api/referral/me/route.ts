import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

async function pass(res: Response) {
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}

// GET /api/referral/me → Go (cookie дальше).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/referral/me`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return pass(res);
}
