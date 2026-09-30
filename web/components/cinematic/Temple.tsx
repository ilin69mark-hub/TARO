"use client";

// Храм procedural (см. 05-design/09-premium-temple.md). Модели и текстуры не
// грузятся вообще: всё строится из примитивов и процедурного шума.
//
// Почему раньше читалось как «карты в тёмной комнате», а не как храм: колонна
// была одним CylinderGeometry(0.18, 0.24, 5, 6) — шестигранный столб без
// базы и капители, стоящий прямо в тумане. Не было ни архитектуры (арок, ступеней,
// алтаря), ни задней стены, из-за которой колонны висели в пустоте.
//
// Здесь: база → ствол с лёгким сужением и фаской → капитель, арки между
// соседними колоннами, ступени к алтарю, полукупол-апсида сзади. Плюс
// процедурный шум по шероховатости и цвету: без него материал — плоская заливка,
// на которой свет не играет.

import { useEffect, useMemo } from "react";
import { MeshReflectorMaterial } from "@react-three/drei";
import * as THREE from "three";
import type { Lab } from "./SceneInner";

const NIGHT = "#0B0B14";
const STONE = "#1a1930";
const STONE_LIGHT = "#2a2846";

/**
 * Глубина алтаря по умолчанию. Живёт здесь, а не в SceneInner/Candles, потому
 * что алтарь, ступени, свечи и ворон обязаны стоять на ОДНОЙ оси: при
 * altarZ 5.5 алтарь уезжал на 11 метров от камеры, и череп с вороном
 * превращались в пятно в 50 px — объект, которого на экране просто нет.
 * 3.4 ставит их в 9 метров, и силуэт читается.
 */
export const ALTAR_Z = 3.4;
export const ALTAR_STEPS = 4;

/** Разметка верхней плиты алтаря. Именованными константами, потому что
 *  верх плиты нужен трём разным местам сцены, и когда он был посчитан
 *  на глаз в каждом из них, книга оказалась внутри блока алтаря. */
const PLATE_Y = 0.78;
const PLATE_H = 0.16;

/**
 * Верхняя грань алтаря в мировых координатах — то, на что что-то кладётся.
 *
 * Раньше SceneInner и Candles независимо считали «верх алтаря» как верх
 * СТУПЕНЕЙ (-1.2 + 0.11 + steps*0.22 = -0.21), а плита лежит на 0.86 выше,
 * на ножке. Свечи это случайно компенсировали припиской +0.94, а книга — нет.
 * Теперь поверхность считается один раз, рядом с разметкой плиты.
 */
export function altarSurface(altarZ: number = ALTAR_Z, steps: number = ALTAR_STEPS) {
  return {
    y: -1.2 + 0.11 + steps * 0.22 + PLATE_Y + PLATE_H / 2,
    z: -altarZ - steps * 0.42,
  };
}

/* ─────────────────────────  процедурные текстуры  ───────────────────────── */

/**
 * Мрамор/гранит процедурно: value-noise в несколько октав, результат идёт и в
 * цвет, и в шероховатость. Без этого MeshStandardMaterial читается как
 * пластик — свет скользит по нему пятном, а грани не держат.
 */
function stoneTextures(): { map: THREE.CanvasTexture; rough: THREE.CanvasTexture } {
  const S = 256;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;
  const img = g.createImageData(S, S);

  const hash = (x: number, y: number) =>
    Math.abs(Math.sin(x * 127.1 + y * 311.7) * 43758.5453) % 1;
  const noise = (x: number, y: number) => {
    const xi = Math.floor(x), yi = Math.floor(y);
    const xf = x - xi, yf = y - yi;
    const u = xf * xf * (3 - 2 * xf);
    const v = yf * yf * (3 - 2 * yf);
    const a = hash(xi, yi), b = hash(xi + 1, yi);
    const cc = hash(xi, yi + 1), d = hash(xi + 1, yi + 1);
    return a * (1 - u) * (1 - v) + b * u * (1 - v) + cc * (1 - u) * v + d * u * v;
  };
  const fbm = (x: number, y: number) =>
    noise(x, y) * 0.5 + noise(x * 2, y * 2) * 0.3 + noise(x * 4, y * 4) * 0.2;

  const rc = document.createElement("canvas");
  rc.width = rc.height = S;
  const rg = rc.getContext("2d")!;
  const rimg = rg.createImageData(S, S);

  const base = new THREE.Color(STONE);
  const light = new THREE.Color(STONE_LIGHT);

  for (let y = 0; y < S; y++) {
    for (let x = 0; x < S; x++) {
      const i = (y * S + x) * 4;
      // Прожилки: синус, искажённый шумом — даёт «течение» камня
      const warp = fbm(x / 40, y / 40) * 6;
      const vein = Math.sin((x / 9 + y / 26) + warp) * 0.5 + 0.5;
      const grain = fbm(x / 12, y / 12);

      const t = Math.min(1, vein * 0.55 + grain * 0.45);
      const col = base.clone().lerp(light, t * 0.6);
      img.data[i] = col.r * 255;
      img.data[i + 1] = col.g * 255;
      img.data[i + 2] = col.b * 255;
      img.data[i + 3] = 255;

      // Шероховатость: прожилки полированнее зерна
      const r = 0.62 + (1 - t) * 0.3;
      rimg.data[i] = rimg.data[i + 1] = rimg.data[i + 2] = r * 255;
      rimg.data[i + 3] = 255;
    }
  }
  g.putImageData(img, 0, 0);
  rg.putImageData(rimg, 0, 0);

  const map = new THREE.CanvasTexture(c);
  map.colorSpace = THREE.SRGBColorSpace;
  map.wrapS = map.wrapT = THREE.RepeatWrapping;
  const rough = new THREE.CanvasTexture(rc);
  rough.wrapS = rough.wrapT = THREE.RepeatWrapping;
  return { map, rough };
}

/* ─────────────────────────────  материалы  ──────────────────────────────── */

function useStone(repeat: number) {
  return useMemo(() => {
    const { map, rough } = stoneTextures();
    map.repeat.set(repeat, repeat * 2);
    rough.repeat.set(repeat, repeat * 2);
    return new THREE.MeshStandardMaterial({
      map,
      roughnessMap: rough,
      roughness: 1,
      metalness: 0.04,
      emissive: new THREE.Color("#7C5CFF"),
      emissiveIntensity: 0.05,
    });
  }, [repeat]);
}

function useGold(repeat: number) {
  return useMemo(() => {
    const { map } = stoneTextures();
    const m = map.clone();
    m.repeat.set(repeat, repeat);
    m.needsUpdate = true;
    return new THREE.MeshStandardMaterial({
      color: "#D4AF37",
      roughness: 0.32,
      metalness: 0.85,
      emissive: new THREE.Color("#D4AF37"),
      emissiveIntensity: 0.12,
    });
  }, [repeat]);
}

/* ────────────────────────────────  пол  ────────────────────────────────── */

function Floor({ lab }: { lab?: Lab }) {
  const mat = useStone(14);
  useEffect(() => () => mat.dispose(), [mat]);
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -1.2, 0]} material={mat}>
      <planeGeometry args={[60, 60]} />
    </mesh>
  );
}

/** Отражение пола: отдельный слой, а не замена материала. */
function FloorMirror({ desktop, lab }: { desktop: boolean; lab?: Lab }) {
  if (!desktop || (lab?.reflect ?? 1) < 0.5) return null;
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -1.19, 0]}>
      <planeGeometry args={[60, 60]} />
      <MeshReflectorMaterial
        blur={[220, 220]}
        resolution={lab?.reflRes ?? 512}
        mixBlur={1}
        // mixStrength 12 давал зеркало-лужу. Золото на полу нам не нужно —
        // нам нужна глубина, а не блик.
        mixStrength={lab?.reflStr ?? 4.2}
        roughness={0.85}
        depthScale={0.6}
        minDepthThreshold={0.5}
        maxDepthThreshold={1.5}
        color={NIGHT}
        metalness={0.3}
      />
    </mesh>
  );
}

/* ──────────────────────────────  колонны  ───────────────────────────────── */

function Column({
  mat,
  capMat,
  height,
  radius,
}: {
  mat: THREE.Material;
  capMat: THREE.Material;
  height: number;
  radius: number;
}) {
  const H = height;
  const shaftH = H - 0.5; // база и капитель вычитаются из общей высоты
  return (
    <group>
      {/* База: два усечённых цилиндра — читается как цоколь */}
      <mesh material={mat} position={[0, 0.14, 0]}>
        <cylinderGeometry args={[radius * 1.9, radius * 2.2, 0.28, 24]} />
      </mesh>
      <mesh material={mat} position={[0, 0.4, 0]}>
        <cylinderGeometry args={[radius * 1.42, radius * 1.7, 0.26, 24]} />
      </mesh>
      {/* Ствол: лёгкое сужение книзу + фаска сверху. 24 сегмента вместо 6 —
          на гладком стволе силуэт читается, а не «палка». */}
      <mesh material={mat} position={[0, 0.53 + shaftH / 2, 0]}>
        <cylinderGeometry args={[radius, radius * 1.16, shaftH, 24, 1]} />
      </mesh>
      {/* Капитель: расширение + аба��ус */}
      <mesh material={capMat} position={[0, 0.53 + shaftH + 0.12, 0]}>
        <cylinderGeometry args={[radius * 1.5, radius * 1.18, 0.24, 24]} />
      </mesh>
      <mesh material={mat} position={[0, 0.53 + shaftH + 0.34, 0]}>
        <cylinderGeometry args={[radius * 1.62, radius * 1.5, 0.2, 24]} />
      </mesh>
    </group>
  );
}

/* ──────────────────────────  колоннада и арки  ─────────────────────────── */

function Colonnade({ lab }: { lab?: Lab }) {
  const H = lab?.colH ?? 9;
  const R = lab?.colR ?? 7.0;
  const N = 6;
  const mat = useStone(2);
  const capMat = useGold(1);
  const archMat = useStone(3);

  const cols = useMemo(
    () =>
      Array.from({ length: N }, (_, i) => {
        const a = (i / N) * Math.PI * 2;
        return [Math.cos(a) * R, Math.sin(a) * R, a] as const;
      }),
    [N, R]
  );

  // Арка между соседними колоннами: полукольцо, стоящее на капителях.
  // Радиус = половина расстояния между соседями (2R·sin(π/N)).
  const archR = (R * 2 * Math.sin(Math.PI / N)) / 2;
  const springY = 0.53 + (H - 0.5) + 0.44 - 1.2; // верх капителя в мировых Y

  return (
    <group>
      {cols.map(([x, z], i) => (
        <group key={i} position={[x, -1.2, z]}>
          <Column mat={mat} capMat={capMat} height={H} radius={0.34} />
        </group>
      ))}
      {cols.map(([x, z, a], i) => {
        const next = cols[(i + 1) % N];
        const mx = (x + next[0]) / 2;
        const mz = (z + next[1]) / 2;
        const aMid = Math.atan2(mz, mx);
        // Хорда арки должна быть касательной к окружности: поворачиваем
        // полукольцо на -(π/2 + aMid), иначе арка встаёт боком к колоннам.
        return (
          <mesh
            key={`arch-${i}`}
            material={archMat}
            position={[mx, springY, mz]}
            rotation={[0, -(Math.PI / 2 + aMid), 0]}
          >
            <torusGeometry args={[archR, 0.22, 10, 30, Math.PI]} />
          </mesh>
        );
      })}
    </group>
  );
}

/* ────────────────────  задняя стена: полукупол-апсида  ─────────────────── */

/**
 * Задняя стена-парапет.
 *
 * Два открытия, найденные скриншотом.
 *
 * 1) Прежняя «апсида» была ЦИЛИНДРОМ ВЫСОТОЙ 11.25 с односторонними
 *    гранями. Камера стоит внутри цилиндра, то есть видит ИЗНУТРЕННИЕ
 *    поверхности, а у цилиндра нормали наружу — и backface culling их
 *    отбрасывал. Стена не рисовалась вообще: сквозь неё было видно звёзды.
 *    Любая высота была бы бессмысленна, пока грани односторонние: стена
 *    либо не видна, либо закрывает небо целиком. Здесь DoubleSide, и она
 *    низкая — небо должно остаться небом, а не быть заколоченным.
 *
 * 2) Парапет низкий ещё и потому, что закрывает ДАЛЬНЮЮ КРОМКУ ПОЛА.
 *    Пол 60×60 обрывается на z = -30, и без стены его край читался
 *    жёсткой горизонтальной линией через весь кадр: выше — тёмное небо,
 *    ниже — чуть более светлый цвет тумана. Парапет на 3.4 единицы
 *    перекрывает этот край и уводит его в туман.
 *
 * Золотого кольца у основания здесь больше нет. Оно висело в воздухе на
 * высоте 0.5 над полом и в кадре читалось ровно тем, чем и было — случайной
 * полукруглой линией, которую владелец не смог опознать («линия полукруглая
 * это что?»). Декорация, которую нельзя объяснить, просто убирается.
 */
function Apse({ lab }: { lab?: Lab }) {
  const R = (lab?.colR ?? 7) * 1.75;
  const H = lab?.apseH ?? 3.4;
  const mat = useStone(6);
  // Стена должна быть ТЕМНЕЕ тумана, иначе читается как экран за сценой,
  // а не как камень в тени. Приглушаем карту материала.
  useEffect(() => {
    const m = mat as THREE.MeshStandardMaterial;
    m.color.setRGB(0.45, 0.45, 0.55);
    m.side = THREE.DoubleSide;
  }, [mat]);
  return (
    <group position={[0, -1.2, 0]}>
      <mesh material={mat} position={[0, H / 2, 0]}>
        <cylinderGeometry args={[R, R, H, 40, 1, true, Math.PI * 0.25, Math.PI * 1.5]} />
      </mesh>
      {/* Карниз: узкий венец по верхней кромке. Без него верх стены —
          просто обрез цилиндра, и она читается как отрезанный кусок. */}
      <mesh material={mat} position={[0, H + 0.09, 0]}>
        <cylinderGeometry args={[R + 0.22, R + 0.1, 0.18, 40, 1, true, Math.PI * 0.25, Math.PI * 1.5]} />
      </mesh>
    </group>
  );
}

/* ────────────────────────  ступени и алтарь  ───────────────────────────── */

function Altar({ lab }: { lab?: Lab }) {
  const mat = useStone(2);
  const gold = useGold(1);
  const steps = ALTAR_STEPS;
  const zTop = -(lab?.altarZ ?? ALTAR_Z);
  return (
    <group position={[0, -1.2, zTop]}>
      {/* Ступени: каждая выше и глубже предыдущей */}
      {Array.from({ length: steps }, (_, i) => (
        <mesh
          key={i}
          material={mat}
          position={[0, 0.11 + i * 0.22, -i * 0.42]}
        >
          <boxGeometry args={[3.8 - i * 0.5, 0.22, 0.9]} />
        </mesh>
      ))}
      {/* Алтарь: плита на ножке */}
      <group position={[0, 0.11 + steps * 0.22, -steps * 0.42]}>
        <mesh material={mat} position={[0, 0.35, 0]}>
          <boxGeometry args={[1.5, 0.7, 0.8]} />
        </mesh>
        <mesh material={gold} position={[0, PLATE_Y, 0]}>
          <boxGeometry args={[1.72, PLATE_H, 0.94]} />
        </mesh>
        {/* Выгравированный круг на плите: книга кладётся не в пустоту, а в
            подготовленное место. Раньше здесь было «углубление под рубашку» —
            рубашка на алтаре больше не рисуется. */}
        <mesh
          material={gold}
          position={[0, PLATE_Y + PLATE_H / 2 + 0.002, 0]}
          rotation={[-Math.PI / 2, 0, 0]}
        >
          <ringGeometry args={[0.3, 0.44, 32]} />
        </mesh>
      </group>
    </group>
  );
}

/* ────────────────────────────  купол-свечение  ──────────────────────────── */

function DomeGlow({ lab }: { lab?: Lab }) {
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
    t.colorSpace = THREE.SRGBColorSpace;
    return t;
  }, []);
  useEffect(() => () => tex.dispose(), [tex]);
  return (
    // Масштаб и прозрачность уменьшены: при 7×0.5 спрайт заливал треть кадра
    // кремовым пятном, и звёздное небо над ним не читалось вовсе.
    <sprite position={[0, 3.4, -3]} scale={[5, 5, 1]}>
      <spriteMaterial
        map={tex}
        transparent
        opacity={lab?.domeO ?? 0.28}
        depthWrite={false}
      />
    </sprite>
  );
}

/* ─────────────────────────────  FakeFloor  ──────────────────────────────── */

function FakeFloor() {
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -1.2, 0]}>
      <planeGeometry args={[60, 60]} />
      <meshBasicMaterial color={NIGHT} transparent opacity={0.9} />
    </mesh>
  );
}

/* ───────────────────────────────  сборка  ───────────────────────────────── */

export default function Temple({ desktop, lab }: { desktop: boolean; lab?: Lab }) {
  return (
    <group>
      {desktop ? <Floor lab={lab} /> : <FakeFloor />}
      <FloorMirror desktop={desktop} lab={lab} />
      {(lab?.apse ?? 1) > 0.5 && <Apse lab={lab} />}
      {(lab?.cols ?? 1) > 0.5 && <Colonnade lab={lab} />}
      {(lab?.altar ?? 1) > 0.5 && <Altar lab={lab} />}
      {(lab?.dome ?? 1) > 0.5 && <DomeGlow lab={lab} />}
    </group>
  );
}
