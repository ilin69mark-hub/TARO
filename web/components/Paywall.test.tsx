// Unit: paywall-сортировка U26 + стрик-бейдж (см. D-покрытие).
import React from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import PaywallSheet, { Plan } from "@/components/PaywallSheet";
import StreakBadge from "@/components/StreakBadge";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn(async () => ({ days: 0 })) }));
vi.mock("@/lib/api", async (orig) => {
  const mod = await orig<typeof import("@/lib/api")>();
  return { ...mod, api: { ...mod.api, get: getMock } };
});

const PLANS: Plan[] = [
  { code: "month_299", price_rub: 299, stars_amount: 199, duration_days: 30 },
  { code: "single_99", price_rub: 99, stars_amount: 66, duration_days: null },
];

function ls(hits: number) {
  const store: Record<string, string> = { taro_paywalls: String(hits) };
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store[k] ?? null,
    setItem: (k: string, v: string) => {
      store[k] = v;
    },
    removeItem: (k: string) => {
      delete store[k];
    },
    clear: () => {},
  } as Storage);
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("PaywallSheet", () => {
  it("single_99 первым при 3+ упорах (U26)", () => {
    ls(5);
    const { container } = render(<PaywallSheet plans={PLANS} onClose={() => undefined} />);
    const first = container.querySelector("li p")?.textContent;
    expect(first).toContain("Разовый");
  });
  it("обычный порядок при <3", () => {
    ls(0);
    const { container } = render(<PaywallSheet plans={PLANS} onClose={() => undefined} />);
    const first = container.querySelector("li p")?.textContent;
    expect(first).toContain("Безлимит");
  });
});

describe("StreakBadge", () => {
  it("прячется при days<2", async () => {
    ls(0);
    getMock.mockResolvedValueOnce({ days: 1 });
    const { container } = render(<StreakBadge />);
    await screen.findByText(() => false).catch(() => undefined);
    expect(container.textContent).toBe("");
  });
  it("показывает плюрализацию", async () => {
    ls(0);
    getMock.mockResolvedValueOnce({ days: 5 });
    render(<StreakBadge />);
    await screen.findByLabelText("Стрик 5 дней");
  });
});
