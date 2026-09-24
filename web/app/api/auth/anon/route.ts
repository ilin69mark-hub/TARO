import { NextRequest } from "next/server";
import { GO } from "@/lib/server";
import { fwdHeaders, passThrough } from "@/lib/proxy";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const body = await req.text();
  const h = fwdHeaders(req);
  // Аудит B: первый элемент X-Forwarded-For подделывается клиентом (nginx дописывает
  // реальный IP в конец). Доверяем только X-Real-IP от nginx; иначе — unknown (лимит по IP слабее, но не обходится подменой).
  const real = req.headers.get("x-real-ip");
  h["X-Real-IP"] = real ? real.split(",")[0].trim() : "unknown";
  const res = await fetch(`${GO}/v1/auth/anon`, { method: "POST", headers: h, body });
  return passThrough(res);
}
