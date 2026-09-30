// Foil-веер: рубашки с золотой кромкой + tilt за pointer (см. 09-premium-temple.md).
// Tilt и foil-перелив — только desktop pointer:fine; mobile — статика.
//
// Текстура рубашки — настоящий арт `public/cards/card-back.webp`, а не
// процедурная canvas. Раньше стоял комментарий «без ассетов до T24», но T24
// сдан (78/78 карт, контракт зафиксирован в cards/README.md), а компонент
// так и остался с нарисованным кружком на плоской заливке. Разница видна
// сразу: у настоящей рубашки фиолетовая ночь, золотое тиснение и звёздная
// розетка — ровно тот язык, который заявлен в 05-design/02-palette.
//
// Путь: image_key в БД — `cards/card-back.webp`, компонент собирает `/` +
// image_key, allowlist в CardArt.tsx этот ключ уже содержит.
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { isFinePointer } from "@/lib/device";

const BACK_URL = "/cards/card-back.webp";

/**
 * Настоящая рубашка.
 *
 * Грузим самим TextureLoader, а не useLoader из drei: в зафиксированной версии
 * (10.7.9) его нет, а suspense-обёртка ради одной текстуры здесь лишняя —
 * компонент и так умеет вернуть null, пока грузится.
 *
 * sRGB обязателен, иначе WebP темнеет примерно вдвое.
 */
function useBackTexture(): THREE.Texture | null {
  const [tex, setTex] = useState<THREE.Texture | null>(null);
  useEffect(() => {
    let live = true;
    const t = new THREE.TextureLoader().load(
      BACK_URL,
      (loaded) => {
        if (!live) return;
        loaded.colorSpace = THREE.SRGBColorSpace;
        loaded.anisotropy = 8;
        // Мип-мапы обязательны: рубашка 500×800, на сцене занимает ~60px
        // и удаляется вдаль — без мипмапов мылит и «искрится».
        loaded.generateMipmaps = true;
        loaded.minFilter = THREE.LinearMipmapLinearFilter;
        loaded.magFilter = THREE.LinearFilter;
        loaded.needsUpdate = true;
        setTex(loaded);
      },
      undefined,
      () => {
        // Файл не загрузился — сцена живёт без рубашек, а не падает.
      }
    );
    return () => {
      live = false;
      t.dispose();
    };
  }, []);
  return tex;
}

export default function FoilFan() {
  const group = useRef<THREE.Group>(null);
  const tex = useBackTexture();
  const fine = useMemo(() => isFinePointer(), []);
  const born = useRef(-1);

  // Fan: stagger .08 — карты разъезжаются веером из колоды (см. 05-animations.md)
  const targets = useMemo(
    () =>
      [-0.85, 0, 0.85].map((x, i) => ({
        pos: new THREE.Vector3(x, Math.abs(x) * 0.45, -i * 0.06),
        rot: -x * 0.42,
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

  const mats = useMemo(
    () =>
      [0, 1, 2].map(() => {
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
      }),
    [tex]
  );

  if (!tex || mats.length === 0) return null;
  return (
    <group ref={group} position={[0, -0.1, -2.6]}>
      {[-0.85, 0, 0.85].map((x, i) => (
        <mesh key={i} material={mats[i]} position={[0, -0.4, 0.3]}>
          <planeGeometry args={[0.78, 1.25]} />
        </mesh>
      ))}
    </group>
  );
}
