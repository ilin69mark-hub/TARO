"use client";

// Дип-линк приглашения: /r/<ref_code> (см. docs/project-book/02-functional/06).
//
// Раньше маршрут не существовал ни здесь, ни в Go: приглашающий обязан был
// продиктовать 8 символов, а приглашённый — вбить их руками в профиле. Ссылка
// из спеки (taro.me/r/<ref_code>) просто не работала.
//
// Здесь код применяется СРАЗУ, до первого расклада, потому что apply разрешён
// только до него: если дать приглашённому сначала сделать расклад, бонус
// сгорит на 409 ALREADY_REFERRED. Поэтому порядок жёсткий:
//   1) ensureAuth — иначе /api/referral/apply вернёт 401;
//   2) apply кода;
//   3) редирект на первый расклад, откуда бонус и начислится.
import { useEffect, useRef, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { ensureAuth } from "@/lib/auth";
import { track, events } from "@/lib/analytics";

export default function ReferralLinkPage() {
  const params = useParams<{ code: string }>();
  const router = useRouter();
  const [msg, setMsg] = useState("");
  const fired = useRef(false);

  useEffect(() => {
    // StrictMode в dev монтирует эффект дважды, а apply не идемпотентен по
    // смыслу (второй раз вернул бы 409). Один вызов на страницу.
    if (fired.current) return;
    fired.current = true;
    const code = (params?.code || "").trim().toUpperCase();
    if (!code || code.length > 16) {
      setMsg("Ссылка не похожа на приглашение. Попросите друга прислать её ещё раз.");
      return;
    }
    void ensureAuth()
      .then(() => api.post("/referral/apply", { code }))
      .then(() => {
        track(events.referralApplied, { via: "deeplink" });
        // Сразу на расклад: бонус начислится хуком после первого чтения.
        router.replace("/spreads/daily");
      })
      .catch((e: unknown) => {
        setMsg((e as Error).message);
      });
  }, [params?.code, router]);

  return (
    <main className="mx-auto max-w-md px-4 pt-16 text-center">
      <p className="text-sm uppercase tracking-wider text-mist">Приглашение</p>
      {msg ? (
        <>
          <p className="mt-3 text-paper">{msg}</p>
          <button
            onClick={() => router.replace("/")}
            className="mt-6 rounded-2xl border border-gold/40 px-4 py-2 text-sm font-semibold text-gold"
          >
            На главную
          </button>
        </>
      ) : (
        <p className="mt-3 text-paper">Применяем код…</p>
      )}
    </main>
  );
}
