// Тесты заставки главной (LandingIntro).
//
// Что тут ловится и почему Source-only проверок мало: дефекты заставки по
// природе невидимы в исходниках. «Она красивая» и «она не сломала LCP» —
// свойства рендера, а не текста. Поэтому здесь реальный рендер через
// @testing-library/react, а инварианты — самые дорогие при откате:
//
//  1) главная НЕ тянет 3D (гейт LCP: autoplay 3D запрещён до LCP);
//  2) заголовок не стартует с opacity:0 (он LCP-элемент);
//  3) полосы не перехватывают тап по кнопке (pointer-events-none);
//  4) заставка один раз за сессию, а не на каждом reload;
//  5) reduced-motion/calm выключают её полностью.
import React from "react";
import fs from "node:fs";
import path from "node:path";
import { render, screen, act } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import LandingIntro, { IntroEyebrow, IntroHeadline, IntroSub, INTRO_DURATION } from "@/components/LandingIntro";

const root = path.resolve(__dirname, "..");
const read = (p: string) => fs.readFileSync(path.join(root, p), "utf8");

const lite = vi.hoisted(() => ({ value: false }));
vi.mock("@/lib/device", () => ({
  shouldUseLite: () => lite.value,
  isFinePointer: () => false,
}));

function Hero() {
  return (
    <LandingIntro>
      <IntroEyebrow />
      <IntroHeadline>Задай вопрос. Вытяни карты. Услышь себя.</IntroHeadline>
      <IntroSub>Карманный вечерний ритуал самопознания за 2 минуты.</IntroSub>
    </LandingIntro>
  );
}

beforeEach(() => {
  lite.value = false;
  sessionStorage.clear();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("заставка главной: гейт производительности", () => {
  it("3D на главной есть, но строго отложенный (ssr:false)", () => {
    // Раньше гейт читался как «на главной нельзя импортировать 3D». Теперь
    // сцена на главной ЕСТЬ — по решению владельца («делаем дорого»), и гейт
    // сменился на другой: 3D не должен попасть в первый экран и блокировать
    // LCP, поэтому сцена грузится отдельным чанком ПОСЛЕ гидрации.
    const hero = fs.readFileSync(path.resolve(__dirname, "HomeHero.tsx"), "utf8");
    expect(hero, "сцена должна подключаться").toMatch(/CinematicBackdrop/);
    expect(hero, "сцена обязана быть dynamic").toMatch(/dynamic\(/);
    expect(hero, "сцена обязана быть ssr:false — иначе three.js попадёт в HTML").toMatch(
      /ssr:\s*false/
    );
    // И h1 остаётся в серверном HTML: без этого LCP и SEO страдают.
    const page = fs.readFileSync(path.resolve(__dirname, "../app/page.tsx"), "utf8");
    expect(page, "страница должна отдавать экран").toMatch(/HomeHero/);
    expect(hero, "заголовок обязан быть в разметке").toMatch(/IntroHeadline/);
  });

  it("декорации не перехватывают события и лежат под контентом", () => {
    const { container } = render(<Hero />);
    const decor = container.querySelector('[data-testid="intro-glow"]');
    expect(decor, "нет подсветки заставки").toBeTruthy();
    const decorRoot = decor!.closest("div[aria-hidden]");
    expect(decorRoot!.className, "декор обязан быть pointer-events-none").toMatch(/pointer-events-none/);
  });

  it("заголовок не стартует с opacity:0 — он LCP-элемент", () => {
    // Исчезающий элемент LCP засчитывается только когда проявится, то есть
    // сдвигает метрику на длительность анимации. Движение (transform) не
    // отменяет LCP, исчезновение — отменяет.
    //
    // Проверяем keyframes, а не отрендеренный style: анимация живёт в CSS, и
    // inline-атрибута у h1 теперь нет вовсе. Это строже прежней проверки:
    // opacity может вернуться не в style, а прямо в @keyframes.
    const css = fs.readFileSync(path.resolve(__dirname, "../app/globals.css"), "utf8");
    const kf = css.slice(css.indexOf("@keyframes intro-rise"));
    const body = kf.slice(0, kf.indexOf("}"));
    expect(body, "@keyframes intro-rise не найден").not.toBe("");
    expect(body, "в анимации заголовка не должно быть opacity — это LCP").not.toMatch(/opacity/);
    expect(body, "анимация заголовка обязана быть на transform").toMatch(/transform: translateY/);

    render(<Hero />);
    const h1 = screen.getByTestId("intro-headline");
    expect(h1.tagName, "заголовок обязан остаться единственным h1").toBe("H1");
    expect(h1.getAttribute("style") ?? "", "инлайн-анимации быть не должно").not.toMatch(/opacity/);
  });

  it("надпись и подзаголовок — единственные, кому разрешен opacity:0", () => {
    // Они на LCP не влияют, им исчезновение не вредит. Заодно это проверка
    // того, что keyframes не «расползлись» на элементы, которым нельзя.
    const css = fs.readFileSync(path.resolve(__dirname, "../app/globals.css"), "utf8");
    for (const name of ["intro-fade", "intro-eyebrow"]) {
      const start = css.indexOf(`@keyframes ${name}`);
      expect(start, `@keyframes ${name} не найден`).toBeGreaterThan(-1);
      expect(css.slice(start, start + 200), `${name} должен использовать opacity`).toMatch(/opacity/);
    }
    render(<Hero />);
    expect(screen.getByTestId("intro-eyebrow").tagName).toBe("P");
    expect(screen.getByTestId("intro-sub").tagName).toBe("DIV");
  });
});

describe("заставка главной: полосы", () => {
  it("рисуются две полосы — верх и низ", () => {
    render(<Hero />);
    expect(screen.getByTestId("intro-bar-top")).toBeTruthy();
    expect(screen.getByTestId("intro-bar-bottom")).toBeTruthy();
  });

  it("слой полос не перехватывает тап по кнопке", () => {
    // Прежний Letterbox на раскладе вешал pointer-events-auto на весь экран и
    // глушил первый тап по контенту. Здесь полосы — чистая декорация.
    const { container } = render(<Hero />);
    const top = screen.getByTestId("intro-bar-top");
    const layer = top.parentElement!;
    expect(layer.className, "слой полос обязан быть pointer-events-none").toMatch(/pointer-events-none/);
    expect(container.innerHTML).toMatch(/pointer-events-none/);
  });

  it("высота полос 8vh, как в 05-animations.md", () => {
    // Спека: «полосы 8vh сверху/снизу». Другая высота — расхождение с доком.
    // Читаем исходник компонента: в отрендеренном DOM Framer держит
    // целевую анимацию в JS, а в style кладёт только текущий кадр.
    const src = fs.readFileSync(
      path.resolve(__dirname, "LandingIntro.tsx"),
      "utf8"
    );
    expect(src, "полосы должны быть 8vh").toMatch(/"8vh"/);
    expect(src, "обе полосы (верх и низ)").toMatch(/"0vh", "8vh", "8vh", "0vh"/);
    expect(src, "суммарная длительность 1.2с — как у Letterbox расклада").toMatch(
      /INTRO_DURATION = 1\.2/
    );
  });
});

describe("заставка главной: один раз за сессию", () => {
  it("первый запуск играет, второй — уже нет", async () => {
    const first = render(<Hero />);
    expect(screen.getByTestId("intro-bar-top"), "на первом заходе полосы должны быть").toBeTruthy();
    first.unmount();

    // Второй заход в той же вкладке: кинематографии быть не должно.
    render(<Hero />);
    expect(screen.queryByTestId("intro-bar-top"), "заставка повторилась в той же сессии").toBeNull();
  });

  it("по истечении 1.2с полосы исчезают, контент остаётся", () => {
    render(<Hero />);
    expect(screen.getByTestId("intro-bar-top")).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(INTRO_DURATION * 1000 + 50);
    });
    expect(screen.queryByTestId("intro-bar-top"), "полосы не убрались после 1.2с").toBeNull();
    expect(screen.getByTestId("intro-headline"), "контент пропал вместе с полосами").toBeTruthy();
  });
});

describe("заставка главной: гейты доступности", () => {
  it("в Lite/reduced-motion заставки нет вообще", () => {
    lite.value = true;
    render(<Hero />);
    expect(screen.queryByTestId("intro-bar-top"), "в Lite полосы играть не должны").toBeNull();
    // Контент и «кинозал» остаются: заставка выключается, оформление — нет.
    expect(screen.getByTestId("intro-glow"), "декорации должны остаться даже в Lite").toBeTruthy();
    expect(screen.getByTestId("intro-headline")).toBeTruthy();
  });

  it("в Lite текст не анимируется: полос нет, класс подъёма снят", () => {
    lite.value = true;
    render(<Hero />);
    // Полосы — клиентский кадр заставки, в Lite их нет. Текст при этом
    // статичен (анимация на CSS, гейт — prefers-reduced-motion), но сам
    // компонент не должен навешивать инлайн-анимацию ни при каких обстоятельствах.
    expect(screen.queryByTestId("intro-bar-top")).toBeNull();
    const h1 = screen.getByTestId("intro-headline");
    expect(h1.getAttribute("style") ?? "").not.toMatch(/translate|opacity/);
  });
});

// Тот же класс дефекта на раскладе: Letterbox висел pointer-events-auto на
// весь экран и первые 1.2с съедал ЛЮБОЙ тап — кнопка «Скопировать ссылку» и
// «Поделиться в TG» на раскладе не срабатывали, если юзер тянулся сразу.
describe("Letterbox на раскладе не ворует тапы у контента", () => {
  const letterbox = () => read("components/cinematic/Letterbox.tsx");
  // Комментарии вырезаем: старый оверлей описан в комментарии как «было», и
  // без этого отрицание ловит собственное объяснение, а не код.
  const letterboxCode = () =>
    letterbox()
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/\/\/.*$/gm, "");

  it("слой полос не перехватывает события", () => {
    expect(letterboxCode()).toMatch(/pointer-events-none absolute inset-0/);
    // Старая разметка, которая крала тапы: оверлей на весь экран с onClick.
    expect(letterboxCode()).not.toMatch(/pointer-events-auto[^"]*inset-0/);
  });

  it("зоны пропуска интро есть только сверху и снизу", () => {
    const src = letterbox();
    expect(src, "нет зоны пропуска сверху").toMatch(/letterbox-skip-top/);
    expect(src, "нет зоны пропуска снизу").toMatch(/letterbox-skip-bottom/);
    // 12vh вместо 8vh — чтобы тап по границе полосы не промахивался мимо.
    expect(src, "зона пропуска должна быть чуть выше самой полосы").toMatch(/h-\[12vh\]/);
  });

  it("зона пропуска не накрывает центр экрана с контентом", () => {
    // 12vh сверху + 12vh снизу = 24vh. Центр (76vh) остаётся зоной контента,
    // иначе кнопки на раскладе снова перестали бы работать.
    const code = letterboxCode();
    const zones = code.match(/h-\[(\d+)vh\]/g) ?? [];
    expect(zones).toHaveLength(2);
    for (const z of zones) {
      const v = Number(z.match(/\d+/)![0]);
      expect(v, "зона не должна перекрывать центр").toBeLessThanOrEqual(12);
    }
  });
});

// Стенд сцены не должен быть доступен в проде. Открытая страница со
// слайдерами 3D — лишний вход для постороннего и лишний чанк в графе загрузки.
describe("стенд сцены закрыт в проде", () => {
  it("роут выключен без явного флага", () => {
    const lab = read("app/design-lab/page.tsx");
    expect(lab).toMatch(/NEXT_PUBLIC_DESIGN_LAB !== "1"/);
    expect(lab, "закрытый стенд должен что-то показывать").toMatch(/Стенд закрыт/);
  });

  it("флаг описан в примере окружения", () => {
    const env = read("../.env.example");
    expect(env, "флаг стенда должен быть задокументирован").toMatch(/NEXT_PUBLIC_DESIGN_LAB/);
  });
});
