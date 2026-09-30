import { NextRequest } from "next/server";
import { GO } from "@/lib/server";
import { fwdHeaders, realIP, bodyTooLarge, tooLarge, relay } from "@/lib/proxy";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const h = fwdHeaders(req);
  // fwdHeaders уже ставит X-Real-IP из nginx; дублируем явно, потому что
  // анонимная регистрация — место, где IP решает (лимит по IP + изоляция
  // фермы), и полагаться на общий хелпер здесь не хочется.
  h["X-Real-IP"] = realIP(req);
  const res = await fetch(`${GO}/v1/auth/anon`, { method: "POST", headers: h, body });
  return relay(res);
}

