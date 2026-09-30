"use client";

// Книга магии на алтаре.
//
// Хронометраж — одна шкала, из которой выводится всё остальное. Никаких
// «появилось — исчезло»: книга парит, крутится, снижается, раскрывается,
// перелистывает страницы и замирает раскрытой. Фазы наложены ДРУГ НА ДРУГА,
// а не выстроены очередью, потому что по замыслу книга раскрывается УЖЕ НА
// ПОДХОДЕ, а не после посадки.
//
//   0.0  – 1.0 с    появление
//   0.0  – 3.2 с    парит над алтарём и медленно крутится вокруг вертикали
//   3.2  – 7.6 с    плавно снижается на алтарь, вращение гаснет
//   6.8  – 10.0 с   раскрывается (стартует ДО посадки — «при приближении»)
//   10.2 – 14.4 с   перелистываются четыре страницы
//   14.4 с +        покой: раскрыта, дышит светом
//
// Раскрытие — это НЕ «поворот обеих крышек на 35°». Это разворот
// настоящей книги: правая половина (передняя крышка и вся стопка правых
// страниц) поворачивается вокруг корешка на 180° и ложится слева. Только
// так закрытая книга действительно закрыта, а раскрытая показывает разворот
// с двумя страницами. «V» из двух досок читалась бы как лодка.
//
// Про доступность. Эта анимация не крутится в кадре у того, кто её отключил:
// сцена при reduced-motion / «спокойном режиме» не монтируется вовсе (см.
// CinematicBackdrop), остаётся статичная графика. Мерцаний «ярче трёх в
// секунду» (WCAG 2.3.1) здесь нет: свечение дышит на 0.08 Гц, одна
// медленная волна на 12 секунд.

import { useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { Lab } from "./SceneInner";

/* ─────────────────────────────  хронометраж  ───────────────────────────── */

const T = {
  appear: 1.0, //  появление
  hover: 3.2, //  конец парения
  land: 7.6, //  посадка
  open0: 6.8, //  начало раскрытия
  open1: 10.0, // книга раскрыта
  flip0: 10.2, // начало перелистывания
  rest: 14.4, // покой
};

const PAGES = 4;
const FLIP_STEP = 0.78; // сдвиг между стартами страниц
const FLIP_DUR = 1.05;

const HOVER_Y = 0.78; // высота парения над алтарём
const SPIN = 0.44; // рад/с вокруг вертикали
const TILT_HOVER = -0.05; // книга почти горизонтальна в полёте
const TILT_REST = 0.46; // в покое страницы развёрнуты к зрителю (пюпитр)
const OPEN_REST = -0.09; // правая половина чуть приподнята — разворот «живой»

/* ───────────────────────────────  геометрия  ────────────────────────────── */

const W = 0.46; // половина ширины
const D = 0.68; // глубина: корешок → обрез
const COVER_T = 0.05;
const BLOCK_T = 0.055;
const TOP_Y = COVER_T + BLOCK_T; // верх стопки
/** Стопка чуть ниже верхней плоскости. Раньше верх блока и плоскость бумаги
 *  совпадали по высоте, и z-битва рисовала на развороте золотые пятна. */
const BLOCK_TOP = TOP_Y - 0.008;
const RIGHT_PIVOT = TOP_Y; // ось правой половины
/** Насколько надо поднять книгу, чтобы при наклоне к зрителю передний край
 *  стоял на алтаре, а не уходил в него. */
const REST_LIFT = Math.sin(TILT_REST) * (D / 2);

const clamp01 = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);
/** Плавная кривая: нулевая производная на концах, поэтому нет рывков. */
const smooth = (v: number) => {
  const t = clamp01(v);
  return t * t * (3 - 2 * t);
};
const span = (t: number, a: number, b: number) => smooth((t - a) / (b - a));

/* ───────────────────────────  процедурные текстуры  ─────────────────────── */

/** Пергамент: тёплая бумага, волокна, руны, затемнение по краям. */
function pageTexture(): THREE.CanvasTexture {
  const S = 512;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;

  const bg = g.createLinearGradient(0, 0, S * 0.7, S);
  bg.addColorStop(0, "#cdbc93");
  bg.addColorStop(0.55, "#c3b087");
  bg.addColorStop(1, "#b09b74");
  g.fillStyle = bg;
  g.fillRect(0, 0, S, S);

  // Волокна: без них бумага выглядит как заливка
  for (let i = 0; i < 1400; i += 1) {
    const x = Math.random() * S;
    const y = Math.random() * S;
    const l = 4 + Math.random() * 22;
    g.strokeStyle = `rgba(${60 + ((Math.random() * 40) | 0)},${46 + ((Math.random() * 30) | 0)},26,0.10)`;
    g.lineWidth = 1;
    g.beginPath();
    g.moveTo(x, y);
    g.lineTo(x + l * 0.4, y + l);
    g.stroke();
  }

  // Строки «рукописи»: глифы из штрихов, а не настоящий текст
  g.strokeStyle = "rgba(46,30,14,0.75)";
  g.lineCap = "round";
  g.lineWidth = 2.2;
  for (let row = 0; row < 13; row += 1) {
    const y = 62 + row * 32;
    let x = 54;
    while (x < S - 54) {
      const glyph = 3 + ((Math.random() * 4) | 0);
      g.beginPath();
      for (let k = 0; k < glyph; k += 1) {
        const gx = x + k * 9;
        g.moveTo(gx, y - 7);
        if (Math.random() > 0.5) g.lineTo(gx + 5, y + 7);
        else g.lineTo(gx, y + 2);
        if (Math.random() > 0.6) {
          g.moveTo(gx, y);
          g.lineTo(gx + 7, y - 5);
        }
      }
      g.stroke();
      x += glyph * 9 + 10 + Math.random() * 12;
    }
  }

  // Сигил на странице: два кольца и луч звезды
  g.save();
  g.translate(S / 2, S * 0.46);
  g.strokeStyle = "rgba(92,58,18,0.9)";
  g.lineWidth = 3;
  g.beginPath();
  g.arc(0, 0, 96, 0, Math.PI * 2);
  g.stroke();
  g.lineWidth = 1.6;
  g.beginPath();
  g.arc(0, 0, 84, 0, Math.PI * 2);
  g.stroke();
  g.beginPath();
  for (let i = 0; i < 7; i += 1) {
    const a1 = (i / 7) * Math.PI * 2;
    const a2 = (((i + 3) % 7) / 7) * Math.PI * 2;
    g.moveTo(Math.cos(a1) * 84, Math.sin(a1) * 84);
    g.lineTo(Math.cos(a2) * 84, Math.sin(a2) * 84);
  }
  g.stroke();
  g.restore();

  // Затемнение по краям: лист лежит в тени у корешка и обреза
  const vg = g.createRadialGradient(S / 2, S / 2, S * 0.28, S / 2, S / 2, S * 0.72);
  vg.addColorStop(0, "rgba(0,0,0,0)");
  vg.addColorStop(1, "rgba(38,26,12,0.55)");
  g.fillStyle = vg;
  g.fillRect(0, 0, S, S);

  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  return t;
}

/** Переплёт: тёмная кожа, золотая двойная рамка, завитки, печать. */
function coverTexture(): THREE.CanvasTexture {
  const S = 512;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;

  const bg = g.createRadialGradient(S * 0.42, S * 0.38, 20, S / 2, S / 2, S * 0.78);
  bg.addColorStop(0, "#3a2130");
  bg.addColorStop(0.6, "#26141f");
  bg.addColorStop(1, "#160a12");
  g.fillStyle = bg;
  g.fillRect(0, 0, S, S);

  for (let i = 0; i < 5200; i += 1) {
    g.fillStyle = `rgba(255,220,190,${Math.random() * 0.05})`;
    g.fillRect(Math.random() * S, Math.random() * S, 1.6, 1.6);
  }

  const gold = g.createLinearGradient(0, 0, S, S);
  gold.addColorStop(0, "#f0d795");
  gold.addColorStop(0.4, "#d4af37");
  gold.addColorStop(0.7, "#8c6a1e");
  gold.addColorStop(1, "#e7c86a");
  g.strokeStyle = gold;

  g.lineWidth = 7;
  g.strokeRect(30, 30, S - 60, S - 60);
  g.lineWidth = 2.5;
  g.strokeRect(46, 46, S - 92, S - 92);

  g.lineWidth = 3.5;
  for (const [cx, cy, sx, sy] of [
    [62, 62, 1, 1],
    [S - 62, 62, -1, 1],
    [62, S - 62, 1, -1],
    [S - 62, S - 62, -1, -1],
  ] as const) {
    g.beginPath();
    g.moveTo(cx + 34 * sx, cy);
    g.quadraticCurveTo(cx, cy, cx, cy + 34 * sy);
    g.stroke();
    g.beginPath();
    g.arc(cx + 13 * sx, cy + 13 * sy, 6, 0, Math.PI * 2);
    g.stroke();
  }

  // Печать: змея, кусающая свой хвост, и звезда внутри
  g.lineWidth = 5;
  g.beginPath();
  g.arc(S / 2, S / 2, 96, 0.3, Math.PI * 1.75);
  g.stroke();
  g.lineWidth = 3;
  g.beginPath();
  g.arc(S / 2, S / 2, 82, 0, Math.PI * 2);
  g.stroke();
  g.lineWidth = 4;
  g.beginPath();
  for (let i = 0; i < 8; i += 1) {
    const a1 = (i / 8) * Math.PI * 2 - Math.PI / 2;
    const a2 = (((i + 3) % 8) / 8) * Math.PI * 2 - Math.PI / 2;
    g.moveTo(S / 2 + Math.cos(a1) * 80, S / 2 + Math.sin(a1) * 80);
    g.lineTo(S / 2 + Math.cos(a2) * 80, S / 2 + Math.sin(a2) * 80);
  }
  g.stroke();
  g.beginPath();
  g.arc(S / 2, S / 2, 16, 0, Math.PI * 2);
  g.stroke();

  // Потёртости: золото в живом изделии не бывает ровным
  for (let i = 0; i < 900; i += 1) {
    g.fillStyle = `rgba(20,10,16,${Math.random() * 0.3})`;
    g.fillRect(Math.random() * S, Math.random() * S, 2.4, 2.4);
  }

  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  return t;
}

/** Мягкое пятно для аддитивного свечения между страницами. */
function glowTexture(): THREE.CanvasTexture {
  const S = 256;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;
  const rg = g.createRadialGradient(S / 2, S / 2, 2, S / 2, S / 2, S / 2);
  rg.addColorStop(0, "rgba(255,228,170,0.7)");
  rg.addColorStop(0.35, "rgba(210,164,255,0.6)");
  rg.addColorStop(1, "rgba(120,80,255,0)");
  g.fillStyle = rg;
  g.fillRect(0, 0, S, S);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

/* ────────────────────────────────  книга  ───────────────────────────────── */

export default function MagicBook({
  y = 0,
  z = 0,
  size = 1.45,
  lab,
}: {
  /** Мировая высота верхней грани алтаря. */
  y?: number;
  z?: number;
  /**
   * Размер относительно расчётных размеров. Вынесен отдельной группой
   * ВНУТРИ корня, а не масштабом корня: масштаб корня умножил бы на
   * REST_LIFT и высоту парения, и книга после смены размера встала бы
   * в воздух или утонула в плите.
   */
  size?: number;
  lab?: Lab;
}) {
  const root = useRef<THREE.Group>(null);
  const right = useRef<THREE.Group>(null);
  const flips = useRef<(THREE.Group | null)[]>([]);
  const bends = useRef<(THREE.Group | null)[]>([]);
  const glow = useRef<THREE.Mesh>(null);
  const light = useRef<THREE.PointLight>(null);

  const page = useMemo(() => pageTexture(), []);
  const cover = useMemo(() => coverTexture(), []);
  const glowTex = useMemo(() => glowTexture(), []);

  const mats = useMemo(
    () => ({
      leather: new THREE.MeshStandardMaterial({ color: "#2b1a24", roughness: 0.62, metalness: 0.12 }),
      // Обрез листов золотой: у дорогой книги обрез красят
      edges: new THREE.MeshStandardMaterial({ color: "#b08c46", roughness: 0.62, metalness: 0.3 }),
      paper: new THREE.MeshStandardMaterial({ map: page, roughness: 0.86, metalness: 0 }),
      coverFace: new THREE.MeshStandardMaterial({ map: cover, roughness: 0.44, metalness: 0.3 }),
      glow: new THREE.MeshBasicMaterial({
        map: glowTex,
        transparent: true,
        opacity: 0,
        depthWrite: false,
        blending: THREE.AdditiveBlending,
      }),
    }),
    [page, cover, glowTex]
  );

  // Стенд смотрит кадр в произвольный момент хронометража, поэтому там
  // время задаётся слайдером, а не часами сцены.
  const frozen = lab ? (lab.bookT ?? T.rest) : null;

  useFrame((state) => {
    const t = frozen ?? state.clock.elapsedTime;

    const appear = span(t, 0, T.appear);
    const land = span(t, T.hover, T.land);
    const open = span(t, T.open0, T.open1);
    const alive = t > T.rest ? Math.min(1, (t - T.rest) / 1.6) : 0;

    if (root.current) {
      // Высота: парение → посадка. Пока книга летит, она ещё и покачивается;
      // при посадке и колебание, и остаточная высота гаснут вместе.
      const hover = HOVER_Y * (1 - land);
      const bob = (Math.sin(t * 1.15) * 0.055 + Math.sin(t * 0.63 + 1.3) * 0.03) * (1 - land) * appear;
      const rise = (1 - appear) * 0.45; // появление снизу из алтаря
      root.current.position.y = y + hover + bob + rise + REST_LIFT * land;
      root.current.scale.setScalar(appear);
      root.current.visible = appear > 0.001;
      // Спин гаснет к посадке и не делает лишний оборот: угол отсчитывается
      // от нуля, а не накапливается
      root.current.rotation.y = t < T.hover ? t * SPIN : T.hover * SPIN * (1 - land);
      root.current.rotation.x =
        TILT_HOVER * (1 - land) + TILT_REST * land + Math.sin(t * 0.7) * 0.05 * (1 - land) * appear;
    }

    if (right.current) {
      // Раскрытие = разворот правой половины вокруг корешка
      right.current.rotation.z = Math.PI * (1 - open) + OPEN_REST * open;
    }

    for (let i = 0; i < PAGES; i += 1) {
      const start = T.flip0 + i * FLIP_STEP;
      const f = flips.current[i];
      const p = clamp01((t - start) / FLIP_DUR);
      if (f) {
        // До своего черёда лист не существует: иначе он лежал бы ВНУТРИ
        // закрытой книги и торчал сквозь крышку
        f.visible = t >= start;
        if (f.visible) {
          f.rotation.z = Math.PI * p;
          // Лист отрывается от блока и ложится потом, поэтому в середине
          // хода он приподнят
          f.position.y = 0.118 + i * 0.005 + Math.sin(p * Math.PI) * 0.05;
        }
      }
      // Изгиб: два шарнира с накоплением дают дугу, а не поворот плоского
      // листа, который читается как картон
      const bend = Math.sin(p * Math.PI) * 0.62;
      const b1 = bends.current[i * 2];
      const b2 = bends.current[i * 2 + 1];
      if (b1) b1.rotation.z = -bend;
      if (b2) b2.rotation.z = -bend;
    }

    // Свет держим низким. Источник стоит в сантиметрах от страниц, и при
    // затухании 1/d² обычная «лампа накаливания» выбивала пергамент в белое
    // пятно вместе с рунами. Свечение должно быть признаком магии, а не
    // способом засветить текстуру.
    const lit = open * (0.82 + 0.18 * Math.sin(t * 0.5));
    if (glow.current) {
      (glow.current.material as THREE.MeshBasicMaterial).opacity = lit * 0.5;
    }
    if (light.current) light.current.intensity = 0.35 + lit * 1.5 * (0.9 + alive * 0.1);
  });

  const seg = W / 3;

  return (
    <group ref={root} position={[0, y, z]}>
      <group scale={size}>
      {/* Левая половина: задняя крышка и стопка левых страниц. Недвижна —
          при раскрытии книги двигается только правая. */}
      <mesh material={mats.leather} position={[-W / 2, COVER_T / 2, 0]} castShadow receiveShadow>
        <boxGeometry args={[W, COVER_T, D]} />
      </mesh>
      <mesh
        material={mats.edges}
        position={[-W * 0.47, BLOCK_TOP - BLOCK_T / 2, 0]}
        castShadow
        receiveShadow
      >
        <boxGeometry args={[W * 0.94, BLOCK_T, D * 0.93]} />
      </mesh>
      <mesh material={mats.paper} position={[-W * 0.47, TOP_Y, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <planeGeometry args={[W * 0.94, D * 0.93]} />
      </mesh>

      {/* Правая половина. Ось на высоте стопки: поворот на 180° кладёт её
          ровно поверх левой, поэтому закрытая книга действительно закрыта. */}
      <group ref={right} position={[0, RIGHT_PIVOT, 0]}>
        <mesh material={mats.leather} position={[W / 2, -0.08, 0]} castShadow receiveShadow>
          <boxGeometry args={[W, COVER_T, D]} />
        </mesh>
        <mesh material={mats.edges} position={[W * 0.47, BLOCK_TOP - BLOCK_T - RIGHT_PIVOT, 0]} castShadow receiveShadow>
          <boxGeometry args={[W * 0.94, BLOCK_T, D * 0.93]} />
        </mesh>
        <mesh material={mats.paper} position={[W * 0.47, 0, 0]} rotation={[-Math.PI / 2, 0, 0]}>
          <planeGeometry args={[W * 0.94, D * 0.93]} />
        </mesh>
        {/* Крышка снаружи: накладка с золотой печатью */}
        <mesh
          material={mats.coverFace}
          position={[W / 2, -0.08 - COVER_T / 2 - 0.002, 0]}
          rotation={[Math.PI / 2, 0, Math.PI]}
        >
          <planeGeometry args={[W * 0.98, D * 0.98]} />
        </mesh>
      </group>

      {/* Корешок: заполняет середину, иначе между половинами дыра */}
      <mesh material={mats.leather} position={[0, TOP_Y / 2, 0]} castShadow receiveShadow>
        <boxGeometry args={[0.07, TOP_Y, D]} />
      </mesh>

      {/* Перелистываемые листы: три сегмента, два шарнира */}
      {Array.from({ length: PAGES }, (_, i) => (
        <group
          key={i}
          ref={(el) => {
            flips.current[i] = el;
          }}
          position={[0, 0.118 + i * 0.005, 0]}
          visible={false}
        >
          <group
            ref={(el) => {
              bends.current[i * 2] = el;
            }}
            position={[seg, 0, 0]}
          >
            <mesh material={mats.paper} position={[seg / 2, 0, 0]} rotation={[-Math.PI / 2, 0, 0]}>
              <planeGeometry args={[seg, D * 0.93]} />
            </mesh>
            <group
              ref={(el) => {
                bends.current[i * 2 + 1] = el;
              }}
              position={[seg, 0, 0]}
            >
              <mesh material={mats.paper} position={[seg / 2, 0, 0]} rotation={[-Math.PI / 2, 0, 0]}>
                <planeGeometry args={[seg, D * 0.93]} />
              </mesh>
              <group position={[seg, 0, 0]}>
                <mesh material={mats.paper} position={[seg / 2, 0, 0]} rotation={[-Math.PI / 2, 0, 0]}>
                  <planeGeometry args={[seg, D * 0.93]} />
                </mesh>
              </group>
            </group>
          </group>
        </group>
      ))}

      {/* Свечение из корешка. Аддитивное и без depthWrite — иначе плоскость
          перекрывала бы страницы, а не лежала между ними. */}
      <mesh
        ref={glow}
        material={mats.glow}
        position={[0, TOP_Y + 0.07, 0]}
        rotation={[-Math.PI / 2, 0, 0]}
      >
        <planeGeometry args={[W * 1.9, D * 0.9]} />
      </mesh>
      <pointLight
        ref={light}
        position={[0, TOP_Y + 0.5, 0]}
        color="#c9a6ff"
        intensity={0.35}
        distance={2.6}
        decay={2}
      />
      </group>
    </group>
  );
}
