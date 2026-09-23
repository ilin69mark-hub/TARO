import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// GET /api/diary/export → Go (cookie дальше, см. V12).
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/diary/export`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  out.headers.set("Content-Type", "application/json");
  const disp = res.headers.get("content-disposition");
  if (disp) out.headers.set("Content-Disposition", disp);
  return out;
}
