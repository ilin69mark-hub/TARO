import { GO } from "@/lib/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { relay } from "@/lib/proxy";

// GET /api/push/public → Go (VAPID-публичник, см. U21).
export async function GET() {
  const res = await fetch(`${GO}/v1/push/public`);
  return relay(res);
}
