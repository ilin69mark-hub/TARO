// Гейты Lite vs 3D (см. 05-design/08-ultra-cinematic.md, 09-premium-temple.md).
// Lite: reduced-motion, спокойный режим, deviceMemory<4, CPU<=4.
export function shouldUseLite(): boolean {
  if (typeof window === "undefined" || typeof navigator === "undefined") return true;
  try {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return true;
    if (localStorage.getItem("taro_calm") === "1") return true;
    const mem = (navigator as unknown as { deviceMemory?: number }).deviceMemory;
    if (mem && mem < 4) return true;
    if (navigator.hardwareConcurrency && navigator.hardwareConcurrency <= 4) return true;
  } catch {
    return true;
  }
  return false;
}

// isFinePointer — gyro/tilt только desktop pointer:fine (см. T22/T27).
export function isFinePointer(): boolean {
  if (typeof window === "undefined") return false;
  return window.matchMedia("(pointer: fine)").matches;
}
