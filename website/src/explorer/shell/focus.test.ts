import { describe, expect, it, vi } from "vitest";
import { closesMenu, focusLeftMenu } from "./menu-keys";
import { pickOpener, refocus, rememberOpener } from "./restore-focus";
import { dismissesTip } from "../ui/help";

const element = (connected = true) => ({ focus: vi.fn(), isConnected: connected });

describe("pickOpener", () => {
  it("TestPickOpener_a_focused_element_is_remembered", () => {
    const button = element();
    expect(pickOpener(button, {})).toBe(button);
  });

  it("TestPickOpener_nothing_focused_or_the_page_itself_is_not_a_place_to_return_to", () => {
    const body = element();
    expect(pickOpener(null, body)).toBeNull();
    expect(pickOpener(body, body)).toBeNull();
  });
});

describe("rememberOpener", () => {
  it("TestRememberOpener_a_second_open_while_open_keeps_the_first_opener_not_the_dialogs_own_input", () => {
    const button = element();
    const paletteInput = element();
    const first = rememberOpener(null, button, {});
    expect(first).toBe(button);
    // Ctrl+K again: focus is in the palette's own input.
    expect(rememberOpener(first, paletteInput, {})).toBe(button);
  });

  it("TestRememberOpener_after_the_dialog_closed_the_next_opener_is_remembered", () => {
    const next = element();
    expect(rememberOpener(null, next, {})).toBe(next);
  });

  it("TestRememberOpener_nothing_focused_remembers_nothing", () => {
    const body = element();
    expect(rememberOpener(null, body, body)).toBeNull();
  });
});

describe("refocus", () => {
  it("TestRefocus_gives_focus_back_to_an_opener_that_is_still_on_the_page", () => {
    const button = element();
    expect(refocus(button)).toBe(true);
    expect(button.focus).toHaveBeenCalledTimes(1);
  });

  it("TestRefocus_an_opener_that_left_the_page_or_never_was_is_left_alone", () => {
    const gone = element(false);
    expect(refocus(gone)).toBe(false);
    expect(gone.focus).not.toHaveBeenCalled();
    expect(refocus(null)).toBe(false);
  });
});

describe("the Browse menu's keys", () => {
  it("TestClosesMenu_only_escape_closes", () => {
    expect(closesMenu("Escape")).toBe(true);
    for (const key of ["Enter", "Tab", "ArrowDown", " ", "Esc"]) expect(closesMenu(key)).toBe(false);
  });

  it("TestFocusLeftMenu_a_move_to_something_outside_closes_it", () => {
    const inside = {};
    const outside = {};
    const root = { contains: (n: unknown) => n === inside };
    expect(focusLeftMenu(root as never, outside as never)).toBe(true);
    expect(focusLeftMenu(root as never, inside as never)).toBe(false);
  });

  it("TestFocusLeftMenu_a_blur_with_no_destination_or_no_menu_leaves_it_open", () => {
    const root = { contains: () => false };
    expect(focusLeftMenu(root as never, null)).toBe(false);
    expect(focusLeftMenu(null, {} as never)).toBe(false);
  });
});

describe("the help tip", () => {
  it("TestDismissesTip_escape_hides_a_tip_without_moving_focus", () => {
    expect(dismissesTip("Escape")).toBe(true);
    expect(dismissesTip("Enter")).toBe(false);
  });
});
