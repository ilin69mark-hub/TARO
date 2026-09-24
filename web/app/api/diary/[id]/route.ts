import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

import { fwdHeaders, bodyTooLarge, tooLarge } from "@/lib/proxy";

const headers = (req: NextRequest) => fwdHeaders(req);

async function fwd(res: Response) {
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}

// id дневника — UUID: allowlist + encode (аудит B: был traversal в Go).
const ID_RE = /^[0-9a-fA-F-]{8,64}$/;
function badId() {
  return NextResponse.json({ error: { message_ru: "Некорректный id" } }, { status: 422 });
}

// GET /api/diary/:id → Go.
export async function GET(req: NextRequest, { params }: { params: { id: string } }) {
  if (!ID_RE.test(params.id)) return badId();
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(params.id)}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return fwd(res);
}

// PUT /api/diary/:id → Go.
export async function PUT(req: NextRequest, { params }: { params: { id: string } }) {
  if (!ID_RE.test(params.id)) return badId();
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(params.id)}`, {
    method: "PUT",
    headers: headers(req),
    body,
  });
  return fwd(res);
}

// DELETE /api/diary/:id → Go.
export async function DELETE(req: NextRequest, { params }: { params: { id: string } }) {
  if (!ID_RE.test(params.id)) return badId();
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(params.id)}`, {
    method: "DELETE",
    headers: headers(req),
  });
  return fwd(res);
}
