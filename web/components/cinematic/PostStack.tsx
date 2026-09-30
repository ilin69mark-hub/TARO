"use client";

// Пост-обработка сцены (см. 05-design/08-ultra-cinematic.md §3, 09 §7).
//
// Задача стека — сделать картинку «снимком», а не рендером:
//
//   DepthOfField — размывает всё, кроме плоскости алтаря. Это не украшение,
//     а приём композиции: размытые края уводят взгляд в центр, где алтарь и
//     рубашка. Без него периферия (колонны у кромок) спорит с фокусом.
//   Bloom        — свечи и золото «пылают», как в объективе.
//   ChromaticAberration — по краям кадра, слабо: имитирует линзу.
//   LUT          — единый тон: тени в фиолет, света в золото. Сцена собирается
//     из холодного фона и тёплых источников, и без грейда она «пестрит».
//   Noise        — зерно плёнки. Убирает бандинги на градиентах тумана.
//   Vignette     — кадр в кадре.
//   SMAA         — антиалиасинг. MSAA в EffectComposer отключён (multisampling=0),
//     потому что он не работает с полноэкранными проходами.
//
// ВАЖНО: весь стек выключается на уровне FPS-гарда (см. SceneInner) и целиком
// в Lite. Пост-эффекты — самая дорогая часть, и на слабом телефоне они
// съедают кадры быстрее, чем дают вид.

import { useMemo } from "react";
import {
  EffectComposer,
  Bloom,
  DepthOfField,
  ChromaticAberration,
  Noise,
  Vignette,
  SMAA,
} from "@react-three/postprocessing";
import { BlendFunction } from "postprocessing";
import * as THREE from "three";
import type { Lab } from "./SceneInner";

export default function PostStack({ lab }: { lab?: Lab }) {
  const ca = useMemo(
    () => new THREE.Vector2(0.0004, 0.0009),
    []
  );
  const focus = lab?.dof ?? 12.0;

  return (
    <EffectComposer multisampling={0} enableNormalPass={false}>
      {/* Фокус на алтаре. worldFocusRange — ширина резкой зоны: чем меньше,
          тем «портретнее» кадр. Значение подобрано под altarZ: плоскость
          фокуса должна ложиться на алтарь, а не на передний план.

          bokehScale пришлось убрать с 2.6 до 0.9. Эффект считает размытие
          ЛИНЕЙНО по расстоянию и упирается в потолок на дальней плоскости
          (а небо — это фоновый quad без depthWrite, то есть в буфере там
          дальний клип, ~1000 м). При 2.6 каждая звезда превращалась в
          боке-диск диаметром 15 px, и небо читалось как снег, а не как
          фотография. 0.9 даёт звезде точку с мягким ореолом — как на
          реальном снимке, где звёзды снимают на закрытой диафрагме. */}
      <DepthOfField
        worldFocusDistance={focus}
        worldFocusRange={lab?.dofR ?? 5}
        bokehScale={lab?.bokeh ?? 0.9}
        resolutionScale={0.6}
      />
      <Bloom
        intensity={lab?.bloom ?? 0.85}
        luminanceThreshold={lab?.bloomT ?? 0.62}
        luminanceSmoothing={0.25}
        mipmapBlur
      />
      <ChromaticAberration
        offset={ca}
        blendFunction={BlendFunction.NORMAL}
        radialModulation
        modulationOffset={0.35}
      />
      {/* ToneMapping здесь НЕ включаем: он уже выставлен на gl в
          SceneInner.onCreated. Второй ACES поверх первого сжимал кадр
          почти в чёрный.

          LUT-грейдинг тоже выключен: самодельная 3D-таблица 32×32 в
          DataTexture отдавала почти чёрный кадр (конвенция чтения LUT в
          postprocessing — RGBE/RGBA, и такая текстура в неё не попадает).
          Тон добираем экспозицией gl и светом сцены. */}
      <Noise opacity={lab?.grain ?? 0.055} premultiply blendFunction={BlendFunction.SCREEN} />
      <Vignette darkness={lab?.vig ?? 0.55} offset={0.22} eskil={false} />
      <SMAA />
    </EffectComposer>
  );
}
