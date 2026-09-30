// 3D-фон перекрывал настоящие карты расклада декоративным «веером рубашек»
// (FoilFan). Владелец: «карты всегда должны быть видны, приглуши».
//
// Дефект был молчаливым: страница отдавала 200, разметка корректная, тесты
// зелёные — картину портили только три 3D-объекта в z-плоскости. Ловится
// только глазами или сравнением набора дочерних компонентов сцены.
//
// Приглушение сделано двумя независимыми средствами, потому что каждое
// закрывает свой класс проблем:
//   1) FoilFan выключен — он рисует РУБАШКИ карт ровно там, где лежат
//      настоящие, то есть конкурирует с ними напрямую;
//   2) вуаль над канвасом — гасит пыль, свет и боке, которые тоже снижали
//      контраст карт. Вуаль лежит в absolute-слое ПОД страницей, поэтому
//      гасится только фон, а контент остаётся контрастным.
import path from "node:path";
import fs from "node:fs";
import { describe, expect, it } from "vitest";

// __dirname уже указывает на components/cinematic, поэтому пути отсюда простые.
const here = __dirname;
const read = (p: string) => fs.readFileSync(path.join(here, p), "utf8");
const sceneSrc = read("SceneInner.tsx");
/** SceneInner без комментариев: они описывают дефекты и содержат те же слова,
 *  что и проверки, — иначе отрицание ловит собственное объяснение. */
const sceneCode = () =>
  sceneSrc.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
const backdropSrc = read("CinematicBackdrop.tsx");
const pageSrc = read("../../app/reading/[id]/page.tsx");
const liveSrc = read("../InterpretationLive.tsx");

describe("3D-фон не перекрывает карты расклада", () => {
  it("FoilFan выключается в приглушённом режиме", () => {
    // Обязательное условие: рубашки FoilFan стоят ровно там, где карты.
    // Условие переросло (добавлен тумблер стенда `lab.fan`), поэтому проверяем
    // структурно: в строке с <FoilFan /> обязаны присутствовать и `dimmed`,
    // и инверсия. Раньше тут стояла регулярка по точному литералу, и она
    // молча падала на любом расширении выражения.
    const line = sceneCode()
      .split("\n")
      .find((l) => l.includes("<FoilFan />"));
    expect(line, "FoilFan не найден в сцене").toBeTruthy();
    expect(line, "FoilFan обязан выключаться в приглушённом режиме").toMatch(/!dimmed/);
  });

  it("в обычном режиме FoilFan остаётся — фон не должен терять декорации", () => {
    // dimmed=true по умолчанию только на раскладе; на лендинге сцена полная.
    expect(sceneCode()).toMatch(/dimmed\s*=/);
    expect(sceneSrc).toMatch(/dimmed = false/);
    expect(backdropSrc).toMatch(/muted = false/);
  });

  it("поверх сцены кладётся вуаль — гасит пыль, свет и боке", () => {
    // Вуаль стала параметром (veil): на раскладе плотная, на главной лёгкая.
    // Проверяем, что он вообще применим и по умолчанию плотный.
    expect(backdropSrc).toMatch(/veil\s*=\s*0\.62/);
    expect(backdropSrc).toMatch(/muted &&/);
    expect(backdropSrc).toMatch(/rgba\(11,11,20/);
  });

  it("расклад берёт плотный вуаль, главная — лёгкий", () => {
    // Значения разъехались однажды: при 0.62 на главной храм превращался в
    // неразличимую тень, и сцена переставала работать как фон.
    const reading = read("../../app/reading/[id]/page.tsx");
    expect(reading).toMatch(/<CinematicBackdrop muted \/>/); // без veil => 0.62
    const hero = read("../HomeHero.tsx");
    const v = hero.match(/veil=\{([\d.]+)\}/);
    expect(v, "главная обязана задавать вуаль явно").toBeTruthy();
    expect(Number(v![1]), "вуаль главной слишком плотный — храм не читается").toBeLessThan(0.45);
  });

  it("вуаль НЕ накрывает контент: она внутри absolute-слоя фона", () => {
    // Слой фона — absolute inset-0 и рендерится ДО контента страницы.
    const iBackdrop = backdropSrc.indexOf("absolute inset-0");
    const iVeil = backdropSrc.indexOf("muted && (");
    expect(iBackdrop).toBeGreaterThan(-1);
    expect(iVeil, "вуаль не найдена в слое фона").toBeGreaterThan(iBackdrop);
  });

  it("приглушённый Lite-фон тоже гасится", () => {
    // Иначе на слабом устройстве фон внезапно станет контрастнее, чем в 3D.
    expect(backdropSrc).toMatch(/LiteBackdrop muted=\{muted\}/);
    expect(backdropSrc).toMatch(/muted\s*\?\s*"radial-gradient/);
  });
});

// Страница расклада обязана просить приглушение: иначе новый экран снова
// покажет карты под золотыми рубашками.
describe("расклад включает приглушение фона", () => {
  it("передаёт muted в бэкдроп", () => {
    expect(pageSrc).toMatch(/<CinematicBackdrop muted \/>/);
  });

  it("толкование остаётся поверх вуали и читаемым", () => {
    // Вуаль гасит только фон; колонка описания обязана остаться в контенте.
    // Текст толкования рисует InterpretationLive, поэтому проверяем и
    // порядок колонок на странице, и читаемый класс внутри компонента.
    const iBackdrop = pageSrc.indexOf("CinematicBackdrop");
    const iText = pageSrc.indexOf("InterpretationLive");
    expect(iBackdrop).toBeGreaterThan(-1);
    expect(iText).toBeGreaterThan(iBackdrop);
    expect(liveSrc).toMatch(/whitespace-pre-wrap text-sm leading-relaxed text-paper/);
  });
});

describe("смешанный рендер не ломается", () => {
  it("SceneInner рендерится как клиентский компонент с пропом", () => {
    // Регулярка по смыслу, а не по точному литералу: сигнатура уже расширялась
    // (dimmed → +lab для стенда), и проверка на строку молча ломалась на любой
    // добавленный проп — вместо того чтобы ловить реальный дефект.
    expect(sceneSrc).toMatch(/export default function SceneInner\(\{/);
    expect(sceneSrc).toMatch(/\bdimmed\b/);
    // dimmed обязан реально выключать FoilFan: рубашки стоят ровно там, где
    // лежат настоящие карты, и перекрывали их (жалоба владельца).
    const line = sceneCode()
      .split("\n")
      .find((l) => l.includes("<FoilFan />"));
    expect(line, "FoilFan не найден в сцене").toBeTruthy();
    expect(line).toMatch(/!dimmed/);
  });

  it("ограничение по частоте кадров (FPS-гард) не сломан", () => {
    // dimmed не должен выключать лестницу деградации: она снижает пыль и пост.
    expect(sceneSrc).toMatch(/<FpsGuard onLevel=\{setLevel\} \/>/);
    expect(sceneSrc).toMatch(/<Dust count=/);
  });
});

// Атмосферная перспектива. Без неё дальний край пола виден жёсткой полосой
// через весь кадр: FogQuad рисуется фоном (depthTest:false, первым) и не
// затягивает геометрию, поэтому 14×14 пол обрывался чёткой линией там, где
// заканчивался. На скриншоте это читалось как «серое пятно поперёк кадра».
describe("сцена не обрывается на горизонте", () => {
  it("в сцене есть fog — дальняя геометрия уходит в цвет неба", () => {
    expect(sceneCode()).toMatch(/fogExp2\s+attach="fog"/);
  });

  it("пол шире фрустума, чтобы его край был дальше границы тумана", () => {
    // Имя НЕ floor: это глобальная функция JS, её затенение внутри it() путает
    // и отладку, и часть инструментов.
    const temple = read("Temple.tsx");
    const sizes = [...temple.matchAll(/planeGeometry args=\{\[([\d.]+), ([\d.]+)\]\}/g)];
    expect(sizes.length, "в храме нет пола").toBeGreaterThan(0);
    for (const m of sizes) {
      expect(Number(m[1]), `пол ${m[1]}x${m[2]} обрывается в кадре`).toBeGreaterThanOrEqual(30);
    }
    expect(temple).not.toMatch(/args=\{\[14, 14\]\}/);
  });

  it("цвет тумана светлее ночного фона, иначе геометрия силуэтится", () => {
    // Раньше стоял ровно #0B0B14 (0.043), тогда как фон шейдера у горизонта
    // около 0.10. Дальняя геометрия гасла в более тёмный цвет, чем небо за
    // ней, и апсида вырисовывалась силуэтом с жёсткой верхней кромкой —
    // ровно та же ошибка, что была с краем пола. Теперь цвет СВЕТЛЕЕ базы.
    const m = sceneCode().match(/fogExp2[\s\S]{0,140}?args=\{\[FOG_MATCH/);
    expect(m, "цвет тумана должен идти через FOG_MATCH").toBeTruthy();
    const decl = sceneCode().match(/FOG_MATCH = "(#[0-9A-Fa-f]{6})"/);
    expect(decl, "FOG_MATCH не объявлен").toBeTruthy();
    // Относительная яркость по ВСЕМ каналам, а не по красному: у фиолетово-
    // чёрного тона почти вся яркость в синем, и подсчёт по R давал бы
    // ложное «туман темнее фона».
    const hex = decl![1].slice(1);
    const chan = (i: number) => parseInt(hex.slice(i * 2, i * 2 + 2), 16) / 255;
    const lum =
      0.299 * chan(0) + 0.587 * chan(1) + 0.114 * chan(2);
    const night =
      0.299 * (11 / 255) + 0.587 * (11 / 255) + 0.114 * (20 / 255);
    expect(lum, "цвет тумана должен быть светлее ночного фона #0B0B14").toBeGreaterThan(
      night
    );
  });
});

// Пыль: до правки частицы у самой камеры давали gl_PointSize до 20 device-px и
// читались как белые шары. Кламп и отсев обязательны.
describe("пыль остаётся пылью", () => {
  const dust = read("Dust.tsx");
  it("размер частицы ограничен сверху", () => {
    expect(dust).toMatch(/gl_PointSize = clamp\(/);
  });
  it("частицы у камеры отбрасываются", () => {
    // Частица с -mv.z < 0.9 даёт gl_PointSize = 4.0/0.2 = 20 device-px —
    // белое пятно поверх контента. Такие выбрасываются в вершинном шейдере.
    expect(dust).toMatch(/modelViewMatrix/);
    expect(dust).toMatch(/\.z < 0\.9/);
  });
});
