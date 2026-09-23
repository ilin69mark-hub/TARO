"use client";

import * as THREE from "three";

// Свет кино (см. 09-premium-temple.md): key gold spot + rim violet + candle static.
// Flicker запрещен (WCAG 2.3.1) — static всегда; тени только desktop.
export default function Lights({ desktop }: { desktop: boolean }) {
  return (
    <group>
      <spotLight
        position={[3, 6, 2]}
        angle={0.5}
        penumbra={1}
        intensity={60}
        color="#F1D97B"
        castShadow={desktop}
        shadow-mapSize={[1024, 1024]}
      />
      <directionalLight position={[-2, 3, -4]} intensity={2.2} color="#7C5CFF" />
      <pointLight position={[0, 0.5, 0.5]} intensity={8} color="#D4AF37" distance={6} />
      <ambientLight intensity={0.25} color="#A8A8C0" />
    </group>
  );
}

// God-rays: 2 additive planes за колодой (см. 09).
export function GodRays() {
  return (
    <group position={[0, 1.2, -2.5]}>
      {[0, 1].map((i) => (
        <mesh key={i} position={[i === 0 ? -0.8 : 0.8, 0, 0]} rotation={[0, 0, i === 0 ? 0.35 : -0.35]}>
          <planeGeometry args={[1.2, 5]} />
          <meshBasicMaterial color="#F1D97B" transparent opacity={0.06} depthWrite={false} blending={THREE.AdditiveBlending} />
        </mesh>
      ))}
    </group>
  );
}
