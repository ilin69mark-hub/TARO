import { NextRequest } from "next/server";

// Прокси к Go: только runtime, без prerender (Go недоступен при build, см. CI).
export const dynamic = "force-dynamic";
import { GO } from "@/lib/server";
import { fwdHeaders, relay, bodyTooLarge, tooLarge } from "@/lib/proxy";

// GET /api/me → Go (личность сессии: user_id, tg-привязка, has_payment).
// Отдельный метод, а не разделённый catch-all: DELETE без тела и GET без
// тела — разные контракты, и смешивать их в одном обработчике значит
// разрешить DELETE-у прийти с телом GET и наоборот.
//
// GET тоже нужен в прокси, а не только в Go: без него страница профиля
// получает 405 и не может показать человеку его идентификатор.
export async function GET(req: NextRequest) {
  const res = await fetch(`${GO}/v1/me`, {
    method: "GET",
    headers: fwdHeaders(req),
  });
  return relay(res);
}

// DELETE /api/me → Go (удаление данных, см. T15). Тело {confirm} форвардим (сервер требует).
// Go гасит сессию cookie'ами — relay доносит их (A20/F-13).
export async function DELETE(req: NextRequest) {
  if (bodyTooLarge(req)) return tooLarge();
  const body = await req.text();
  const res = await fetch(`${GO}/v1/me`, {
    method: "DELETE",
    headers: fwdHeaders(req),
    body,
  });
  return relay(res);
}
