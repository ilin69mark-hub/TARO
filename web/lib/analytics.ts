// Аналитика PostHog (см. 04-architecture/08, D4): lazy-init при NEXT_PUBLIC_POSTHOG_KEY,
// иначе no-op (dev тихий). PII (вопрос/толкование) в пропсах запрещены — ловит CI-grep (см. D4).
import posthog from "posthog-js";

type Props = Record<string, string | number | boolean | undefined>;

const KEY = process.env.NEXT_PUBLIC_POSTHOG_KEY;

let started = false;

function ensure(): boolean {
  if (!KEY || typeof window === "undefined") return false;
  if (!started) {
    posthog.init(KEY, {
      api_host: "https://eu.posthog.com",
      capture_pageview: false,
      persistence: "localStorage",
    });
    started = true;
  }
  return true;
}

export function track(event: string, props: Props = {}): void {
  if (!ensure()) return;
  try {
    posthog.capture(event, { ...props, anon_id: anonId() });
  } catch {
    /* аналитика никогда не ломает продукт */
  }
}

function anonId(): string {
  try {
    return localStorage.getItem("taro_uuid") || "unknown";
  } catch {
    return "unknown";
  }
}

export const events = {
  visit: "visit",
  spreadOpen: "spread_open",
  readingDone: "reading_done",
  paywallShow: "paywall_show",
  paySuccess: "pay_success",
  trialStart: "trial_start",
  deleteMe: "delete_me",
  shareDone: "share_done", // U14: шеры (конверсия в регистрации — по рефереру ссылки)
} as const;
