"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

// Настройки пушей: час + тишина (см. V24). Cron уважает (см. HandleEvening).
export default function PushPrefs() {
  const [hour, setHour] = useState(21);
  const [quiet, setQuiet] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api.get<{ hour: number; quiet: boolean }>("/push/prefs").then((p) => {
      setHour(p.hour);
      setQuiet(p.quiet);
    }).catch(() => undefined);
  }, []);

  async function save() {
    await api.post("/push/prefs", { hour, quiet });
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
  }

  return (
    <div className="mt-2 flex items-center gap-2">
      <label className="text-sm text-mist">
        Час{" "}
        <select
          value={hour}
          onChange={(e) => setHour(Number(e.target.value))}
          className="rounded-xl border border-white/10 bg-deep p-2 text-paper"
        >
          {Array.from({ length: 24 }, (_, h) => (
            <option key={h} value={h}>
              {h}:00
            </option>
          ))}
        </select>
      </label>
      <button
        onClick={() => setQuiet(!quiet)}
        aria-pressed={quiet}
        className="rounded-xl border border-white/10 px-3 py-2 text-sm text-paper"
      >
        {quiet ? "Тихо 🤫" : "Со звуком"}
      </button>
      <button onClick={save} className="rounded-xl border border-gold/40 px-3 py-2 text-sm text-gold">
        {saved ? "ОК!" : "ОК"}
      </button>
    </div>
  );
}
