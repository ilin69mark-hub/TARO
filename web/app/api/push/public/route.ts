import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

async function pass(res: Response) {
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}

// GET /api/push/public → Go (VAPID-публичник, см. U21).
export async function GET() {
  const res = await fetch(`${GO}/v1/push/public`);
  return pass(res);
}
