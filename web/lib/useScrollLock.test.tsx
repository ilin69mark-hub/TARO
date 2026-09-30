// Блокировка скролла под модальным окном.
//
// Проверяем в jsdom на реальном document.body: баг, который это чинит, —
// страница прокручивается под открытым окном (колесо/свайп), и это выглядит
// как сбой, хотя вёрстка исправна.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { useScrollLock } from "./useScrollLock";

let host: HTMLDivElement | null = null;
let root: ReturnType<typeof createRoot> | null = null;

function mount(active: boolean) {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  function Probe() {
    useScrollLock(active);
    return null;
  }
  act(() => root!.render(<Probe />));
}

afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove();
  host = null;
  root = null;
  document.body.style.overflow = "";
  document.body.style.paddingRight = "";
});

describe("useScrollLock", () => {
  it("блокирует скролл страницы, пока окно открыто", () => {
    document.body.style.overflow = "";
    mount(true);
    expect(document.body.style.overflow).toBe("hidden");
  });

  it("возвращает скролл при размонтировании", () => {
    mount(true);
    act(() => root!.unmount());
    root = null;
    expect(document.body.style.overflow).toBe("");
  });

  it("не трогает страницу, если блокировка не нужна", () => {
    document.body.style.overflow = "";
    mount(false);
    expect(document.body.style.overflow).toBe("");
  });

  it("компенсирует ширину скроллбара, чтобы содержимое не дёрнулось", () => {
    // Скроллбар исчезает при overflow:hidden — без padding-right страница
    // сдвигается вбок на его ширину.
    const clientWidth = 1000;
    Object.defineProperty(document.documentElement, "clientWidth", {
      configurable: true,
      value: clientWidth,
    });
    window.innerWidth = clientWidth + 15;

    mount(true);
    expect(document.body.style.paddingRight).toBe("15px");

    act(() => root!.unmount());
    root = null;
    expect(document.body.style.paddingRight).toBe("");
  });

  it("не добавляет отступ, когда скроллбара нет (мобильные)", () => {
    Object.defineProperty(document.documentElement, "clientWidth", {
      configurable: true,
      value: window.innerWidth,
    });
    mount(true);
    expect(document.body.style.paddingRight).toBe("");
  });

  it("возвращает ровно то, что было, если блокировка уже стояла", () => {
    // Вложенные окна: если сбросить в '', после закрытия верхнего нижнее
    // останется без защиты.
    document.body.style.overflow = "hidden";
    mount(true);
    act(() => root!.unmount());
    root = null;
    expect(document.body.style.overflow).toBe("hidden");
  });
});
