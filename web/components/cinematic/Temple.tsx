"use client";

// Храм procedural (см. 05-design/09-premium-temple.md): пол, 6 колонн, купол.
// Desktop: MeshReflectorMaterial; mobile — fake gradient plane (reflector запрещен).
import { useMemo } from "react";
import { MeshReflectorMaterial } from "@react-three/drei";
import * as THREE from "three";

function FakeFloor() {
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -1.2, 0]}>
      <planeGeometry args={[14, 14]} />
      <meshBasicMaterial color="#0B0B14" transparent opacity={0.9} />
    </mesh>
  );
}

function Columns() {
  const geo = useMemo(() => new THREE.CylinderGeometry(0.18, 0.24, 5, 6), []);
  const mat = useMemo(
    () =>
      new THREE.MeshStandardMaterial({
        color: "#151524",
        roughness: 0.9,
        metalness: 0.05,
        emissive: "#7C5CFF",
        emissiveIntensity: 0.06,
      }),
    []
  );
  const spots = useMemo(() => {
    const arr: [number, number][] = [];
    for (let i = 0; i < 6; i++) {
      const a = (i / 6) * Math.PI * 2;
      arr.push([Math.cos(a) * 4.2, Math.sin(a) * 4.2]);
    }
    return arr;
  }, []);
  return (
    <group>
      {spots.map(([x, z], i) => (
        <mesh key={i} geometry={geo} material={mat} position={[x, 1.3, z]} />
      ))}
    </group>
  );
}

function DomeGlow() {
  const tex = useMemo(() => {
    const c = document.createElement("canvas");
    c.width = c.height = 128;
    const d = c.getContext("2d")!;
    const g = d.createRadialGradient(64, 64, 4, 64, 64, 64);
    g.addColorStop(0, "rgba(212,175,55,0.5)");
    g.addColorStop(1, "rgba(212,175,55,0)");
    d.fillStyle = g;
    d.fillRect(0, 0, 128, 128);
    const t = new THREE.CanvasTexture(c);
    return t;
  }, []);
  return (
    <sprite position={[0, 3.4, -3]} scale={[7, 7, 1]}>
      <spriteMaterial map={tex} transparent opacity={0.5} depthWrite={false} />
    </sprite>
  );
}

export default function Temple({ desktop }: { desktop: boolean }) {
  return (
    <group>
      {desktop ? (
        <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -1.2, 0]}>
          <planeGeometry args={[14, 14]} />
          {/* @ts-expect-error drei reflector material */}
          <MeshReflectorMaterial
            blur={[300, 300]}
            resolution={512}
            mixBlur={1}
            mixStrength={12}
            roughness={0.9}
            depthScale={0.5}
            minDepthThreshold={0.4}
            maxDepthThreshold={1.4}
            color="#0B0B14"
            metalness={0.4}
          />
        </mesh>
      ) : (
        <FakeFloor />
      )}
      <Columns />
      <DomeGlow />
    </group>
  );
}
