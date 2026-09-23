import { NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/plans → Go (публичные цены, см. T18).
export async function GET() {
  const res = await fetch(`${GO}/v1/plans`, { next: { revalidate: 60 } });
  const body = await res.arrayBuffer();
  return new NextResponse(body, {
    status: res.status,
    headers: { "Content-Type": "application/json" },
  });
}
