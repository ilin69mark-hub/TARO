import HomeHero from "@/components/HomeHero";

// Лендинг — mobile-first 360px (см. 03-nonfunctional/06, 05-design/04).
// U18: JSON-LD WebApplication (см. 03-nonfunctional/05).
//
// Сцена, заставка и контент вынесены в HomeHero: их три слоя (3D / «кино-зал» /
// текст) не помещаются в серверную страницу, а здесь остаётся только разметка
// документа, которую видят поисковики.
const JSON_LD = {
  "@context": "https://schema.org",
  "@type": "WebApplication",
  name: "Онлайн Таро",
  applicationCategory: "LifestyleApplication",
  operatingSystem: "Web",
  inLanguage: "ru",
  offers: { "@type": "Offer", price: "0", priceCurrency: "RUB" },
};

export default function Home() {
  return (
    <>
      {/* JSON-LD только из статической константы; user/AI-текст — никогда (аудит B).
          </ экранирован: иначе будущая переменная даст stored-XSS в script-контексте. */}
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(JSON_LD).replace(/</g, "\\u003c") }}
      />
      <HomeHero />
    </>
  );
}
