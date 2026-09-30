import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, bodyTooLarge, tooLarge, relay } from "@/lib/proxy";

// POST /api/payments/stars/invoice → Go (cookie дальше, см. D6/T29).
export async function POST(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/payments/stars/invoice`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
  });
  return relay(res);
}
