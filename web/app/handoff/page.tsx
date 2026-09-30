"use client";

// Точка входа переноса покупки — Mini App, открытая из t.me со start_param.
//
// Страница намеренно «глупая» и одноразовая: получить токен, отдать его серверу
// вместе с живым initData, показать результат. Никакого рендера профиля, никаких
// красивых состояний — если перенос не сработал, человек всё равно уходит в
// поддержку, и лишняя отрисовка тут только рисует.
//
// Отдельная страница, а не корень, по практической причине: у Mini App свой
// адрес, и если повесить перенос на корень, то start_param достанется и при
// обычном открытии из меню бота — где его нет. Отдельный маршрут означает, что
// перенос пришёл только туда, куда его целиком вели.

import { useCallback, useEffect, useRef, useState } from "react";
import { handoffToken, linkTelegram, telegramAppUrl } from "@/lib/me";

type Phase = "working" | "done" | "failed";

export default function HandoffPage() {
  const [phase, setPhase] = useState<Phase>("working");
  const [msg, setMsg] = useState("");
  // Повторно слать нельзя: токен одноразовый, и вторая попытка получила бы
  // HANDOFF_EXPIRED вместо результата. useRef держит флаг в рамках этой
  // загрузки страницы — этого ровно достаточно, потому что страница не
  // переживает перезапуск Mini App.
  const started = useRef(false);

  const run = useCallback(async () => {
    const token = handoffToken();
    if (!token) {
      setPhase("failed");
      setMsg("Ссылка переноса неполная — откройте приложение из того сообщения, где вы оплачивали.");
      return;
    }
    const res = await linkTelegram(token);
    if (res.ok) {
      setPhase("done");
      return;
    }
    // Две ошибки не требуют действий от человека, но требуют разных слов.
    if (res.code === "HANDOFF_IP_MISMATCH") {
      setPhase("failed");
      setMsg("Похоже, вы переключили сеть. Вернитесь в обычный браузер и попробуйте ещё раз.");
      return;
    }
    if (res.code === "HANDOFF_EXPIRED") {
      setPhase("failed");
      setMsg("Ссылка перестала действовать — она живёт 5 минут. Начните перенос заново из браузера.");
      return;
    }
    setPhase("failed");
    setMsg(res.message);
  }, []);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void run();
  }, [run]);

  const appUrl = telegramAppUrl();

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-5 py-10">
      <h1 className="text-xl font-semibold text-paper">Перенос покупки</h1>

      {phase === "working" && (
        <p className="mt-3 text-sm text-mist" role="status">
          Переносим покупку в Telegram…
        </p>
      )}

      {phase === "done" && (
        <>
          <p className="mt-3 text-sm text-mist" role="status">
            Готово. Покупка теперь в вашем Telegram-аккаунте и не пропадёт при смене телефона.
          </p>
          <a
            href={appUrl || "/"}
            className="mt-6 inline-flex h-12 w-full items-center justify-center rounded-2xl bg-gradient-to-br from-gold to-goldsoft text-sm font-semibold uppercase tracking-wider text-deep active:scale-95"
          >
            Продолжить
          </a>
        </>
      )}

      {phase === "failed" && (
        <>
          <p className="mt-3 text-sm text-goldsoft" role="alert">
            {msg}
          </p>
          <a
            href={appUrl || "/"}
            className="mt-6 inline-flex h-12 w-full items-center justify-center rounded-2xl border border-white/20 text-sm font-semibold text-paper active:scale-95"
          >
            Вернуться в приложение
          </a>
        </>
      )}

      {/*
        Предупреждение о пересылке — не декорация. Токен привязан к сети, но
        внутри одной /24 (или /64) он остаётся рабочим: сосед по Wi-Fi, другой
        телефон в том же доме, скриншот, пересланное сообщение. Отправив ссылку
        кому-то, человек отдаёт свою покупку — поэтому об отмене слияния мы
        говорим прямо, а не прячем.
      */}
      {phase !== "done" && (
        <p className="mt-10 text-xs text-mist">
          Не пересылайте эту ссылку никому: по ней покупку заберут вместо вас. Если перенос пошёл
          не туда — напишите в поддержку, мы вернём всё на место.
        </p>
      )}
    </main>
  );
}
