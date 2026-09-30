"use client";

import { useEffect, useMemo } from "react";
import * as THREE from "three";
import type { Lab } from "./SceneInner";

// Свет кино (см. 09-premium-temple.md): key gold spot + rim violet + candle static.
//
// Про мерцание. Свечи НЕ мерцают быстро: ярких вспышек больше 3/с быть не
// должно (WCAG 2.3.1) — это не вкусовое, а требование доступности. Здесь
// мерцание медленное и с малой амплитудой: «дыхание» на ~0.4Гц даёт ощущение
// живого огня и остаётся в допуске.
//
// Бюджет ограничен не «качеством», а доступностью. Всё остальное поднято:
// интенсивность и температура вынесены на стенд.
export default function Lights({ desktop, lab }: { desktop: boolean; lab?: Lab }) {
  const keyI = lab?.keyI ?? 60;
  // temp 0 = холодный фиолет, 1 = тёплое золото. Смешиваем цвет источника.
  const t = lab?.temp ?? 0.5;
  const keyColor = useMemoColor(
    new THREE.Color("#7C5CFF").lerp(new THREE.Color("#F1D97B"), t)
  );

  return (
    <group>
      <spotLight
        position={[3, 6, 2]}
        angle={0.5}
        penumbra={1}
        intensity={keyI}
        color={keyColor}
        castShadow={desktop}
        shadow-mapSize={[1024, 1024]}
      />
      {/* Rim сзади: контровой свет, отделяющий колонны от тумана. */}
      <directionalLight position={[-2, 3, -4]} intensity={2.2} color="#7C5CFF" />
      {/* Тёплый акцент на алтарь — ловит золото в тумане. */}
      <pointLight position={[0, 0.5, 0.5]} intensity={8} color="#D4AF37" distance={6} />
      {/* Свет НА алтарь. Прежний спот стоял на [3, 6, 2] и целился в начало
          координат: до алтаря (z = -7.18) от его оси 36.8° при конусе 28.6°,
          то есть алтарь был ВНЕ освещённого конуса, и единственным светом
          там были свечи. Ворон с черепом стояли чёрным силуэтом на чёрном
          парапете — то есть объектом, которого на экране нет. */}
      <pointLight position={[0, 2.15, -4.2]} intensity={9} color="#F1D97B" distance={7} decay={2} />
      {/* Холодный контровой сзади-сверху: перо ворона ловит кромку и
          отделяется от парапета даже там, где тёплый свет не достаёт. */}
      <pointLight position={[0.4, 2.1, -8.8]} intensity={7} color="#8A7BFF" distance={5} decay={2} />
      {/* Заполняющий снизу: без него низ колонн сливается в чёрное пятно. */}
      <pointLight position={[0, -1.0, 1.5]} intensity={3.5} color="#4a3d8f" distance={8} />
      <ambientLight intensity={0.25} color="#A8A8C0" />
    </group>
  );
}

/**
 * God-rays: additive-полосы за колодой (см. 09).
 *
 * Раньше это были плоскости с `meshBasicMaterial` одного цвета — на сцене они
 * читались как два белых треугольника из бумаги, а не как свет. Теперь это
 * мягкие полосы с процедурной текстурой: непрозрачность падает к краям и к
 * низу, поэтому луч растворяется, а не заканчивается.
 *
 * Это «дешёвые» лучи-подложка. Настоящие объёмные — в шейдере FogQuad
 * (марчинг от источника). Здесь нужен только разброс по краям кадра.
 */
function beamTexture(): THREE.CanvasTexture {
  const c = document.createElement("canvas");
  c.width = 32;
  c.height = 256;
  const d = c.getContext("2d")!;
  const g = d.createLinearGradient(0, 0, 0, 256);
  // Верх полосы — это КРОМКА кадра, а не источник. Если сделать её самой яркой,
  // плоскость обрывается жёсткой светлой линией через весь экран (видно было
  // на скриншоте). Поэтому луч появляется из-за края и гаснет книзу.
  g.addColorStop(0, "rgba(212,175,55,0)");
  g.addColorStop(0.22, "rgba(241,217,123,0.55)");
  g.addColorStop(0.6, "rgba(212,175,55,0.16)");
  g.addColorStop(1, "rgba(212,175,55,0)");
  d.fillStyle = g;
  d.fillRect(0, 0, 32, 256);
  // Горизонтальное затухание: края полосы прозрачнее середины
  const h = d.createLinearGradient(0, 0, 32, 0);
  h.addColorStop(0, "rgba(0,0,0,1)");
  h.addColorStop(0.5, "rgba(0,0,0,0)");
  h.addColorStop(1, "rgba(0,0,0,1)");
  d.globalCompositeOperation = "destination-out";
  d.fillStyle = h;
  d.fillRect(0, 0, 32, 256);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

export function GodRays() {
  const tex = useMemo(() => beamTexture(), []);
  useEffect(() => () => tex.dispose(), [tex]);
  return (
    <group position={[0, 2.9, -2.5]}>
      {[0, 1, 2].map((i) => (
        <mesh
          key={i}
          position={[(i - 1) * 1.05, 0, 0]}
          rotation={[0, 0, (i - 1) * 0.22]}
        >
          <planeGeometry args={[1.0, 7.0]} />
          <meshBasicMaterial
            map={tex}
            color="#F1D97B"
            transparent
            opacity={0.12}
            depthWrite={false}
            blending={THREE.AdditiveBlending}
          />
        </mesh>
      ))}
    </group>
  );
}

// THREE.Color мутируется на месте, а React переиспользует объекты — поэтому
// копия. Без неё «температура» со стенда меняла бы цвет предыдущего кадра.
function useMemoColor(c: THREE.Color): string {
  return `#${c.getHexString()}`;
}
