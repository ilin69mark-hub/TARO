"use client";

// Камера 3 акта (см. 09-premium-temple.md): Arrival 2с → Reading топ-даун.
// Assert: дистанция ≥2.2м (крупные планы арта запрещены) — clamp в коде.
// maath damp (см. D7 — зависимость больше не мертвая).
import { useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import { easing } from "maath";
import * as THREE from "three";

// Камера отодвинута: раньше стояла в 3.2 единицах от алтаря, и храм шириной
// 5.2 занимал весь кадр — это читалось как «предметы в тёмной комнате».
// Теперь алтарь в 5.5, камера в 5.0: зал виден целиком, и остаётся место
// под заголовок поверх.
const ARRIVAL = new THREE.Vector3(0, 2.0, 5.0);
const READING = new THREE.Vector3(0, 3.4, 4.2); // топ-даун
const MIN_DIST = 2.2;
/**
 * Точка взгляда. Раньше стояла на 1.4 — алтарь с книгой попадал ровно в
 * середину кадра, где на главной живёт заголовок, и то, ради чего сцена
 * делалась, оказывалось закрыто текстом. Поднятая точка взгляда опускает
 * всю сцену в кадре: алтарь уходит под текстовый блок, а небо
 * ОДНОВРЕМЕННО занимает больше верхней части кадра. Оба условия
 * выполняются одним движением.
 */
const LOOK_AT = new THREE.Vector3(0, 2.2, -3.5);

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
    camera.lookAt(LOOK_AT);
  });
  return null;
}
