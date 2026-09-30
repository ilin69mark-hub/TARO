import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { relayJSON } from "@/lib/proxy";

// GET /api/ab/me → Go (cookie дальше, см. U22).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/ab/me`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return relayJSON(res);
}
