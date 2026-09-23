import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

// DELETE /api/me → Go (удаление данных, см. T15).
export async function DELETE(req: NextRequest) {
  const res = await fetch(`${GO}/v1/me`, {
    method: "DELETE",
    headers: { "X-CSRF": "1", Cookie: req.headers.get("cookie") || "" },
  });
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}
