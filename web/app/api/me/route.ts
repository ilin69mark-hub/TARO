import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, bodyTooLarge, tooLarge } from "@/lib/proxy";

// DELETE /api/me → Go (удаление данных, см. T15). Тело {confirm} форвардим (сервер требует).
export async function DELETE(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/me`, {
    method: "DELETE",
    headers: fwdHeaders(req),
    body,
  });
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}
