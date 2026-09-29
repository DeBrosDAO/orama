import type { World } from "./world";

/**
 * The newest block that is final at `nowMs`. The block in progress is not:
 * more transactions can still land in it, so it is never shown as the head.
 */
export const finalizedHeight = (world: World, nowMs: number): number => world.heightAt(nowMs) - 1;

/**
 * Bring the world up to `nowMs` without generating anything for the block in
 * progress. Afterwards every transaction in the world is in a final block, so
 * no query can show one that is still changing.
 */
export function settle(world: World, nowMs: number): void {
  world.advanceThroughHeight(finalizedHeight(world, nowMs));
}
