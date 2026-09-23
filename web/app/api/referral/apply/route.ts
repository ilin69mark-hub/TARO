import { NextRequest } from "next/server";
import { GO } from "@/lib/server";

// POST /api/referral/apply → Go.
export async function POST(req: NextRequest) {
  const body = await req.text();
  const res = await fetch(`${GO}/v1/referral/apply`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF": "1",
      Cookie: req.headers.get("cookie") || "",
    },
    body,
  });
  const buf = await res.arrayBuffer();
  const out = new Response(buf, { status: res.status });
  res.headers.forEach((v, k) => {
    if (["content-type"].includes(k.toLowerCase())) out.headers.set(k, v);
  });
  return out;
}
