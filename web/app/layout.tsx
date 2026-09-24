import type { Metadata, Viewport } from "next";
import { Cormorant_Garamond, Inter } from "next/font/google";
import "./globals.css";
import TabBar from "../components/TabBar";
import AuthBootstrap from "../components/AuthBootstrap";
import SWRegister, { InstallPrompt } from "../components/PWA";
import AgeGate from "../components/Legal";
import Onboarding from "../components/Onboarding";

// Типографика Книги (см. 05-design/03): Display Cormorant + Body Inter, кириллица.
const display = Cormorant_Garamond({
  subsets: ["cyrillic", "latin"],
  weight: ["500", "600"],
  style: ["normal", "italic"],
  variable: "--font-display",
  display: "swap",
});

const body = Inter({
  subsets: ["cyrillic", "latin"],
  weight: ["400", "500", "600"],
  variable: "--font-body",
  display: "swap",
});

export const viewport: Viewport = { themeColor: "#0B0B14" };

export const metadata: Metadata = {
  title: "Онлайн Таро — задай вопрос, услышь себя",
  description:
    "Самое красивое русскоязычное Таро с ИИ-толкованием, которое не пугает. Карта дня за 2 минуты.",
  manifest: "/manifest.json",
  appleWebApp: { capable: true, statusBarStyle: "black-translucent", title: "Таро" },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ru" className={`${display.variable} ${body.variable}`}>
      <body style={{ fontFamily: "var(--font-body), system-ui, sans-serif" }}>
        <AuthBootstrap />
        <SWRegister />
        <AgeGate />
        <Onboarding />
        <div className="pb-20">{children}</div>
        <footer className="mx-auto max-w-md px-4 pb-24 text-center text-xs text-mist">
          Онлайн Таро — инструмент самопознания и рефлексии. Не является медицинской,
          психологической, юридической или финансовой услугой. Решения принимаете вы. 18+
        </footer>
        <InstallPrompt />
        <TabBar />
      </body>
    </html>
  );
}
