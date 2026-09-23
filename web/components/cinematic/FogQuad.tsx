// Туман-quad: 1 fullscreen GLSL (fbm 3 октавы, violet 20% + gold 8%, см. 08-ultra).
// Параллакс pointer ×0.03 только desktop (см. lib/device.ts).
import { useMemo, useRef } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { isFinePointer } from "@/lib/device";

const VERT = /* glsl */ `
varying vec2 vUv;
void main() { vUv = uv; gl_Position = vec4(position.xy, 0.0, 1.0); }
`;

const FRAG = /* glsl */ `
precision highp float;
varying vec2 vUv;
uniform float uTime;
uniform vec2 uShift;
float hash(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
float noise(vec2 p) {
  vec2 i = floor(p), f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(hash(i), hash(i + vec2(1, 0)), u.x), mix(hash(i + vec2(0, 1)), hash(i + vec2(1, 1)), u.x), u.y);
}
float fbm(vec2 p) { return noise(p) * 0.5 + noise(p * 2.0) * 0.3 + noise(p * 4.0) * 0.2; }
void main() {
  vec2 p = vUv * 2.0 + uShift;
  float f = fbm(p + vec2(uTime * 0.02, 0.0));
  vec3 violet = vec3(0.486, 0.361, 1.0) * 0.20;
  vec3 gold = vec3(0.831, 0.686, 0.216) * 0.08;
  float glow = smoothstep(0.2, 0.9, f);
  vec3 col = vec3(0.043, 0.043, 0.078) + violet * glow + gold * glow * glow;
  gl_FragColor = vec4(col, 1.0);
}
`;

export default function FogQuad() {
  const ref = useRef<THREE.Mesh>(null);
  const { pointer } = useThree();
  // Параллакс pointer: ×0.03 desktop, ×0.01 touch (gyro-пермишн не нужен —
  // pointer-события работают и от тач-драга, см. D7).
  const k = useMemo(() => (isFinePointer() ? 0.03 : 0.01), []);
  const mat = useMemo(
    () =>
      new THREE.ShaderMaterial({
        vertexShader: VERT,
        fragmentShader: FRAG,
        uniforms: { uTime: { value: 0 }, uShift: { value: new THREE.Vector2() } },
        depthWrite: false,
        depthTest: false,
      }),
    []
  );
  useFrame((state) => {
    mat.uniforms.uTime.value = state.clock.elapsedTime;
    mat.uniforms.uShift.value.set(pointer.x * k, pointer.y * k);
  });
  return (
    <mesh ref={ref} material={mat} frustumCulled={false}>
      <planeGeometry args={[2, 2]} />
    </mesh>
  );
}
