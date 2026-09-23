import { NextResponse } from "next/server";
import { GO } from "@/lib/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";

// GET /api/spreads → Go (кэш 5 мин на стороне Go, см. T05).
export async function GET() {
  const res = await fetch(`${GO}/v1/spreads`, { next: { revalidate: 60 } });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
