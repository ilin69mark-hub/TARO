import { NextResponse } from "next/server";
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
