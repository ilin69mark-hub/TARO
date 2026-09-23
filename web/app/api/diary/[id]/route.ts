import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

import { fwdHeaders } from "@/lib/proxy";

const headers = (req: NextRequest) => fwdHeaders(req);

async function fwd(res: Response) {
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}

// GET /api/diary/:id → Go.
export async function GET(req: NextRequest, { params }: { params: { id: string } }) {
  const res = await fetch(`${GO}/v1/diary/${params.id}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return fwd(res);
}

// PUT /api/diary/:id → Go.
export async function PUT(req: NextRequest, { params }: { params: { id: string } }) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/diary/${params.id}`, {
    method: "PUT",
    headers: headers(req),
    body,
  });
  return fwd(res);
}

// DELETE /api/diary/:id → Go.
export async function DELETE(req: NextRequest, { params }: { params: { id: string } }) {
  const res = await fetch(`${GO}/v1/diary/${params.id}`, {
    method: "DELETE",
    headers: headers(req),
  });
  return fwd(res);
}
