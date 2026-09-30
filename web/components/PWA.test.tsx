// Кнопка «Установить приложение» была fixed поверх таб-бара и закрывала текст
// расклада — то есть контент, ради которого пользователь пришёл. Владелец
// увидел это на стенде и попросил убрать в меню.
//
// Дефект был молчаливым по двум причинам:
//   1) кнопка рендерилась только после 2-го расклада, то есть в деф-ветке
//      (eligible=false) её не видно и регресс не воспроизводится;
//   2) без beforeinstallprompt в тестовом браузере она не рендерилась вообще.
//
// Поэтому проверяем не «видна ли кнопка», а инварианты: в layout её больше нет,
// в профиле пункт есть, и логика счётчика не потерялась.
import React from "react";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const read = (p: string) => fs.readFileSync(path.join(root, p), "utf8");

describe("install prompt lives in the profile, not over the content", () => {
  it("общий layout больше не рендерит плавающую кнопку", () => {
    const layout = read("app/layout.tsx");
    expect(layout).not.toMatch(/InstallPrompt/);
    // Кнопка не должна появляться и через косвенный импорт/вызов.
    expect(layout).not.toMatch(/beforeinstallprompt/);
  });

  it("в layout осталась только регистрация сервис-воркера", () => {
    expect(read("app/layout.tsx")).toMatch(/SWRegister/);
  });

  it("нет fixed-кнопки установки ни в одном компоненте", () => {
    const files = [
      "components/PWA.tsx",
      "components/TabBar.tsx",
      "app/layout.tsx",
      "app/profile/page.tsx",
    ];
    for (const f of files) {
      const src = read(f);
      expect(src, `${f} вернул плавающую кнопку`).not.toMatch(
        /fixed[^"']*bottom-\d+[^"']*Установить/
      );
    }
  });

  it("профиль содержит пункт установки и подпись про офлайн", () => {
    const profile = read("app/profile/page.tsx");
    expect(profile).toMatch(/useInstallPrompt/);
    expect(profile).toMatch(/Установить/);
    // Пользователь должен понимать, что кнопка вообще бывает недоступна,
    // иначе пустой блок выглядит как ошибка вёрстки.
    expect(profile).toMatch(/Установка недоступна/);
  });

  it("хук ловит beforeinstallprompt и уважает порог 2-го расклада", () => {
    const pwa = read("components/PWA.tsx");
    expect(pwa).toMatch(/beforeinstallprompt/);
    expect(pwa).toMatch(/taro_readings/);
    expect(pwa).toMatch(/>=\s*2/);
  });

  it("счётчик раскладов и bump не потеряны при переносе", () => {
    const pwa = read("components/PWA.tsx");
    expect(pwa).toMatch(/export function bumpReadingCount/);
    expect(pwa).toMatch(/taro_readings/);
  });
});

// Компонентный уровень: хук возвращает canInstall/eligible независимо, чтобы
// отладка не выглядела как «кнопка сломалась». Рендерим через TestRenderer-подобный
// вызов хука в реальном React-дереве не нужен — здесь проверяем форму контракта.
describe("useInstallPrompt contract", () => {
  it("разделяет «браузер умеет» и «пользователь наиграл»", () => {
    const pwa = read("components/PWA.tsx");
    expect(pwa).toMatch(/canInstall/);
    expect(pwa).toMatch(/eligible/);
    expect(pwa).toMatch(/promptInstall/);
    // canInstall не должен зависеть от счётчика раскладов.
    expect(pwa).not.toMatch(/canInstall:\s*deferred !== null && eligible/);
  });
});
