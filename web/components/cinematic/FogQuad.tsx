// Атмосфера: объёмный туман + лучи + звёздное небо + золотые «чернила».
//
// Прежняя версия была `smoothstep` двух слоёв шума — это плоское свечение, а
// не свет. Здесь источник света живёт ЗА заголовком, и туман марчится к
// наблюдателю: плотность падает с расстоянием, края лучей рвутся шумом,
// чтобы свет не читался как геометрический конус.
//
// Стоимость: один fullscreen-quad, один проход фрагментного шейдера, без
// циклов по пикселям. Октавы fbm фиксированы и развёрнуты на месте (unrolled),
// чтобы компилятор не сделал цикл — на слабых GPU это разница в 2-3 раза.
import { useMemo } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { isFinePointer } from "@/lib/device";
import type { Lab } from "./SceneInner";

const VERT = /* glsl */ `
varying vec2 vUv;
void main() { vUv = uv; gl_Position = vec4(position.xy, 0.0, 1.0); }
`;

const FRAG = /* glsl */ `
precision highp float;
varying vec2 vUv;
uniform float uTime;
uniform vec2 uShift;
uniform float uFog;      // плотность тумана
uniform float uRays;     // сила лучей
uniform float uStars;    // звёзды: 0 = чистое небо
uniform float uStarSpd;  // скорость бега неба
uniform float uStarSize; // размер звёзд
uniform float uDepth;    // глубина (экспоненциальное затухание)
uniform float uAspect;   // ширина/высота кадра
uniform vec2 uLight;     // положение источника в UV

float hash(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }

float noise(vec2 p) {
  vec2 i = floor(p), f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), u.x),
             mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), u.x), u.y);
}

// 4 октавы, развёрнуты: быстрее цикла на слабых GPU
float fbm4(vec2 p) {
  float s = noise(p) * 0.5;
  s += noise(p * 2.03) * 0.25;
  s += noise(p * 4.01) * 0.15;
  s += noise(p * 8.07) * 0.10;
  return s;
}

/** Поворот координат — небо поворачивается вокруг полюса, а не едет строем. */
vec2 rot(vec2 p, float a) {
  float s = sin(a), c = cos(a);
  return vec2(c * p.x - s * p.y, s * p.x + c * p.y);
}

/** Цвет звезды по «температуре»: сине-белые → белые → золотые → оранжевые. */
vec3 starColor(float t) {
  vec3 blue = vec3(0.70, 0.81, 1.00);
  vec3 white = vec3(0.97, 0.97, 0.95);
  vec3 gold = vec3(1.00, 0.83, 0.58);
  vec3 red = vec3(1.00, 0.63, 0.44);
  if (t < 0.42) return mix(blue, white, t / 0.42);
  if (t < 0.86) return mix(white, gold, (t - 0.42) / 0.44);
  return mix(gold, red, (t - 0.86) / 0.14);
}

// ── Звёздное поле.
//
// Три свойства, без которых небо читается как синтетика. Владелец:
// «звёздного неба не вижу, хочу чтобы оно двигалось как будто мы на месте,
// а небо бежит, нужен прям фотореализм».
//
// 1) КООРДИНАТЫ ЭКРАНА, А НЕ КЛЕТОК. Поле считается в sp — координатах
//    ровно одного кадра (y от -0.5 до 0.5). Раньше оно считалось в p, а p
//    зависел от масштаба фона, и на телефоне (узкий кадр) звёзд было втрое
//    больше, чем на десктопе, а «скорость 0.05 клетки в секунду» при ячейке
//    1/31 экрана давала 0.03% экрана в секунду — то есть небо не двигалось
//    вообще. Теперь скорость задана в долях высоты кадра в секунду и не
//    зависит ни от плотности сетки, ни от размера окна.
//
// 2) ПАРАЛЛАКС ЧЕРЕЗ ВРАЩЕНИЕ. На небесной сфере звёзды идут по дуге вокруг
//    полюса, а не по прямой. Полюс вынесен НАД кадром: оттуда движение
//    читается как «стоишь на месте, небо едет мимо». Прямая подрезка поля
//    сразу выдаёт себя.
//
// 3) ФОТОРЕАЛИЗМ ПО СВЕТУ И РАЗМЕРУ:
//      • степенной закон: тусклых много (pow(hash, 4.5)), ярких единицы;
//      • ярче — значит крупнее; одинаковые точки мгновенно выдают сетку;
//      • цвет по температуре, а не «все белые»;
//      • дифракционный крест у самых ярких;
//      • атмосферное гашение у горизонта: низкие звёзды тусклые и рыжие.

/**
 * Один слой звёзд.
 * scale — ячеек по высоте кадра (плотность)
 * speed — доля высоты кадра в секунду (видимая скорость)
 * spin  — радиан в секунду вокруг полюса
 * fill  — доля ячеек, в которых вообще есть звезда
 */
vec3 starLayer(vec2 sp, float scale, float speed, float spin, float fill,
               float bright, float spikes, float size) {
  // Полюс выше верхней кромки кадра — оттуда дуги, а не сдвиг
  const vec2 POLE = vec2(0.0, 0.46);
  vec2 g = rot(sp - POLE, uTime * spin) + POLE;
  vec2 gp = g * scale + vec2(uTime * speed * scale, uTime * speed * scale * 0.13);

  vec2 id = floor(gp);
  vec2 f = fract(gp) - 0.5;
  float h = hash(id);
  if (h > fill) return vec3(0.0);

  vec2 off = (vec2(hash(id + 1.73), hash(id + 5.31)) - 0.5) * 0.72;
  vec2 d = f - off;

  // Радиус в клетках: размер задан в долях кадра и пересчитывается под сетку,
  // иначе дальние слои превращаются в крупные шарики, а ближние — в пиксель.
  float r = max(size * scale, 0.09);
  float mag = pow(hash(id + 9.17), 4.5);
  float tw = 0.76 + 0.24 * sin(uTime * 0.55 + h * 42.0);

  float core = smoothstep(r, 0.0, length(d)) * (0.10 + mag * 2.0);
  vec3 tint = starColor(hash(id + 3.77));
  vec3 col = tint * core * tw;

  if (spikes > 0.5 && mag > 0.45) {
    // Обе оси гаснут РОВНО на границе ячейки, иначе у ярких звёзд виден
    // обрыв луча на краю клетки.
    float sh = smoothstep(r * 1.7, 0.0, abs(d.y)) * smoothstep(0.5, 0.0, abs(d.x));
    float sv = smoothstep(r * 1.4, 0.0, abs(d.x)) * smoothstep(0.5, 0.0, abs(d.y));
    col += tint * (sh * 0.6 + sv * 0.42) * mag * tw;
  }
  return col * bright;
}

/**
 * Млечный Путь. Полоса неразрешённых звёзд и пыли поперёк кадра — та деталь,
 * по которой небо узнаётся на настоящей фотографии. Без неё даже идеально
 * расставленные звёзды выглядят как гирлянда.
 * Ось полосы гуляет на шуме, иначе прямая линия читается как покраска.
 */
float milkyWay(vec2 sp) {
  float axis = sp.y - sp.x * 0.34 + (noise(sp * 1.7 + 4.0) - 0.5) * 0.30;
  float core = exp(-axis * axis * 30.0);
  float halo = exp(-axis * axis * 6.5);
  // Тёмные пылевые волокна внутри полосы — они и делают её похожей на снимок
  float dust = smoothstep(0.38, 0.76, noise(sp * 5.0 + 11.0));
  return core * (0.30 + 0.95 * dust) + halo * 0.34 * (0.35 + 0.65 * dust);
}

void main() {
  vec2 uv = vUv;
  // Фон рисуется ровно на весь кадр: gl_Position уже в clipspace, плоскость
  // 2×2 закрывает [-1,1]². Масштабировать геометрию «под аспект» больше не
  // нужно — аспект приходит в шейдер, и координаты звёзд от размера окна
  // не зависят.
  vec2 p  = (uv - 0.5) * vec2(uAspect, 1.0) + uShift;
  // Координаты ровно одного кадра: y ∈ [-0.5, 0.5]
  vec2 sp = (uv - 0.5) * vec2(uAspect, 1.0);

  // ── Лучи. Это храм, а не открытое небо: свет падает СВЕРХУ, из высоких
  //    окон, поэтому лучи идут столбом сверху вниз и не расходятся веером.
  //    Прежняя модель считала их от источника-заголовком (sin от угла), и
  //    на скриншоте это выглядело солнечной звездой ровно посередине кадра —
  //    «дешёвый» свет, который никогда не появляется в помещении.
  // Окна: узкая светящаяся полоса у самой верхней кромки.
  float window = smoothstep(0.80, 0.90, uv.y);

  // Лучи: из окон вниз, затухая. Угол задаётся X, а не полярным углом —
  // иначе свет расходился веером из точки, что в помещении не бывает.
  float shaft = fbm4(vec2(uv.x * 2.6, uTime * 0.03));
  float rays = pow(max(0.0, sin(uv.x * 7.0 + shaft * 3.4)), 5.0);
  rays *= smoothstep(0.26, 0.72, uv.y);
  // Наклон: лучи слегка расходятся, иначе читаются как полосы обоев
  rays *= 0.85 + 0.15 * sin(uv.x * 3.1);

  // ── Туман: два слоя с разной скоростью = ощущение объёма
  float far  = fbm4(p * 1.6 + vec2(uTime * 0.012, uTime * 0.005));
  float near = fbm4(p * 3.3 - vec2(uTime * 0.028, uTime * 0.010));
  float fog  = mix(far, near, 0.42) * uFog;

  // ── «Чернила»: domain warp даёт нитевидные прожилки вместо каши
  vec2 w = vec2(fog, fbm4(p * 2.1 + 5.2));
  float ink = fbm4(p * 5.0 + w * 2.4);
  ink = pow(clamp(ink, 0.0, 1.0), 3.0);

  // ── Сборка цвета
  // ВСЕ константы нормированы вкладом в итоговую яркость. Раньше rayGold шла
  // без множителя, а violet/gold — с 0.20/0.08, и лучи давали 0.61 из 0.71 в
  // красном канале: верх кадра выходил кремовым и выглядел пересвеченным
  // потолком. Теперь каждый вклад сравним.
  vec3 night   = vec3(0.043, 0.043, 0.078);                     // #0B0B14
  vec3 violet  = vec3(0.486, 0.361, 1.000) * 0.20;             // #7C5CFF тело
  vec3 gold    = vec3(0.831, 0.686, 0.216) * 0.08;             // #D4AF37 акцент
  vec3 rayGold = vec3(0.945, 0.851, 0.482) * 0.12;             // #F1D97B луч

  // Туман — это зал, а не небо: к зениту он рассеивается, иначе верх кадра
  // снова становится кремовым и звёзды в нём тонут. Но рассеивание начинается
  // ВЫШЕ горизонта (горизонт в кадре — это ~0.65 по uv.y, там кончается пол):
  // ниже него фон обязан совпадать с FOG_MATCH, иначе стык неба и пола виден
  // жёсткой линией — ровно то, что уже чинили на самом поле.
  float height = smoothstep(0.66, 1.02, uv.y);
  float fogUp = fog * (1.0 - 0.7 * height);

  vec3 col = night;
  col += violet * smoothstep(0.15, 0.85, fogUp);
  col += gold * ink * 1.4 * (1.0 - 0.4 * height);
  col += rayGold * rays * uRays;
  // Полоса окон — источник, а не «потолок»: узкая и тихая
  col += rayGold * window * 0.10 * uRays;
  col += rayGold * pow(smoothstep(0.66, 0.90, uv.y), 2.0) * 0.06 * uRays;

  // ── Небо. Гаснет у горизонта (за стеной храма неба не видно), под плотным
  //    туманом и за светом окон — там, где яркий фон съел бы слабые звёзды.
  float sky = smoothstep(0.28, 0.50, uv.y);
  if (uStars > 0.001 && sky > 0.004) {
    float veil = clamp(1.0 - fogUp * 0.30, 0.25, 1.0) * (1.0 - window * 0.5);
    float mw = milkyWay(sp) * sky;
    // Сама полоса: холодное сияние + тёплый пылевой налёт
    col += vec3(0.052, 0.055, 0.078) * mw * 1.20;
    col += vec3(0.085, 0.070, 0.048) * mw * mw * 0.90;
    // Четыре слоя: дальние — пыль Млечного Пути, ближние — редкие яркие.
    // Яркости занижены против «красивых» значений: каждая звезда проходит
    // через Bloom (порог 0.62) и ACES, и в сумме поле превращается в снег.
    col += starLayer(sp,  55.0, 0.0022 * uStarSpd, 0.0016, 0.10, 0.16, 0.0, 0.0013 * uStarSize) * (0.35 + mw * 2.2) * veil * uStars;
    col += starLayer(sp, 100.0, 0.0045 * uStarSpd, 0.0026, 0.09, 0.30, 0.0, 0.0013 * uStarSize) * (0.50 + mw * 1.5) * veil * uStars;
    col += starLayer(sp, 175.0, 0.0080 * uStarSpd, 0.0040, 0.06, 0.52, 0.0, 0.0012 * uStarSize) * veil * uStars;
    col += starLayer(sp, 260.0, 0.0135 * uStarSpd, 0.0055, 0.04, 0.85, 1.0, 0.0011 * uStarSize) * veil * uStars;
    // Атмосферное гашение: у горизонта звёзды тусклые и рыжие
    col = mix(col, col * vec3(1.10, 0.86, 0.66) * 0.55, smoothstep(0.34, 0.10, uv.y) * 0.8);
  }

  gl_FragColor = vec4(col, 1.0);
}
`;

export default function FogQuad({ lab }: { lab?: Lab }) {
  const { pointer, size } = useThree();
  const k = useMemo(() => (isFinePointer() ? 0.03 : 0.01), []);

  const uniforms = useMemo(
    () => ({
      uTime: { value: 0 },
      uShift: { value: new THREE.Vector2() },
      uFog: { value: lab?.fog ?? 1 },
      uRays: { value: lab?.rays ?? 1 },
      uStars: { value: lab?.star ?? 1 },
      uStarSpd: { value: lab?.starSpd ?? 1 },
      uStarSize: { value: lab?.starSize ?? 1 },
      uDepth: { value: lab?.fogD ?? 0.04 },
      uAspect: { value: 1 },
      // Источник чуть выше центра — за заголовком
      uLight: { value: new THREE.Vector2(0.5, 0.56) },
    }),
    // Значения перечитываются на смене слайдера, а uniform-объекты мутируются ниже
    [] // eslint-disable-line react-hooks/exhaustive-deps
  );

  useFrame((state) => {
    uniforms.uTime.value = state.clock.elapsedTime;
    uniforms.uShift.value.set(pointer.x * k, pointer.y * k);
    uniforms.uAspect.value = size.width / Math.max(1, size.height);
    if (lab) {
      uniforms.uFog.value = lab.fog ?? 1;
      uniforms.uRays.value = lab.rays ?? 1;
      uniforms.uStars.value = lab.stars === 0 ? 0 : (lab.star ?? 1);
      uniforms.uStarSpd.value = lab.starSpd ?? 1;
      uniforms.uStarSize.value = lab.starSize ?? 1;
      uniforms.uDepth.value = lab.fogD ?? 0.04;
    }
  });

  const mat = useMemo(
    () =>
      new THREE.ShaderMaterial({
        vertexShader: VERT,
        fragmentShader: FRAG,
        uniforms,
        depthWrite: false,
        depthTest: false,
      }),
    [uniforms]
  );

  return (
    // Плоскость 2×2 в clipspace закрывает кадр целиком; frustumCulled снят,
    // потому что вершины лежат на границе видимого объёма камеры.
    <mesh material={mat} frustumCulled={false} renderOrder={-1}>
      <planeGeometry args={[2, 2]} />
    </mesh>
  );
}
