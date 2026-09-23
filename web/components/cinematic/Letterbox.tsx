"use client";

// Letterbox-интро 1.2с, skippable тапом (см. 05-animations.md).
// Только при активной 3D-сцене (CinematicBackdrop рендерит вне Lite).
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";

export default function Letterbox() {
  const [gone, setGone] = useState(false);
  return (
    <AnimatePresence>
      {!gone && (
        <motion.div
          aria-hidden
          className="pointer-events-auto absolute inset-0 z-10"
          onClick={() => setGone(true)}
          exit={{ opacity: 0 }}
        >
          <motion.div
            className="absolute inset-x-0 top-0 bg-black"
            initial={{ height: 0 }}
            animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
            transition={{ duration: 1.2, times: [0, 0.25, 0.75, 1], ease: [0.22, 1, 0.36, 1] }}
            onAnimationComplete={() => setGone(true)}
          />
          <motion.div
            className="absolute inset-x-0 bottom-0 bg-black"
            initial={{ height: 0 }}
            animate={{ height: ["0vh", "8vh", "8vh", "0vh"] }}
            transition={{ duration: 1.2, times: [0, 0.25, 0.75, 1], ease: [0.22, 1, 0.36, 1] }}
          />
        </motion.div>
      )}
    </AnimatePresence>
  );
}
