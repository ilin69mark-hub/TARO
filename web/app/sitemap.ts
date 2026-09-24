import { MetadataRoute } from "next";

// U17: sitemap — лендинг + каталог + 78 карт. Закрыты: reading/history/admin (см. 03-nonfunctional/05).
// Аудит B: без BASE_URL падаем на build, а не травим прод картой на taro.local.
const base = process.env.NEXT_PUBLIC_BASE_URL;
if (!base && process.env.NODE_ENV === "production") {
  throw new Error("NEXT_PUBLIC_BASE_URL required");
}
export default function sitemap(): MetadataRoute.Sitemap {
  const origin = base || "https://taro.local";
  const cards = Array.from({ length: 78 }, (_, i) => ({
    url: `${origin}/cards/${i}`,
    lastModified: new Date(),
  }));
  return [
    { url: origin, lastModified: new Date() },
    { url: `${origin}/spreads`, lastModified: new Date() },
    ...cards,
  ];
}
