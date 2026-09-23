import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

// GET /api/streak/me → Go (cookie дальше, см. U20).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/streak/me`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
