"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

// Бейдж стрика «дней подряд» (см. U20). Показываем при days>=2.
export default function StreakBadge() {
  const [days, setDays] = useState(0);
  useEffect(() => {
    api.get<{ days: number }>("/streak/me").then((r) => setDays(r.days)).catch(() => undefined);
  }, []);
  if (days < 2) return null;
  return (
    <p aria-label={`Стрик ${days} дней`} className="mt-2 inline-block rounded-full border border-gold/40 px-3 py-1 text-sm text-gold">
      🔥 {days} {plural(days)}
    </p>
  );
}

function plural(n: number): string {
  if (n % 10 === 1 && n % 100 !== 11) return "день подряд";
  if ([2, 3, 4].includes(n % 10) && ![12, 13, 14].includes(n % 100)) return "дня подряд";
  return "дней подряд";
}
