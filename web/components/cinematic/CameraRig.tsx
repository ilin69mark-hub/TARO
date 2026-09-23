"use client";

// Камера 3 акта (см. 09-premium-temple.md): Arrival 2с → Reading топ-даун.
// Assert: дистанция ≥2.2м (крупные планы арта запрещены) — clamp в коде.
// maath damp (см. D7 — зависимость больше не мертвая).
import { useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import { easing } from "maath";
import * as THREE from "three";

const ARRIVAL = new THREE.Vector3(0, 1.6, 3.2);
const READING = new THREE.Vector3(0, 3.0, 2.2); // топ-даун 15°
const MIN_DIST = 2.2;

export default function CameraRig() {
  const { camera } = useThree();
  const [act, setAct] = useState(0);
  const t = useRef(0);

  useFrame((_, delta) => {
    if (act === 0) {
      // акт 1: arrival ~2с
      t.current += delta;
      easing.damp3(camera.position, ARRIVAL, 0.8, delta);
      if (t.current > 2) setAct(1);
    } else {
      // акт 3: reading топ-даун (акт 2 Fan — DOM-веер карт, см. result)
      easing.damp3(camera.position, READING, 0.8, delta);
    }
    // assert дистанции (см. 09: clamp, не доверие словам)
    const d = camera.position.length();
    if (d < MIN_DIST) camera.position.setLength(MIN_DIST);
    camera.lookAt(0, 0.4, -0.5);
  });
  return null;
}
