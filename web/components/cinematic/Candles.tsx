"use client";

// Свечи алтаря: корпус, фитиль, шейдерное пламя.
//
// Про мерцание — это ограничение ДОСТУПНОСТИ, а не вкусовое. WCAG 2.3.1
// запрещает яркие вспышки чаще 3 раз в секунду: у людей с фотосенсибилитизмом
// и вестибулярными расстройствами от них бывает тошнота и головокружение.
// Поэтому здесь медленное «дыхание» ~0.4Гц с малой амплитудой: выглядит как
// живой огонь и остаётся в допуске. Быстрый «нервный» огонь здесь был бы
// эффектнее на 2 секунды просмотра и бит по людям — не берём.

import { useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { ALTAR_Z, altarSurface } from "./Temple";
import type { Lab } from "./SceneInner";

/* Пламя: каплевидная маска, искажённая по времени + мерцание масштаба. */
const FLAME_VERT = /* glsl */ `
// precision обязана совпадать с фрагментной: uTime объявлен в обоих, и при
// расхождении (highp против mediump) драйвер отвергает всю программу.
precision highp float;
uniform float uTime;
uniform float uFlicker;
varying vec2 vUv;
void main() {
  vUv = uv;
  vec3 p = position;
  // Язык пламени качает СИЛЬНЕЕ ВВЕРХУ, низ у фитиля почти неподвижен.
  // Прежний код делал наоборот по форме и читался как столбик.
  float h = p.y + 0.5;                       // 0 внизу, 1 вверху
  float sway  = sin(uTime * 1.7 + h * 2.2) * 0.085 * uFlicker * h;
  float sway2 = sin(uTime * 2.9 + h * 5.1) * 0.035 * uFlicker * h;
  p.x += sway + sway2;
  // Лёгкое сплющивание: язык то вытягивается, то приседает
  p.y *= 0.92 + 0.16 * uFlicker;
  p.x *= 1.0 - 0.12 * h;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(p, 1.0);
}
`;

const FLAME_FRAG = /* glsl */ `
precision highp float;
varying vec2 vUv;
uniform float uTime;
uniform float uFlicker;

// Псевдошум без текстуры
float h(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
float n(vec2 p) {
  vec2 i = floor(p), f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(h(i), h(i + vec2(1.0, 0.0)), u.x),
             mix(h(i + vec2(0.0, 1.0)), h(i + vec2(1.0, 1.0)), u.x), u.y);
}

void main() {
  vec2 uv = vUv;
  float y = uv.y;

  // ── ФОРМА ЯЗЫЧКА.
  // Прежняя маска 0.42*pow(y, 0.55) давала НУЛЕВУЮ ширину внизу и
  // максимум сверху — то есть перевёрнутый огонь, который и выглядел
  // столбиком. Настоящий язычок: узкий у фитиля, шире к трети высоты,
  // и сходит в остриё наверху.
  float w = sin(pow(y, 0.72) * 3.14159) * 0.46;
  w *= 1.0 - 0.18 * y;                    // к острию чуть уже

  // Язык «облизывает» вбок: осевая линия уводится с высотой, причём не по
  // одной синусоиде, а по двум — иначе качание читается маятником.
  float cx = (sin(uv.y * 4.0 - uTime * 1.05) * 0.085
            + sin(uv.y * 9.0 - uTime * 1.62) * 0.032) * y;
  float x = uv.x - 0.5 - cx;
  // Кромка неровная: огонь не бывает ровной колбасой
  w *= 1.0 - 0.20 * y * (0.5 + 0.5 * sin(uv.y * 6.0 + uTime * 1.3));

  float body = smoothstep(w, w * 0.12, abs(x));
  // Низ срезан по фитилю: огонь начинается чуть выше воска
  body *= smoothstep(0.0, 0.10, y);
  if (body <= 0.004) discard;

  // ── ЦВЕТ. Внизу — почти белый (температура максимальная), к острию
  // оранжевый, и сам кончик тускнеет.
  float core = 1.0 - smoothstep(0.0, w * 0.62, abs(x));
  vec3 col = mix(vec3(1.00, 0.34, 0.06), vec3(1.00, 0.78, 0.32), body);
  col = mix(col, vec3(1.00, 0.96, 0.84), core * 0.92);
  // Тонкая синяя зона у самого фитиля — там, где горит водород
  float base = smoothstep(0.26, 0.02, y) * (1.0 - core * 0.5);
  col = mix(col, vec3(0.45, 0.70, 1.00), base * 0.35);
  // Остриё гаснет
  col *= 1.0 - smoothstep(0.78, 1.0, y) * 0.75;

  // ── МЕРЦАНИЕ. Медленное «дыхание» + мелкая рябь по краю, и всё это
  // синхронно с интенсивностью света (WCAG 2.3.1: ярких вспышек >3/с нет).
  float breathe = 0.84 + 0.16 * sin(uTime * 2.2);
  float ripple = 0.92 + 0.08 * n(vec2(y * 7.0, uTime * 0.9));
  gl_FragColor = vec4(col * breathe * ripple, body * (0.75 + 0.25 * core) * uFlicker);
}
`;

function Candle({
  x,
  z,
  y,
  scale = 1,
  seed = 0,
}: {
  x: number;
  z: number;
  y: number;
  scale?: number;
  /** Сдвиг фазы. Четыре свечи, мерцающие синхронно, читаются как гирлянда
   *  на проводе, а не как четыре отдельных огня. */
  seed?: number;
}) {
  const flameMat = useMemo(
    () =>
      new THREE.ShaderMaterial({
        vertexShader: FLAME_VERT,
        fragmentShader: FLAME_FRAG,
        uniforms: { uTime: { value: seed }, uFlicker: { value: 1 } },
        transparent: true,
        depthWrite: false,
        blending: THREE.AdditiveBlending,
      }),
    [seed]
  );
  const light = useRef<THREE.PointLight>(null);
  const wax = useMemo(
    () => new THREE.MeshStandardMaterial({ color: "#E8DFC8", roughness: 0.75 }),
    []
  );
  const holder = useMemo(
    () => new THREE.MeshStandardMaterial({ color: "#D4AF37", roughness: 0.3, metalness: 0.9 }),
    []
  );

  useFrame((state) => {
    const t = state.clock.elapsedTime + seed;
    flameMat.uniforms.uTime.value = t;
    // Тот же медленный ритм у света и у пламени, иначе они «разходятся».
    const breathe = 0.85 + 0.15 * Math.sin(t * 2.5);
    if (light.current) light.current.intensity = 5.5 * breathe * scale;
  });

  return (
    <group position={[x, y, z]} scale={scale}>
      {/* Подсвечник */}
      <mesh material={holder} position={[0, 0.03, 0]}>
        <cylinderGeometry args={[0.11, 0.16, 0.06, 16]} />
      </mesh>
      <mesh material={holder} position={[0, 0.16, 0]}>
        <cylinderGeometry args={[0.035, 0.05, 0.22, 12]} />
      </mesh>
      <mesh material={holder} position={[0, 0.3, 0]}>
        <cylinderGeometry args={[0.07, 0.05, 0.05, 12]} />
      </mesh>
      {/* Корпус свечи: слегка неровный верх, как у настоящей */}
      <mesh material={wax} position={[0, 0.55, 0]}>
        <cylinderGeometry args={[0.045, 0.05, 0.45, 14]} />
      </mesh>
      <mesh material={wax} position={[0, 0.78, 0]}>
        <cylinderGeometry args={[0.03, 0.045, 0.03, 14]} />
      </mesh>
      {/* Пламя */}
      <mesh material={flameMat} position={[0, 0.855, 0]}>
        <planeGeometry args={[0.145, 0.27]} />
      </mesh>
      {/* Свет от пламени — то, что реально освещает алтарь */}
      <pointLight
        ref={light}
        position={[0, 0.9, 0]}
        color="#F1D97B"
        intensity={5.5}
        distance={5}
        decay={2}
      />
    </group>
  );
}

/**
 * Свечи на алтаре и у его основания. Положение считается от глубины алтаря,
 * чтобы ступени и свечи не разъезжались при правке стенда.
 */
export default function Candles({ lab }: { lab?: Lab }) {
  const top = altarSurface(lab?.altarZ ?? ALTAR_Z);
  const topY = top.y;
  const topZ = top.z;
  const amount = lab?.candles ?? 1;
  if (amount < 0.5) return null;

  return (
    <group>
      {/* Две на алтаре */}
      <Candle x={-0.7} z={topZ} y={topY} scale={0.75} seed={0} />
      <Candle x={0.7} z={topZ} y={topY} scale={0.75} seed={2.7} />
      {/* Две у основания ступеней — задают ближний тёплый свет */}
      <Candle x={-1.9} z={topZ + 1.7} y={-1.2 + 0.11} scale={1.25} seed={5.1} />
      <Candle x={1.9} z={topZ + 1.7} y={-1.2 + 0.11} scale={1.25} seed={1.4} />
    </group>
  );
}
