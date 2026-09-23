// Foil-веер: 3 рубашки с золотой кромкой + tilt за pointer (см. 09-premium-temple.md).
// Tilt и foil-перелив — только desktop pointer:fine; mobile — статика.
// Текстура рубашки — procedural canvas (без ассетов до T24).
import { useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { isFinePointer } from "@/lib/device";

function backTexture(): THREE.CanvasTexture {
  const c = document.createElement("canvas");
  c.width = 256;
  c.height = 410;
  const d = c.getContext("2d")!;
  d.fillStyle = "#151524";
  d.fillRect(0, 0, 256, 410);
  d.strokeStyle = "#D4AF37";
  d.lineWidth = 6;
  d.strokeRect(14, 14, 228, 382);
  d.strokeStyle = "rgba(212,175,55,0.4)";
  d.lineWidth = 2;
  for (let i = 0; i < 8; i++) {
    d.beginPath();
    d.arc(128, 205, 20 + i * 12, 0, Math.PI * 2);
    d.stroke();
  }
  d.fillStyle = "#F1D97B";
  d.beginPath();
  d.moveTo(128, 165);
  d.lineTo(142, 205);
  d.lineTo(128, 245);
  d.lineTo(114, 205);
  d.closePath();
  d.fill();
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

export default function FoilFan() {
  const group = useRef<THREE.Group>(null);
  const tex = useMemo(() => (typeof document !== "undefined" ? backTexture() : null), []);
  const fine = useMemo(() => isFinePointer(), []);
  const born = useRef(-1);

  // Fan: stagger .08 — карты разъезжаются веером из колоды (см. 05-animations.md)
  const targets = useMemo(
    () =>
      [-0.5, 0, 0.5].map((x, i) => ({
        pos: new THREE.Vector3(x, Math.abs(x) * 0.3, -i * 0.05),
        rot: -x * 0.5,
        delay: i * 0.08,
      })),
    []
  );

  useFrame((state) => {
    if (!group.current || !fine) return;
    const { x, y } = state.pointer;
    group.current.rotation.y = x * 0.18; // tilt ±10°
    group.current.rotation.x = -y * 0.1;
    if (born.current < 0) born.current = state.clock.elapsedTime;
    const t = state.clock.elapsedTime - born.current;
    group.current.children.forEach((child, i) => {
      const tg = targets[i];
      if (!tg) return;
      const k = Math.min(1, Math.max(0, (t - tg.delay) / 0.6));
      const e = 1 - Math.pow(1 - k, 3);
      child.position.lerpVectors(new THREE.Vector3(0, -0.4, 0.3), tg.pos, e);
      child.rotation.z = tg.rot * e;
    });
  });

  const mats = useMemo(() => {
    if (!tex) return [];
    return [0, 1, 2].map(() => {
      const m = new THREE.MeshPhysicalMaterial({
        map: tex,
        roughness: 0.35,
        metalness: 0.1,
      });
      // edge-gilding: emissive gold на grazing angle (см. 09-premium-temple.md)
      m.onBeforeCompile = (sh) => {
        sh.fragmentShader = sh.fragmentShader.replace(
          "#include <emissivemap_fragment>",
          `#include <emissivemap_fragment>
           vec3 vDir = normalize(vViewPosition);
           float fres = pow(1.0 - abs(dot(normalize(normal), vDir)), 2.0);
           totalEmissiveRadiance += vec3(0.83, 0.69, 0.22) * fres * 0.6;`
        );
      };
      return m;
    });
  }, [tex]);

  if (!tex || mats.length === 0) return null;
  return (
    <group ref={group} position={[0, -0.2, -1.5]}>
      {[-0.5, 0, 0.5].map((x, i) => (
        <mesh key={i} material={mats[i]} position={[0, -0.4, 0.3]}>
          <planeGeometry args={[0.62, 1.0]} />
        </mesh>
      ))}
    </group>
  );
}
