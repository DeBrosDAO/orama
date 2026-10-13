/** Whether this key closes an open menu. */
export function closesMenu(key: string): boolean {
  return key === "Escape";
}

/**
 * Whether focus has moved out of a menu to something that is not part of it. A blur with no destination
 * (the window lost focus, a click landed on text) leaves the menu open: the pointer handler covers clicks.
 */
export function focusLeftMenu(root: { contains(node: Node | null): boolean } | null, next: Node | null): boolean {
  return root !== null && next !== null && !root.contains(next);
}
