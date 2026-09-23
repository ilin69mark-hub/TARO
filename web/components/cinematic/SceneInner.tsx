"use client";

// R3F-сцена: туман + храм + foil-веер + свет + пыль + камера + пост (см. 08/09).
// Tier: desktop (fine pointer) — reflector, тени, пыль 600, пост;
// mobile — fake-plane, без теней, пыль 150, без поста.
// FPS-гард: <28fps 1.5с → level+ (пост off → пыль off); <22fps → Lite (событие taro:lite).
// reduced-motion/calm → R3F не монтируется вовсе (см. CinematicBackdrop + 04-a11y.md).
import { useMemo, useRef, useState } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { EffectComposer, Bloom, Noise, Vignette } from "@react-three/postprocessing";
import FogQuad from "./FogQuad";
import FoilFan from "./FoilFan";
import Temple from "./Temple";
import Lights, { GodRays } from "./Lights";
import Dust from "./Dust";
import CameraRig from "./CameraRig";
import { isFinePointer } from "@/lib/device";

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

export default function SceneInner() {
  const desktop = useMemo(() => isFinePointer(), []);
  const [level, setLevel] = useState(0); // 0 full → 1 пост off → 2 reflector+тени off → 3 пыль off
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
      <CameraRig />
      <FpsGuard onLevel={setLevel} />
      <FogQuad />
      <Temple desktop={desktop && level < 2} />
      <Lights desktop={desktop && level < 2} />
      <GodRays />
      <FoilFan />
      <Dust count={level >= 3 ? 0 : desktop ? 600 : 150} />
      {desktop && level < 1 && (
        <EffectComposer multisampling={0}>
          <Bloom intensity={0.6} luminanceThreshold={0.75} luminanceSmoothing={0.2} />
          <Noise opacity={0.08} />
          <Vignette darkness={0.35} />
        </EffectComposer>
      )}
    </Canvas>
  );
}
