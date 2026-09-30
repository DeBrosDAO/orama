import { useEffect } from "react";
import { useNavigate } from "react-router";
import { explorerPaths } from "../../model/routes";
import { blockKeyTarget } from "./block-nav";
import type { BlockNeighbours } from "./block-nav";

/** ← and → open the previous and next block, unless the reader is typing or using a shortcut. */
export function useBlockKeys(nav: BlockNeighbours): void {
  const navigate = useNavigate();
  const { prev, next } = nav;
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const height = blockKeyTarget(
        {
          key: e.key,
          ctrlKey: e.ctrlKey,
          metaKey: e.metaKey,
          altKey: e.altKey,
          shiftKey: e.shiftKey,
          target: e.target,
        },
        { prev, next },
      );
      if (height === null) return;
      e.preventDefault();
      navigate(explorerPaths.block(height));
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [prev, next, navigate]);
}
