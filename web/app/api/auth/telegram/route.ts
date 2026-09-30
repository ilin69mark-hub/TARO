import { NextRequest } from "next/server";
import { GO } from "@/lib/server";
import { fwdHeaders, relay, bodyTooLarge, tooLarge } from "@/lib/proxy";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

// POST /api/auth/telegram → Go. Здесь ставятся сессионные cookie (taro_jwt,
// taro_fp, taro_csrf) — relay обязан донести их все (A20/F-13).
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/auth/telegram`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
  });
  return relay(res);
}
