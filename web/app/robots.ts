import { MetadataRoute } from "next";

// U17: robots — индексируем лендинг/каталог/карты; закрываем reading/history/admin/api.
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: "*",
        allow: ["/", "/spreads", "/cards/"],
        disallow: ["/reading/", "/history", "/profile", "/api/", "/admin"],
      },
    ],
  };
}
