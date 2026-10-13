import { useCallback, useRef } from "react";

/** What of an element focus needs: to be asked, and to know whether it is still on the page. */
export interface Focusable {
  focus(): void;
  readonly isConnected: boolean;
}

/** The element that had focus as a dialog opened, if there was one: the page itself (the body) is no place to return to. */
export function pickOpener<T extends Focusable>(active: T | null, body: unknown): T | null {
  return active !== null && active !== body ? active : null;
}

/**
 * What to remember as the opener when a dialog is asked to open: the one already remembered if the
 * dialog is open (a second shortcut press arrives with focus in the dialog's own input, which is no
 * place to return to), else whatever has focus now.
 */
export function rememberOpener<T extends Focusable>(remembered: T | null, active: T | null, body: unknown): T | null {
  return remembered ?? pickOpener(active, body);
}

/** Gives focus back to the opener; false when there is none or it has left the page (a link that navigated away). */
export function refocus(opener: Focusable | null): boolean {
  if (opener === null || !opener.isConnected) return false;
  opener.focus();
  return true;
}

/**
 * Where focus goes when a dialog that has no trigger of its own closes. Radix returns focus to the
 * dialog's trigger; the palette and the transaction preview are opened by a shortcut or by a button
 * that is not a Dialog.Trigger, so without this focus falls to the page and a keyboard reader starts
 * again from the top. `remember` is called as the dialog opens and notes the element that had focus;
 * `onCloseAutoFocus` is the dialog content's handler and gives focus back to it.
 */
export function useRestoreFocus() {
  const opener = useRef<HTMLElement | null>(null);
  const remember = useCallback(() => {
    const active = document.activeElement;
    opener.current = rememberOpener(opener.current, active instanceof HTMLElement ? active : null, document.body);
  }, []);
  const onCloseAutoFocus = useCallback((event: Event) => {
    event.preventDefault();
    refocus(opener.current);
    opener.current = null;
  }, []);
  return { remember, onCloseAutoFocus };
}
