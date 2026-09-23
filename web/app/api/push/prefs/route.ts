import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

function hdrs(req: NextRequest) {
  return {
    "Content-Type": "application/json",
    "X-CSRF": "1",
    Cookie: req.headers.get("cookie") || "",
  };
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
