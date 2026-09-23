import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/auth/telegram`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF": "1" },
    body,
  });
  const buf = await res.arrayBuffer();
  const out = new NextResponse(buf, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}
