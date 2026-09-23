import { NextRequest, NextResponse } from "next/server";
import { GO } from "@/lib/server";

// S07: форвардим клиентские заголовки как есть. CSRF ставит клиент из cookie
// taro_csrf, Origin/Referer — браузер. Прокси НИЧЕГО не инжектит.
export function fwdHeaders(req: NextRequest): Record<string, string> {
  const h: Record<string, string> = {
    "Content-Type": "application/json",
    Cookie: req.headers.get("cookie") || "",
  };
  for (const k of ["X-CSRF", "Origin", "Referer", "Idempotency-Key"]) {
    const v = req.headers.get(k);
    if (v) h[k] = v;
  }
  return h;
}

export async function passThrough(res: Response) {
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type", "set-cookie", "cache-control"].includes(k.toLowerCase())) {
      out.headers.set(k, v);
    }
  });
  return out;
}
