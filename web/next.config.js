/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  experimental: {
    // 3D-сцена (three/R3F) подключается только lazy ssr:false — см. docs/project-book/05-design/08
    optimizePackageImports: ["framer-motion"],
  },
};

module.exports = nextConfig;
