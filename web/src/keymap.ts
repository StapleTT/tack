// KeyboardEvent.code (physical key) to Windows virtual-key code, which is what
// Sunshine expects. Physical codes keep the host's own layout in charge.

const table: Record<string, number> = {
  Escape: 0x1b, Tab: 0x09, CapsLock: 0x14, Space: 0x20, Enter: 0x0d, Backspace: 0x08,
  ShiftLeft: 0xa0, ShiftRight: 0xa1, ControlLeft: 0xa2, ControlRight: 0xa3,
  AltLeft: 0xa4, AltRight: 0xa5, MetaLeft: 0x5b, MetaRight: 0x5c, OSLeft: 0x5b, OSRight: 0x5c,
  ContextMenu: 0x5d,
  Insert: 0x2d, Delete: 0x2e, Home: 0x24, End: 0x23, PageUp: 0x21, PageDown: 0x22,
  ArrowLeft: 0x25, ArrowUp: 0x26, ArrowRight: 0x27, ArrowDown: 0x28,
  PrintScreen: 0x2c, ScrollLock: 0x91, Pause: 0x13, NumLock: 0x90,
  Minus: 0xbd, Equal: 0xbb, BracketLeft: 0xdb, BracketRight: 0xdd, Backslash: 0xdc,
  Semicolon: 0xba, Quote: 0xde, Backquote: 0xc0, Comma: 0xbc, Period: 0xbe, Slash: 0xbf,
  IntlBackslash: 0xe2,
  NumpadMultiply: 0x6a, NumpadAdd: 0x6b, NumpadSubtract: 0x6d, NumpadDecimal: 0x6e,
  NumpadDivide: 0x6f, NumpadEnter: 0x0d,
  MediaTrackNext: 0xb0, MediaTrackPrevious: 0xb1, MediaStop: 0xb2, MediaPlayPause: 0xb3,
};
for (let i = 0; i < 26; i++) table[`Key${String.fromCharCode(65 + i)}`] = 0x41 + i;
for (let i = 0; i < 10; i++) {
  table[`Digit${i}`] = 0x30 + i;
  table[`Numpad${i}`] = 0x60 + i;
}
for (let i = 1; i <= 24; i++) table[`F${i}`] = 0x6f + i;

// ChromeOS: the Search key reports as MetaLeft and goes to the host as Super
// (Hyprland's main modifier). The top row's navigation keys act as F1-F5;
// Search+top-row already gives real F-keys. Brightness and volume keys are
// not in the table, so they stay with the laptop.
export const isChromeOS = /\bCrOS\b/.test(navigator.userAgent);
if (isChromeOS) {
  Object.assign(table, {
    BrowserBack: 0x70, BrowserForward: 0x71, BrowserRefresh: 0x72,
    ZoomToggle: 0x73, LaunchApp1: 0x74, ShowAllWindows: 0x74,
  });
}

export function vkFor(code: string): number | undefined {
  return table[code];
}

export const VK = {
  Escape: 0x1b, Tab: 0x09, Delete: 0x2e, F4: 0x73,
  LWin: 0x5b, LShift: 0xa0, LControl: 0xa2, LAlt: 0xa4,
} as const;
