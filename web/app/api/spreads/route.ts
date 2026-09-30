import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { relay } from "@/lib/proxy";

// GET /api/spreads → Go (кэш 5 мин на стороне Go, см. T05).
// relay, а не сборка ответа руками: иначе теряются Set-Cookie/Retry-After (A20/F-13).
export async function GET() {
  const res = await fetch(`${GO}/v1/spreads`, { next: { revalidate: 60 } });
  return relay(res);
}
