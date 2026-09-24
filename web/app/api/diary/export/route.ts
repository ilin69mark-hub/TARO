import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";

// POST /api/diary/export → Go (cookie + CSRF дальше, см. аудит B: GET-ссылка триггерилась cross-site).
export async function POST(req: NextRequest) {
  const res = await fetch(`${GO}/v1/diary/export`, {
    method: "POST",
    headers: {
      Cookie: req.headers.get("cookie") || "",
      "X-CSRF": req.headers.get("x-csrf") || "",
      "Content-Type": "application/json",
    },
    body: "{}",
    cache: "no-store",
  });
  const body = await res.arrayBuffer();
  const out = new NextResponse(body, { status: res.status });
  out.headers.set("Content-Type", "application/json");
  const disp = res.headers.get("content-disposition");
  if (disp) out.headers.set("Content-Disposition", disp);
  return out;
}
