import { NextRequest, NextResponse } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, bodyTooLarge, tooLarge, relayJSON } from "@/lib/proxy";

// GET /api/diary/:id → Go.
export async function GET(req: NextRequest, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  if (!/^[0-9a-fA-F-]{8,64}$/.test(id)) {
    return NextResponse.json({ error: { message_ru: "Некорректный id" } }, { status: 422 });
  }
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(id)}`, {
    headers: { Cookie: req.headers.get("cookie") || "" },
    cache: "no-store",
  });
  return relayJSON(res);
}

// DELETE /api/diary/:id → Go (удалить запись).
export async function DELETE(req: NextRequest, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  if (!/^[0-9a-fA-F-]{8,64}$/.test(id)) {
    return NextResponse.json({ error: { message_ru: "Некорректный id" } }, { status: 422 });
  }
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(id)}`, {
    method: "DELETE",
    headers: fwdHeaders(req),
    body,
  });
  return relayJSON(res);
}

// PUT /api/diary/:id → Go (обновление записи, см. diary.HandleUpdate). Роут
// отсутствовал: PUT был доступен напрямую через Go, минуя прокси, а значит
// минуя и Origin/X-CSRF-нормализацию fwdHeaders. Найдено при сплошном переводе
// роутов на relay (A20/F-13).
export async function PUT(req: NextRequest, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  if (!/^[0-9a-fA-F-]{8,64}$/.test(id)) {
    return NextResponse.json({ error: { message_ru: "Некорректный id" } }, { status: 422 });
  }
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/diary/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: fwdHeaders(req),
    body,
  });
  return relayJSON(res);
}
