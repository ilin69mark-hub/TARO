/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  experimental: {
    // 3D-сцена (three/R3F) подключается только lazy ssr:false — см. docs/project-book/05-design/08
    optimizePackageImports: ["framer-motion"],
  },
  // Аудит B: базовые заголовки (CSP держит будущий XSS; HSTS — только после TLS, см. E08).
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
          {
            key: "Content-Security-Policy",
            // NB: script 'unsafe-inline' обязателен для Next.js (иначе белый экран);
            // защита — object/frame/connect/img-ограничения + React-эскейп текстов.
            // Политика обязана посимвольно совпадать с deploy/nginx.conf и
            // deploy/nginx-tls.conf: edge отключает proxy_hide_header и отдаёт
            // свою копию, а X-Frame-Options не ставим — Telegram WebView
            // требует фрейминг, и XFO не умеет allow-list (см. A04/F-14).
            // Равенство проверяет ci.yml.
            value:
              "default-src 'self'; script-src 'self' 'unsafe-inline' https://telegram.org; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' https://eu.posthog.com; font-src 'self'; object-src 'none'; frame-ancestors 'self' https://web.telegram.org; base-uri 'self'",
          },
        ],
      },
    ];
  },
};

module.exports = nextConfig;
