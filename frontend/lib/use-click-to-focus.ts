"use client";

import { useCallback, useEffect, useRef, useState } from "react";

// useClickToFocus implements the "click-to-interact" pattern used for
// embedded diagrams: the surface ignores wheel/scroll events until the
// user clicks inside it, and releases focus when the user clicks
// elsewhere on the page. Callers attach `ref` to the container, call
// `focus` from its onPointerDown, and gate scroll-driven behaviour
// (e.g. ReactFlow's zoomOnScroll) on `focused`.
export function useClickToFocus<T extends HTMLElement = HTMLDivElement>() {
  const ref = useRef<T | null>(null);
  const [focused, setFocused] = useState(false);

  useEffect(() => {
    if (!focused) return;
    const onDocPointerDown = (e: PointerEvent) => {
      const el = ref.current;
      if (!el) return;
      if (e.target instanceof Node && !el.contains(e.target)) {
        setFocused(false);
      }
    };
    document.addEventListener("pointerdown", onDocPointerDown, true);
    return () =>
      document.removeEventListener("pointerdown", onDocPointerDown, true);
  }, [focused]);

  const focus = useCallback(() => setFocused(true), []);

  return { ref, focused, focus };
}
