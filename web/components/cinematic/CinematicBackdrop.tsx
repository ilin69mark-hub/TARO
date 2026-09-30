"use client";

// CinematicBackdrop: R3F-сцена lazy (ssr:false) за Suspense; Lite — CSS-туман (см. T21, 08-ultra).
// Canvas aria-hidden: выбор карт дублируется DOM (см. 04-a11y.md).
// Даунгрейд: событие taro:lite (FPS-гард) или taro:calm (Спокойный режим) → Lite.
import dynamic from "next/dynamic";
import { Suspense, useEffect, useState } from "react";
import { shouldUseLite } from "@/lib/device";
import Letterbox from "./Letterbox";

const Scene = dynamic(() => import("./SceneInner"), {
  ssr: false,
  loading: () => <div aria-hidden className="absolute inset-0 bg-deep" />,
});

function LiteBackdrop({ muted }: { muted?: boolean }) {
  return (
    <div
      aria-hidden
      className="absolute inset-0 bg-deep"
      style={{
        background: muted
          ? "radial-gradient(70% 50% at 50% 30%, rgba(124,92,255,0.12), transparent 70%), #0B0B14"
          : "radial-gradient(70% 50% at 50% 30%, rgba(124,92,255,0.20), transparent 70%), radial-gradient(40% 30% at 70% 70%, rgba(212,175,55,0.08), transparent 70%), #0B0B14",
      }}
    />
  );
}

/**
 * muted — «приглушённый» режим. На раскладе он обязателен: настоящие карты
 * лежат в DOM поверх, и 3D-объекты не должны с ними спорить. На главной он
 * нужен по другой причине — гасит храм, чтобы заголовок читался.
 */
export default function CinematicBackdrop({
  muted = false,
  veil = 0.62,
}: {
  muted?: boolean;
  /** Сила вуали поверх сцены. На раскладе 0.62: там настоящие карты лежат
   *  в DOM поверх и контраст обязателен. На главной 0.3: сцена — фон под
   *  заголовком, и при 0.62 храм превращался в неразличимую тень. */
  veil?: number;
}) {
  const [lite, setLite] = useState(false);
  useEffect(() => {
    if (shouldUseLite()) setLite(true);
    const down = () => setLite(true);
    window.addEventListener("taro:lite", down);
    window.addEventListener("taro:calm", down);
    return () => {
      window.removeEventListener("taro:lite", down);
      window.removeEventListener("taro:calm", down);
    };
  }, []);
  if (lite || shouldUseLite()) return <LiteBackdrop muted={muted} />;
  return (
    <div aria-hidden className="absolute inset-0">
      <Letterbox />
      <Suspense fallback={<LiteBackdrop muted={muted} />}>
        <Scene dimmed={muted} />
      </Suspense>
      {/* Вуаль: приглушает сцену, но НЕ контент. Сцена живёт в absolute-слое
          под страницей, поэтому карты поверх остаются контрастными. Без неё
          3D-веер рубашек (FoilFan) и пыль перекрывали карты расклада. */}
      {muted && (
        <div className="absolute inset-0" style={{ background: `rgba(11,11,20,${veil})` }} />
      )}
    </div>
  );
}
