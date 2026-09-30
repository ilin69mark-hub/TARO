"use client";

// GPU-пыль: 600 desktop / ≤150 mobile / 0 Lite (см. 09-premium-temple.md).
// Дрейф + турбулентность + реакция на луч; пауза при скрытом табе (см. T27).
//
// Что изменилось против прежней версии. Частицы шли строго вверх по прямой
// (`y += t`) и мерцали `sin(t*2 + seed)`. Это читалось как «снег», а не пыль
// в луче света. Теперь движение — сумма трёх синусов с разными частотами и
// фазами (дешёвый аналог curl-noise), частица светится тем ярче, чем ближе к
// оси источника за заголовком, и гаснет вдали. Из-за этого пыль читается как
// объёмная, а не как плоский слой точек.
import { useMemo, useRef, useEffect, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { Lab } from "./SceneInner";

const VERT = /* glsl */ `
attribute float aSeed;
attribute float aScale;
varying float vSeed;
varying float vLit;
uniform float uTime;
uniform float uSize;

void main() {
  vSeed = aSeed;
  vec3 p = position;
  // Слишком близко к камере — выбрасываем: без этого они дают 20px пятна
  if (-(modelViewMatrix * vec4(p, 1.0)).z < 0.9) { gl_Position = vec4(2.0, 2.0, 2.0, 1.0); return; }

  // Турбулентность: три несинхронные компоненты дают «блуждание» без curl-noise
  float s = aSeed;
  p.x += sin(uTime * 0.31 + s * 1.7) * 0.35
       + sin(uTime * 0.13 + s * 4.1) * 0.22;
  p.z += cos(uTime * 0.27 + s * 2.3) * 0.28
       + cos(uTime * 0.11 + s * 5.7) * 0.18;
  // Медленный подъём + медленное опускание: пыль не летит, а висит
  p.y += sin(uTime * 0.17 + s * 0.9) * 0.9 + uTime * 0.055;
  // Оборачиваем по высоте объёма, иначе частицы уходят за потолок сцены
  p.y = mod(p.y + 1.2, 5.0) - 1.2;

  // Освещение: чем ближе к вертикали источника (x≈0, y≈1.6), тем ярче
  float d = length(vec2(p.x * 0.7, p.y - 1.6));
  vLit = 1.0 / (1.0 + d * d * 0.55);

  vec4 mv = modelViewMatrix * vec4(p, 1.0);
  // Калибровка размера. Прежний множитель 120 давал до 90 device-px — белые шары.
  //
  // Кламп обязателен и по другой причине: частица, оказавшаяся рядом с камерой,
  // даёт -mv.z ≈ 0.2, и 4.0/0.2 = 20px. Такие «ближние» точки и были видны как
  // крупные пятна, поэтому их просто не рисуем, а не увеличиваем.
  float sz = aScale * uSize * (4.0 / -mv.z);
  gl_PointSize = clamp(sz, 1.0, 4.0);
  gl_Position = projectionMatrix * mv;
}
`;

const FRAG = /* glsl */ `
precision highp float;
varying float vSeed;
varying float vLit;
uniform float uTime;
uniform float uDust;   // общая яркость слоя
void main() {
  vec2 c = gl_PointCoord - 0.5;
  float d2 = dot(c, c);
  if (d2 > 0.25) discard;
  // Мягкий край вместо жёсткой точки — на большем размере это заметно
  float soft = smoothstep(0.25, 0.0, d2);
  // Мерцание медленное (~0.5Гц) — WCAG 2.3.1 про вспышки
  float tw = 0.62 + 0.38 * sin(uTime * 0.5 + vSeed * 20.0);
  // Насыщенный фиолет в тени, золото в луче. Прежний cool (0.62,0.58,1.0)
  // на чёрном фоне читался как серый шарик.
  vec3 warm = vec3(1.00, 0.82, 0.42);
  vec3 cool = vec3(0.42, 0.36, 0.92);
  vec3 col = mix(cool, warm, vLit);
  gl_FragColor = vec4(col * tw, soft * tw * (0.05 + 0.30 * vLit) * uDust);
}
`;

export default function Dust({ count, lab }: { count: number; lab?: Lab }) {
  const ref = useRef<THREE.Points>(null);
  const [visible, setVisible] = useState(true);
  const density = lab?.dust ?? count;

  useEffect(() => {
    const onVis = () => setVisible(!document.hidden);
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, []);

  const { geo, mat } = useMemo(() => {
    const n = Math.max(0, Math.floor(density));
    const pos = new Float32Array(n * 3);
    const seed = new Float32Array(n);
    const scale = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      // Раскладываем по объёму храма, а не по кубу 8×4×5 — иначе половина
      // частиц оказывается за колоннами и не видна.
      const a = Math.random() * Math.PI * 2;
      const r = 1.2 + Math.pow(Math.random(), 0.6) * 3.4;
      pos[i * 3] = Math.cos(a) * r;
      pos[i * 3 + 1] = Math.random() * 4 - 1;
      pos[i * 3 + 2] = Math.sin(a) * r;
      seed[i] = Math.random() * 100;
      // Крупные частицы редки: иначе каша
      scale[i] = 0.5 + Math.pow(Math.random(), 2.2) * 1.1;
    }
    const geo = new THREE.BufferGeometry();
    geo.setAttribute("position", new THREE.BufferAttribute(pos, 3));
    geo.setAttribute("aSeed", new THREE.BufferAttribute(seed, 1));
    geo.setAttribute("aScale", new THREE.BufferAttribute(scale, 1));
    const mat = new THREE.ShaderMaterial({
      vertexShader: VERT,
      fragmentShader: FRAG,
      uniforms: {
        uTime: { value: 0 },
        uSize: { value: 1 },
        uDust: { value: count > 0 ? 1 : 0 },
      },
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    });
    return { geo, mat };
  }, [density, count]);

  useFrame((state) => {
    mat.uniforms.uTime.value = state.clock.elapsedTime;
  });

  if (!visible || count === 0 || density === 0) return null;
  return <points ref={ref} geometry={geo} material={mat} frustumCulled={false} />;
}
