// SW T19 (см. 03-nonfunctional/06-mobile-pwa.md): кэш shell + spreads 24ч,
// карты — lazy on-demand (CacheFirst). Полная стратегия 22+shell — после артов T24.
// Версия КЭША: bump при каждом релизе.
const VERSION = "taro-v1";
const SHELL = ["/", "/spreads", "/manifest.json", "/icon-192.png"];

self.addEventListener("install", (e) => {
  e.waitUntil(
    caches.open(VERSION).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET") return;
  // spreads API — stale-while-revalidate 24ч
  if (url.pathname === "/api/spreads") {
    e.respondWith(
      caches.open(VERSION).then(async (c) => {
        const cached = await c.match(e.request);
        const fresh = fetch(e.request)
          .then((r) => {
            if (r.ok) c.put(e.request, r.clone());
            return r;
          })
          .catch(() => cached);
        return cached || fresh;
      })
    );
    return;
  }
  // арты карт — CacheFirst on-demand (см. T24: 22 majors + shell precache)
  if (url.pathname.startsWith("/cards/")) {
    e.respondWith(
      caches.open(VERSION).then(async (c) => {
        const cached = await c.match(e.request);
        if (cached) return cached;
        const res = await fetch(e.request);
        if (res.ok) c.put(e.request, res.clone());
        return res;
      })
    );
    return;
  }
  // shell — network first, fallback cache (offline-значения без AI, см. T19)
  if (e.request.mode === "navigate") {
    e.respondWith(fetch(e.request).catch(() => caches.match("/")));
  }
});
