import { NextRequest } from "next/server";
import { GO } from "@/lib/server";
import { fwdHeaders, passThrough } from "@/lib/proxy";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/auth/telegram`, {
    method: "POST",
    headers: fwdHeaders(req),
    body,
  });
  return passThrough(res);
}
