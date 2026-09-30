import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, relayJSON } from "@/lib/proxy";

// GET /api/referral/me → Go (cookie дальше).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/referral/me`, {
    // fwdHeaders, а не ручная сборка: здесь берётся IP юзера, с которого он
    // забрал/показал свой код (users.referral_ip) — он входит в антиферму
    // рефералки. Раньше заголовки собирались вручную (только cookie), поэтому
    // IP не доезжал и IP-сигнал не работал.
    headers: fwdHeaders(req),
    cache: "no-store",
  });
  return relayJSON(res);
}
