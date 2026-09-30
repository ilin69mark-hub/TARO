import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, bodyTooLarge, tooLarge, relayJSON } from "@/lib/proxy";

// GET /api/push/prefs → Go (cookie дальше, см. V24).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/push/prefs`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return relayJSON(res);
}

// POST /api/push/prefs → Go.
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/push/prefs`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
  });
  return relayJSON(res);
}
