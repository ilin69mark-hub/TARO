"use client";

// GPU-пыль: 600 desktop / ≤150 mobile / 0 Lite (см. 09-premium-temple.md).
// Дрейф вверх + мерцание; пауза при скрытом табе (см. T27).
import { useMemo, useRef, useEffect, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";

const VERT = /* glsl */ `
attribute float aSeed;
varying float vSeed;
void main() {
  vSeed = aSeed;
  vec4 mv = modelViewMatrix * vec4(position, 1.0);
  gl_PointSize = (1.0 + fract(aSeed) * 2.0) * (120.0 / -mv.z);
  gl_Position = projectionMatrix * mv;
}
`;

const FRAG = /* glsl */ `
precision highp float;
varying float vSeed;
uniform float uTime;
void main() {
  vec2 c = gl_PointCoord - 0.5;
  if (dot(c, c) > 0.25) discard;
  float tw = 0.5 + 0.5 * sin(uTime * 2.0 + vSeed * 20.0);
  gl_FragColor = vec4(vec3(0.95, 0.85, 0.48) * tw, 0.55 * tw);
}
`;

export default function Dust({ count }: { count: number }) {
  const ref = useRef<THREE.Points>(null);
  const [visible, setVisible] = useState(true);

  useEffect(() => {
    const onVis = () => setVisible(!document.hidden);
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, []);

  const { geo, mat } = useMemo(() => {
    const pos = new Float32Array(count * 3);
    const seed = new Float32Array(count);
    for (let i = 0; i < count; i++) {
      pos[i * 3] = (Math.random() - 0.5) * 8;
      pos[i * 3 + 1] = Math.random() * 4 - 1;
      pos[i * 3 + 2] = -Math.random() * 5;
      seed[i] = Math.random() * 100;
    }
    const geo = new THREE.BufferGeometry();
    geo.setAttribute("position", new THREE.BufferAttribute(pos, 3));
    geo.setAttribute("aSeed", new THREE.BufferAttribute(seed, 1));
    const mat = new THREE.ShaderMaterial({
      vertexShader: VERT,
      fragmentShader: FRAG,
      uniforms: { uTime: { value: 0 } },
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    });
    return { geo, mat };
  }, [count]);

  useFrame((state) => {
    mat.uniforms.uTime.value = state.clock.elapsedTime;
    if (ref.current) ref.current.position.y = Math.sin(state.clock.elapsedTime * 0.1) * 0.2;
  });

  if (!visible || count === 0) return null;
  return <points ref={ref} geometry={geo} material={mat} frustumCulled={false} />;
}
