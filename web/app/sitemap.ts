import { MetadataRoute } from "next";

// U17: sitemap — лендинг + каталог + 78 карт. Закрыты: reading/history/admin (см. 03-nonfunctional/05).
export default function sitemap(): MetadataRoute.Sitemap {
  const base = process.env.NEXT_PUBLIC_BASE_URL || "https://taro.local";
  const cards = Array.from({ length: 78 }, (_, i) => ({
    url: `${base}/cards/${i}`,
    lastModified: new Date(),
  }));
  return [
    { url: base, lastModified: new Date() },
    { url: `${base}/spreads`, lastModified: new Date() },
    ...cards,
  ];
}
