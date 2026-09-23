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

function LiteBackdrop() {
  return (
    <div
      aria-hidden
      className="absolute inset-0 bg-deep"
      style={{
        background:
          "radial-gradient(70% 50% at 50% 30%, rgba(124,92,255,0.20), transparent 70%), radial-gradient(40% 30% at 70% 70%, rgba(212,175,55,0.08), transparent 70%), #0B0B14",
      }}
    />
  );
}

export default function CinematicBackdrop() {
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
  if (lite || shouldUseLite()) return <LiteBackdrop />;
  return (
    <div aria-hidden className="absolute inset-0">
      <Letterbox />
      <Suspense fallback={<LiteBackdrop />}>
        <Scene />
      </Suspense>
    </div>
  );
}
