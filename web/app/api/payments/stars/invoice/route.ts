import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

// POST /api/payments/stars/invoice → Go (cookie дальше, см. D6/T29).
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/payments/stars/invoice`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF": "1",
      Cookie: req.headers.get("cookie") || "",
    },
    body,
  });
  const buf = await res.arrayBuffer();
  return new NextResponse(buf, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
