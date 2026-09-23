import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

function fwd(res: Response) {
  return res.arrayBuffer().then(
    (body) =>
      new NextResponse(body, {
        status: res.status,
        headers: { "Content-Type": "application/json" },
      })
  );
}

import { fwdHeaders } from "@/lib/proxy";

const headers = (req: NextRequest) => fwdHeaders(req);

// GET /api/diary?limit&offset&mood → Go.
export async function GET(req: NextRequest) {
  const qs = req.nextUrl.searchParams.toString();
  const res = await fetch(`${GO}/v1/diary${qs ? `?${qs}` : ""}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return fwd(res);
}

// POST /api/diary → Go.
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/diary`, { method: "POST", headers: headers(req), body });
  return fwd(res);
}
