// Run after npm run build. Exercise the real input listeners in an armed game.
import assert from "node:assert/strict";
import { Input } from "./dist/input.js";

globalThis.window = Object.assign(new EventTarget(), { setInterval: () => 0 });
globalThis.document = new EventTarget();
const video = new EventTarget();
let requests = 0, menu = false;
video.requestPointerLock = () => { requests++; };
document.exitPointerLock = () => { document.pointerLockElement = null; document.dispatchEvent(new Event("pointerlockchange")); };
const packets = [];
const input = new Input({readyState: "open", send: b => packets.push([...b])}, video, {
  armed() {}, modeChanged() {}, menuOpen: () => menu, hotkey: action => { if (action === "menu") menu = !menu; },
});
input.setMode("relative");
function key(code, ctrlKey, altKey, shiftKey) {
  window.dispatchEvent(Object.assign(new Event("keydown"), {code, ctrlKey, altKey, shiftKey}));
}
key("ControlLeft", true, false, false);
key("AltLeft", true, true, false);
key("ShiftLeft", true, true, true);
key("KeyH", true, true, true);
assert.equal(menu, true);
assert.equal(requests, 0);
// A lock already in flight must resolve to the local cursor while the menu is open.
document.pointerLockElement = video;
document.dispatchEvent(new Event("pointerlockchange"));
assert.equal(document.pointerLockElement, null);
assert.deepEqual(packets.filter(([type]) => type === 9), [[9, 0]]);
key("KeyH", true, true, true);
key("KeyW", false, false, false);
assert.equal(requests, 1);
input.stop();
