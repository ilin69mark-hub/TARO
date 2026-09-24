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
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
          {
            key: "Content-Security-Policy",
            // NB: script 'unsafe-inline' обязателен для Next.js (иначе белый экран);
            // защита — object/frame/connect/img-ограничения + React-эскейп текстов.
            value:
              "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' https://eu.posthog.com; font-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'",
          },
        ],
      },
    ];
  },
};

module.exports = nextConfig;
