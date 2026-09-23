"use client";

import { useEffect } from "react";
import Lenis from "lenis";

// Плавный скролл лендинга (см. 08-ultra-cinematic.md, D7).
// Только лендинг, только без reduced-motion/calm.
export default function SmoothScroll() {
  useEffect(() => {
    try {
      if (
        window.matchMedia("(prefers-reduced-motion: reduce)").matches ||
        localStorage.getItem("taro_calm") === "1"
      ) {
        return;
      }
    } catch {
      return;
    }
    const lenis = new Lenis({ duration: 1.1 });
    let raf = 0;
    function loop(time: number) {
      lenis.raf(time);
      raf = requestAnimationFrame(loop);
    }
    raf = requestAnimationFrame(loop);
    return () => {
      cancelAnimationFrame(raf);
      lenis.destroy();
    };
  }, []);
  return null;
}
