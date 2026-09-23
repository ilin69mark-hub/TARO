import { NextRequest } from "next/server";
import { GO } from "@/lib/server";
import { fwdHeaders, passThrough } from "@/lib/proxy";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const body = await req.text();
  const h = fwdHeaders(req);
  // S07/audit: X-Real-IP из x-forwarded-for доверенного фронт-прокси, не req.ip
  const xff = req.headers.get("x-forwarded-for");
  h["X-Real-IP"] = xff ? xff.split(",")[0].trim() : "unknown";
  const res = await fetch(`${GO}/v1/auth/anon`, { method: "POST", headers: h, body });
  return passThrough(res);
}
