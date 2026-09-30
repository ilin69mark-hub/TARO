"use client";

// Стенд сцены: слайдеры параметров без пересборки.
//
// Зачем он. Подбирать плотность тумана, силу лучей, высоту колонок и
// температуру света по скриншотам после каждой правки — цикл в минуты на
// итерацию и вслепую. Здесь параметры живут в стейте, применяются на лету,
// и стенд служит источником правды: наберём значения → переносим их в
// дефолты компонентов.
//
// Это инструмент разработки, а не продуктовая страница: в прод-сборке
// флаг `NEXT_PUBLIC_DESIGN_LAB` выключен, и роут отдаёт 404.
import { useEffect, useState } from "react";
import SceneInner from "@/components/cinematic/SceneInner";
import { isFinePointer } from "@/lib/device";

type Knob = {
  key: string;
  label: string;
  min: number;
  max: number;
  step: number;
  value: number;
};

const KNOBS: Knob[] = [
  { key: "fog", label: "Плотность тумана", min: 0, max: 2, step: 0.01, value: 1 },
  { key: "rays", label: "Сила лучей", min: 0, max: 2, step: 0.01, value: 1 },
  { key: "star", label: "Звёзды", min: 0, max: 2, step: 0.02, value: 1 },
  { key: "starSpd", label: "Скорость неба", min: 0, max: 3, step: 0.05, value: 1 },
  { key: "starSize", label: "Размер звёзд", min: 0.3, max: 2.5, step: 0.05, value: 1 },
  { key: "dust", label: "Пыль", min: 0, max: 2000, step: 50, value: 600 },
  { key: "colH", label: "Высота колонн", min: 3, max: 14, step: 0.1, value: 9 },
  { key: "colR", label: "Радиус колонн", min: 4, max: 12, step: 0.1, value: 7 },
  { key: "keyI", label: "Ключевой свет", min: 0, max: 200, step: 1, value: 60 },
  { key: "temp", label: "Тепло света", min: 0, max: 1, step: 0.01, value: 0.5 },
  { key: "fogD", label: "Глубина тумана", min: 0, max: 0.2, step: 0.002, value: 0.04 },
  { key: "grain", label: "Зерно", min: 0, max: 0.2, step: 0.005, value: 0.055 },
  { key: "bloom", label: "Bloom", min: 0, max: 2, step: 0.05, value: 0.85 },
  { key: "bloomT", label: "Порог Bloom", min: 0, max: 1, step: 0.01, value: 0.62 },
  { key: "dof", label: "Фокус (дистанция)", min: 2, max: 24, step: 0.5, value: 12 },
  { key: "dofR", label: "Глубина резкой зоны", min: 0.5, max: 14, step: 0.2, value: 5 },
  { key: "bokeh", label: "Сила размытия", min: 0, max: 4, step: 0.05, value: 0.9 },
  { key: "vig", label: "Виньетка", min: 0, max: 1, step: 0.02, value: 0.55 },
  { key: "domeO", label: "Яркость купола", min: 0, max: 1, step: 0.01, value: 0.28 },
  { key: "reflStr", label: "Сила отражения пола", min: 0, max: 12, step: 0.1, value: 2.6 },
  { key: "reflRes", label: "Разрешение отражения", min: 128, max: 1024, step: 128, value: 512 },
  { key: "altarZ", label: "Глубина алтаря", min: 1, max: 8, step: 0.1, value: 3.4 },
  { key: "apseH", label: "Высота задней стены", min: 0.5, max: 8, step: 0.1, value: 3.4 },
  {
    key: "bookT",
    label: "Время книги (с)",
    min: 0,
    max: 20,
    step: 0.1,
    value: 14.4,
    // Хронометраж: 3.2 парение, 7.6 посадка, 10.0 раскрыта,
    // 10.2–14.4 перелистывание, дальше покой
  },
];

// Слои целиком. Нужны, чтобы локализовать артефакт за секунду, а не за пересборку:
// на скриншоте полоса света шла через весь кадр, и гадать, чей это край, дороже
// чем переключить тумблер.
const TOGGLES = [
  { key: "beams", label: "Лучи-полосы (GodRays)" },
  { key: "dome", label: "Купол-свечение" },
  { key: "fan", label: "Веер рубашек" },
  { key: "floor", label: "Пол (reflector)" },
  { key: "cols", label: "Колонны" },
  { key: "apse", label: "Задняя стена-парапет" },
  { key: "altar", label: "Ступени и алтарь" },
  { key: "reflect", label: "Отражение пола" },
  { key: "candles", label: "Свечи" },
  { key: "post", label: "Пост-стек" },
  { key: "book", label: "Книга магии" },
  { key: "stars", label: "Звёзды в шейдере" },
];

/**
 * Состояние слоёв по умолчанию. Оно обязано совпадать с боевой сценой, иначе
 * стенд показывает одно, а главная другое — и правка проверяется не на том.
 * Боевая главная: веера нет (dimmed), рубашки на алтаре нет, ворон есть.
 */
const LAYERS: Record<string, number> = {
  beams: 1,
  dome: 1,
  fan: 0,
  stars: 1,
  floor: 1,
  cols: 1,
  apse: 1,
  altar: 1,
  reflect: 1,
  candles: 1,
  post: 1,
  book: 1,
};

export default function DesignLab() {
  const desktop = isFinePointer();
  // useState ДО раннего возврата: хуки должны вызываться в одном и том же
  // порядке при каждом рендере, а флаг окружения — константа времени сборки,
  // но правило всё равно проверяется статически и падает на мерге.
  const [vals, setVals] = useState<Record<string, number>>(() => ({
    ...Object.fromEntries(KNOBS.map((k) => [k.key, k.value])),
    ...LAYERS,
  }));

  // Время книги принимается из ?bookT= и кладётся обратно в адрес. Смысл не в
  // удобстве, а в проверяемости: анимация длится 15 секунд, и чтобы поймать
  // нужную фазу, раньше нужно было либо ловить момент, либо пересобирать
  // проект. Со ссылкой фаза открывается сразу и ею можно поделиться.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search).get("bookT");
    if (q === null) return;
    const t = Number(q);
    if (Number.isFinite(t)) setVals((v) => ({ ...v, bookT: Math.max(0, Math.min(20, t)) }));
  }, []);

  useEffect(() => {
    const url = new URL(window.location.href);
    url.searchParams.set("bookT", String(vals.bookT ?? 0));
    window.history.replaceState(null, "", url);
  }, [vals.bookT]);

  // Стенд — инструмент разработки, и в прод-сборке он закрыт. Открытая
  // страница со слайдерами 3D — это лишний вход для постороннего и лишний
  // чанк в графе загрузки. Включается флагом окружения, а не «всем подряд».
  if (process.env.NEXT_PUBLIC_DESIGN_LAB !== "1") {
    return (
      <main className="flex min-h-screen items-center justify-center bg-deep p-6 text-center text-paper">
        <div>
          <h1 className="text-lg font-semibold">Стенд закрыт</h1>
          <p className="mt-2 max-w-sm text-sm text-mist">
            Инструмент доступен только в среде разработки. Включается переменной
            окружения <code className="text-gold">NEXT_PUBLIC_DESIGN_LAB=1</code>.
          </p>
        </div>
      </main>
    );
  }

  return (
    // h-screen + overflow-hidden: страница не должна скроллиться, иначе сцена
    // уезжает из-под скриншота и артефакты невозможно локализовать. Скроллится
    // ТОЛЬКО панель параметров.
    <main className="fixed inset-0 z-50 flex overflow-hidden bg-deep text-paper">
      <div className="relative min-h-0 flex-1">
        {/* SceneInner САМ является <Canvas> (см. его конец). Оборачивать его в
            ещё один Canvas нельзя — R3F падает с «Canvas is not part of the
            THREE namespace». Поэтому стенд рендерит сцену напрямую; dpr и
            powerPreference берутся из настроек самой сцены. */}
        {/* Стенд показывает оба состояния боевой сцены: с веером — это вид
            расклада, без него — вид главной (вуаль + рубашка/ворон). Переключатель
            один: тумблер «Веер рубашек». */}
        <SceneInner lab={vals} dimmed={(vals.fan ?? 1) < 0.5} />
        <p className="pointer-events-none absolute left-3 top-3 z-10 rounded bg-black/50 px-2 py-1 text-xs">
          {desktop ? "pointer:fine — десктоп-профиль" : "touch — мобильный профиль"}
        </p>
      </div>

      <aside className="w-full shrink-0 overflow-y-auto border-l border-white/10 p-4 lg:w-80">
        <h1 className="mb-1 text-sm font-semibold uppercase tracking-wider text-gold">
          Стенд сцены
        </h1>
        <p className="mb-4 text-xs text-mist">
          Значения переносим в дефолты компонентов. Скопируй дамп через консоль:
          <code className="mt-1 block break-all text-[10px] text-violet">
            {JSON.stringify(vals)}
          </code>
        </p>
        {KNOBS.map((k) => (
          <label key={k.key} className="mb-3 block">
            <span className="flex justify-between text-xs text-mist">
              <span>{k.label}</span>
              <span className="font-mono text-goldsoft">{vals[k.key]}</span>
            </span>
            <input
              type="range"
              className="mt-1 w-full accent-gold"
              min={k.min}
              max={k.max}
              step={k.step}
              value={vals[k.key]}
              onChange={(e) => setVals((v) => ({ ...v, [k.key]: Number(e.target.value) }))}
            />
          </label>
        ))}
        <p className="mb-2 mt-5 text-xs uppercase tracking-wider text-mist">Слои</p>
        {TOGGLES.map((t) => (
          <label key={t.key} className="mb-2 flex items-center gap-2 text-xs text-paper">
            <input
              type="checkbox"
              className="accent-gold"
              checked={vals[t.key] !== 0}
              onChange={(e) => setVals((v) => ({ ...v, [t.key]: e.target.checked ? 1 : 0 }))}
            />
            {t.label}
          </label>
        ))}
        <button
          onClick={() =>
            setVals({
              ...Object.fromEntries(KNOBS.map((k) => [k.key, k.value])),
              ...LAYERS,
            })
          }
          className="mt-2 w-full rounded-2xl border border-white/15 px-3 py-2 text-xs hover:border-gold/50"
        >
          Сбросить к дефолтам
        </button>
      </aside>
    </main>
  );
}
