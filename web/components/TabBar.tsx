"use client";

// Таб-бар снизу — см. docs/project-book/05-design/04-components.md
// 4 иконки SVG + active gold + dot + blur.
import Link from "next/link";
import { usePathname } from "next/navigation";

function Icon({ d }: { d: string }) {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" aria-hidden
      stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round">
      <path d={d} />
    </svg>
  );
}

const TABS = [
  { href: "/", label: "Главная", d: "M3 11l9-8 9 8v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" },
  { href: "/spreads", label: "Расклады", d: "M6 3h9l4 4v14H6z M6 3v5h5 M14 17a3 3 0 1 0 0-6 3 3 0 0 0 0 6z" },
  { href: "/history", label: "История", d: "M3 12a9 9 0 1 0 3-6.7 M3 4v5h5 M12 7v5l3 3" },
  { href: "/profile", label: "Профиль", d: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8z M4 21c0-4 4-6 8-6s8 2 8 6" },
];

export default function TabBar() {
  const pathname = usePathname();
  return (
    <nav
      aria-label="Основная навигация"
      className="fixed inset-x-0 bottom-0 z-10 border-t border-white/10 bg-card/80 backdrop-blur-xl"
    >
      <ul className="mx-auto grid max-w-md grid-cols-4">
        {TABS.map((tab) => {
          const active =
            tab.href === "/" ? pathname === "/" : pathname.startsWith(tab.href);
          return (
            <li key={tab.href}>
              <Link
                href={tab.href}
                aria-current={active ? "page" : undefined}
                className={`flex flex-col items-center gap-1 py-2 text-xs font-semibold uppercase tracking-wider ${
                  active ? "text-gold" : "text-mist"
                }`}
              >
                <Icon d={tab.d} />
                {tab.label}
                {active && <span className="h-1 w-1 rounded-full bg-gold" />}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
