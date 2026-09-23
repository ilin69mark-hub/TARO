"use client";

import { useEffect } from "react";
import { ensureAuth } from "@/lib/auth";
import { track, events } from "@/lib/analytics";

// Auth-bootstrap: anon uuid → cookie при первом заходе (см. 02-functional/02, T16).
// + visit-событие раз в сессию (см. 08-analytics-spec.md, D4).
export default function AuthBootstrap() {
  useEffect(() => {
    ensureAuth();
    try {
      if (!sessionStorage.getItem("taro_visit")) {
        sessionStorage.setItem("taro_visit", "1");
        track(events.visit, {});
      }
    } catch {
      /* ignore */
    }
  }, []);
  return null;
}
