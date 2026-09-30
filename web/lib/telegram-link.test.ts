// Привязка Telegram: клиентская логика и приоритет над оплатой.
//
// Здесь ловится то, что тихо ломает деньги пользователя:
//
//   1) Кнопка показана ВНЕ Telegram, где initData нет. Человек нажимает,
//      получает 422, и приглашение превращается в шум. Значит кнопка вне
//      Telegram не рендерится вообще.
//   2) Привязка показана человеку, который уже привязан — тоже шум.
//   3) Баннер в layout показан анониму БЕЗ оплаты. Тогда это постоянная
//      плашка, которую закрывают не глядя, и она перестаёт работать.
//   4) Привязка не перечитывает профиль: сервер перевыпустил cookie, а на
//      экране остались данные старого аккаунта.
//
// Тесты читают исходники компонентов: логика — в условиях рендера, которые
// без браузера не проверить, а инструкция проекта требует, чтобы такие
// правила были зафиксированы числами, а не «посмотри глазами».
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const read = (p: string) => fs.readFileSync(path.resolve(__dirname, "..", p), "utf8");

const card = read("components/TelegramLinkCard.tsx");
const banner = read("components/TelegramLinkBanner.tsx");
const me = read("lib/me.ts");
const paywall = read("components/PaywallSheet.tsx");
const profile = read("app/profile/page.tsx");
const layout = read("app/layout.tsx");

describe("Привязка возможна только внутри Telegram", () => {
  it("initData читается из Telegram WebApp", () => {
    expect(me).toMatch(/Telegram\?\.WebApp/);
    expect(me).toMatch(/initData/);
  });

  it("вне Telegram linkTelegram не ходит на сервер, а объясняет", () => {
    // Ранний выход: без initData сервер вернёт 422, и человек увидит ошибку
    // вместо причины. Плюс лишний запрос с заведомо бесполезным телом.
    expect(me).toMatch(/code: "NOT_IN_TELEGRAM"/);
    expect(me).toMatch(/только внутри Telegram/);
  });

  it("кнопка привязки не рендерится вне Telegram", () => {
    // Не «disabled», а не рендерится: человек не должен упираться в стену.
    expect(card).toMatch(/if \(typeof window !== "undefined" && !\(window as unknown/);
  });
});

describe("Условия показа", () => {
  it("привязанному Telegram карточка не показывается", () => {
    expect(card).toMatch(/if \(!me \|\| me\.telegram_linked \|\| done\) return null/);
  });

  it("пока не загрузился профиль — не мигает", () => {
    expect(card).toMatch(/!me \|\| me\.telegram_linked/);
  });

  it("баннер в layout — только для анонима, который платил", () => {
    // Ровно это условие. Без has_payment плашка висит над анонимом, который
    // ничего не покупал, и он её закрывает не глядя.
    expect(banner).toMatch(/me\.telegram_linked \|\| !me\.has_payment/);
    expect(banner).toMatch(/me\.telegram_linked \|\| !me\.has_payment/);
  });

  it("баннер не докучает повторно", () => {
    expect(banner).toMatch(/taro_tg_banner_dismissed/);
  });
});

describe("Привязка стоит ДО оплаты", () => {
  it("карточка в paywall выше списка тарифов", () => {
    const link = paywall.indexOf("<TelegramLinkCard");
    const list = paywall.indexOf("{ordered.map(");
    expect(link).toBeGreaterThan(-1);
    expect(list).toBeGreaterThan(-1);
    // Приглашение должно идти раньше кнопок «Оплатить»: после оплаты
    // подписка уже висит на анонимной строке.
    expect(link).toBeLessThan(list);
  });

  it("и в профиле карточка есть", () => {
    expect(profile).toMatch(/<TelegramLinkCard/);
  });
});

describe("После привязки состояние перечитывается", () => {
  it("профиль и квоты запрашиваются заново", () => {
    // Сервер после мержа перевыпускает cookie. Без перезапроса на экране
    // остались бы free_left и valid_until старого аккаунта.
    expect(profile).toMatch(/onLinked/);
    expect(profile).toMatch(/entitlements\/me/);
    expect(banner).toMatch(/reload/);
  });

  it("ALREADY_LINKED считается успехом, а не ошибкой", () => {
    // Человек уже привязан: показывать ему ошибку нельзя, это регресс UX.
    expect(me).toMatch(/ALREADY_LINKED"\) return \{ ok: true/);
  });
});

describe("Идентификатор виден человеку", () => {
  it("user_id выводится в профиле", () => {
    expect(profile).toMatch(/me\.user_id/);
  });

  it("профиль берётся из /me, а не собирается на клиенте", () => {
    expect(me).toMatch(/api\.get<Me>\("\/me"\)/);
  });
});

// Человек, который заплатил и пришёл в другой браузер.
//
// Замеряно на стенде: после оплаты month_299 новый браузер получает
// plan=free и has_payment=false, то есть видит paywall, хотя деньги его.
// Определить такого человека нечем — у анонимной сессии в новом браузере нет
// ни cookie, ни localStorage. Поэтому единственная честная форма подсказки —
// сказать «если покупка была, она в Telegram» всем анонимам вне Telegram.
//
// Тест на исходнике, а не на поведении: условие показа — это «не платил в
// ЭТОЙ сессии и не внутри Telegram», и без браузера оно не проверяется.
describe("Ранее оплативший в другом браузере", () => {
  it("строка «уже покупали» есть и говорит, что покупка в Telegram", () => {
    expect(card).toMatch(/Уже покупали\?/);
    // Формулировка обязана сообщать, где искать, а не предлагать купить заново.
    expect(card).toMatch(/доступ был в Telegram/);
  });

  it("показывается только вне Telegram", () => {
    // Внутри Telegram подсказка ложна: там человек и так в своём аккаунте.
    expect(card).toMatch(/!paid && !insideTelegram\(\)/);
  });

  it("не показывается тому, кто платил в этой сессии", () => {
    // Плательщику этого браузера нужна кнопка переноса, а не «может, вы
    // где-то платили». Два разных сообщения для двух разных ситуаций.
    expect(card).toMatch(/!paid/);
  });

  it("живёт рядом с кнопкой в Telegram, а не отдельным баннером", () => {
    // Отдельная плашка на весь сайт превратилась бы в шум: появление у всех
    // анонимов означало бы, что её перестали читать (см. правило про баннер).
    expect(card).toMatch(/data-testid="tg-already-paid"/);
    const idx = card.indexOf('data-testid="tg-already-paid"');
    const link = card.indexOf("Открыть в Telegram");
    expect(idx).toBeGreaterThan(-1);
    expect(link).toBeGreaterThan(-1);
  });
});
