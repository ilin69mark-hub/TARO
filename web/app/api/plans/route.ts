import { GO } from "@/lib/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { relay } from "@/lib/proxy";

// GET /api/plans → Go (публичные цены, см. T18).
export async function GET() {
  const res = await fetch(`${GO}/v1/plans`, { next: { revalidate: 60 } });
  return relay(res);
}
