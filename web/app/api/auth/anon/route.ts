import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

async function pass(res: Response) {
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie", "cache-control"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}

export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/auth/anon`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF": "1", "X-Real-IP": req.ip || "unknown" },
    body,
  });
  return pass(res);
}
