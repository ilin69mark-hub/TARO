"use client";

// R3F-сцена: туман + храм + foil-веер + свет + пыль + камера + пост (см. 08/09).
// Tier: desktop (fine pointer) — reflector, тени, пыль 600, пост;
// mobile — fake-plane, без теней, пыль 150, без поста.
// FPS-гард: <28fps 1.5с → level+ (пост off → пыль off); <22fps → Lite (событие taro:lite).
// reduced-motion/calm → R3F не монтируется вовсе (см. CinematicBackdrop + 04-a11y.md).
import { useMemo, useRef, useState } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import FogQuad from "./FogQuad";
import FoilFan from "./FoilFan";
import Temple, { ALTAR_Z, altarSurface } from "./Temple";
import Lights, { GodRays } from "./Lights";
import Dust from "./Dust";
import CameraRig from "./CameraRig";
import Candles from "./Candles";
import PostStack from "./PostStack";
import MagicBook from "./MagicBook";
import { isFinePointer } from "@/lib/device";

/** Параметры стенда (app/design-lab). Не переданы = дефолты компонентов. */
export type Lab = Partial<Record<string, number>>;

/**
 * Цвет тумана сцены обязан совпадать с тем, что рисует FogQuad у горизонта.
 * Раньше стоял #0B0B14 (0.043) при фоне шейдера ~0.10 — дальняя геометрия
 * гасла в более тёмный цвет, чем небо за ней, и апсида вырисовывалась
 * силуэтом с жёсткой верхней кромкой. Ровно та же ошибка, что была с краем пола.
 */
export const FOG_MATCH = "#12122b";

function FpsGuard({ onLevel }: { onLevel: (level: number) => void }) {
  const acc = useRef({ sum: 0, n: 0, low: 0, level: 0 });
  useFrame((_, delta) => {
    const a = acc.current;
    const fps = 1 / Math.max(delta, 1e-4);
    a.sum += fps;
    a.n += 1;
    if (a.n < 30) return;
    const avg = a.sum / a.n;
    a.sum = 0;
    a.n = 0;
    if (avg < 22) {
      window.dispatchEvent(new Event("taro:lite"));
      return;
    }
    if (avg < 28) {
      a.low += 1;
      // ~1.5с деградации; лестница: 1 пост off → 2 reflector+тени off → 3 пыль off (см. D7)
      if (a.low >= 3 && a.level < 3) {
        a.level += 1;
        a.low = 0;
        onLevel(a.level);
      }
    } else {
      a.low = 0;
    }
  });
  return null;
}

// dimmed — «приглушённый» режим для экрана расклада. Выключает FoilFan:
// три 3D-рубашки стоят ровно там, где лежат настоящие карты, и перекрывали их
// (см. жалобу владельца: «карты всегда должны быть видны»). Остальная сцена
// — свет, туман, пыль — остаётся: она даёт глубину и не спорит с картами.
/** Верхняя грань алтаря: на неё кладётся книга. См. altarSurface. */
function altarTop(lab?: Lab): { y: number; z: number } {
  return altarSurface(lab?.altarZ ?? ALTAR_Z);
}

export default function SceneInner({ dimmed = false, lab }: { dimmed?: boolean; lab?: Lab }) {
  const desktop = useMemo(() => isFinePointer(), []);
  const [level, setLevel] = useState(0); // 0 full → 1 пост off → 2 reflector+тени off → 3 пыль off
  const dustCount = lab?.dust ?? (desktop ? 600 : 150);
  return (
    <Canvas
      shadows={desktop && level < 2}
      gl={{ antialias: false, powerPreference: "low-power" }}
      camera={{ position: [0, 2.2, 4.5], fov: 45 }}
      dpr={[1, 1.5]}
      onCreated={({ gl }) => {
        gl.toneMapping = THREE.ACESFilmicToneMapping;
        gl.toneMappingExposure = 1.1;
      }}
    >
      {/* Атмосферная перспектива.
          Без неё дальний край пола виден как жёсткая горизонтальная полоса
          через весь кадр: FogQuad рисуется ФОНОМ (depthTest:false, первым), и
          поэтому не затягивает геометрию за собой — 14×14 пол обрывается
          чёткой линией там, где заканчивается. Три способа починить, все
          нужны вместе:
            1) scene fog — дальняя геометрия уходит в цвет неба;
            2) пол шире фрустума, чтобы его край был дальше границы тумана;
            3) цвет тумана совпадает с цветом шейдера у горизонта, иначе
               стык видно даже там, где он сглажен. */}
      <fogExp2
        attach="fog"
        args={[FOG_MATCH, lab?.fogD ? lab.fogD * 2.2 : 0.088]}
      />
      <CameraRig />
      <FpsGuard onLevel={setLevel} />
      <FogQuad lab={lab} />
      <Temple desktop={desktop && level < 2} lab={lab} />
      <Lights desktop={desktop && level < 2} lab={lab} />
      {(lab?.candles ?? 1) > 0.5 && <Candles lab={lab} />}
      {(lab?.beams ?? 1) > 0.5 && <GodRays />}
      {/* Веер — на раскладе, книга магии — на главной. Одновременно они не
          нужны: веер из трёх карт за центром кадра конкурирует с текстом. */}
      {dimmed && (lab?.book ?? 1) > 0.5 && (
        <MagicBook y={altarTop(lab).y} z={altarTop(lab).z} lab={lab} />
      )}
      {!dimmed && (lab?.fan ?? 1) > 0.5 && <FoilFan />}
      <Dust count={level >= 3 ? 0 : dustCount} lab={lab} />
      {/* Пост-стек на десктопе и только до первой ступени FPS-гарда.
          Он дороже всего остального: DOF + Bloom + LUT + SMAA съедают кадры
          быстрее, чем дают вид, и на слабой машине правильное решение —
          снять их, а не упрощать геометрию. */}
      {desktop && level < 1 && (lab?.post ?? 1) > 0.5 && <PostStack lab={lab} />}
    </Canvas>
  );
}
