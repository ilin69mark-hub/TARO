"use client";

import { useEffect } from "react";

// Блокировка скролла страницы под модальным окном.
//
// Модалка `fixed inset-0` закрывает экран, но НЕ мешает прокручивать то, что
// под ней: колесо мыши и свайп двигают страницу, и пользователь уезжает с
// открытым окном оформления. Это выглядит как сбой, хотя вёрстка исправна.
//
// Ширину скроллбара компенсируем padding-right, иначе при появлении окна
// содержимое дёргается вбок на 15px — заметный скачок, особенно на десктопе.
//
// Возвращаем ровно то, что было: если у body уже стоял overflow: hidden
// (например, другое окно открыто выше), не сбрасываем его на '', иначе после
// закрытия верхнего окна нижнее останется без защиты.
export function useScrollLock(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const body = document.body;
    const previousOverflow = body.style.overflow;
    const previousPadding = body.style.paddingRight;
    // Классика: скроллбар исчезает при overflow hidden, ширину узнаём заранее.
    const scrollbar = window.innerWidth - document.documentElement.clientWidth;
    body.style.overflow = "hidden";
    if (scrollbar > 0) body.style.paddingRight = `${scrollbar}px`;
    return () => {
      body.style.overflow = previousOverflow;
      body.style.paddingRight = previousPadding;
    };
  }, [active]);
}
