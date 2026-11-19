// The floating pad that moves in a disc's menu, as the arrows, OK and Menu keys of a remote. Blu-ray menus are
// Java programs that ignore the mouse and touch, and on a phone the bar's Disc menu button has no room, so
// this is the way through them there. Its keys go to the disc through the player (the API's "nav" command),
// whatever has the keyboard focus. Drag it by its top edge so it does not cover the disc's buttons; a double
// click or tap there puts it back in its corner.
import { useRef, useState } from "preact/hooks";
import type { NavDirection, NavPadPos, PlayerController } from "./controller";

const ARROW: Record<Exclude<NavDirection, "activate">, string> = {
  up: "M7.4 15.4 12 10.8l4.6 4.6L18 14l-6-6-6 6z",
  down: "M7.4 8.6 12 13.2l4.6-4.6L18 10l-6 6-6-6z",
  left: "M15.4 16.6 10.8 12l4.6-4.6L14 6l-6 6 6 6z",
  right: "M8.6 16.6 13.2 12 8.6 7.4 10 6l6 6-6 6z",
};
const LABEL: Record<NavDirection, string> = { up: "Up", down: "Down", left: "Left", right: "Right", activate: "OK" };

// Keeps the pad inside the window (it may have been dragged on a bigger screen, or the phone turned).
function clamp(pos: NavPadPos, el: HTMLElement | null): NavPadPos {
  const w = el ? el.offsetWidth : 180;
  const h = el ? el.offsetHeight : 230;
  return {
    x: Math.max(0, Math.min(window.innerWidth - w, pos.x)),
    y: Math.max(0, Math.min(window.innerHeight - h, pos.y)),
  };
}

export function NavPad({ ctl }: { ctl: PlayerController }) {
  const ref = useRef<HTMLDivElement>(null);
  const drag = useRef<{ dx: number; dy: number; id: number } | null>(null);
  const [live, setLive] = useState<NavPadPos | null>(null); // while dragging
  const pos = live || ctl.s.navPadPos;
  const style = pos ? (() => { const p = clamp(pos, ref.current); return { left: p.x + "px", top: p.y + "px", right: "auto", bottom: "auto" }; })() : undefined;

  const key = (d: NavDirection, extra = "") => (
    <button type="button" class={"np-key np-" + d + extra} aria-label={LABEL[d]} title={LABEL[d]} onClick={() => ctl.nav(d)}>
      {d === "activate" ? "OK" : <svg viewBox="0 0 24 24"><path d={ARROW[d]} /></svg>}
    </button>
  );

  return (
    <div id="navpad" ref={ref} style={style} role="group" aria-label="Menu navigation">
      <div
        class="np-grip"
        title="Drag to move (double-click: back to the corner)"
        onPointerDown={(e) => {
          const rect = ref.current!.getBoundingClientRect();
          drag.current = { dx: e.clientX - rect.left, dy: e.clientY - rect.top, id: e.pointerId };
          try {
            (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
          } catch {
            /* ignore */
          }
          e.preventDefault();
        }}
        onPointerMove={(e) => {
          const d = drag.current;
          if (!d || d.id !== e.pointerId) return;
          setLive(clamp({ x: e.clientX - d.dx, y: e.clientY - d.dy }, ref.current));
        }}
        onPointerUp={() => {
          drag.current = null;
          if (live) ctl.moveNavPad(live);
          setLive(null);
        }}
        onPointerCancel={() => {
          drag.current = null;
          setLive(null);
        }}
        onDblClick={() => ctl.moveNavPad(null)}
      >
        <span />
      </div>
      <div class="np-cross">
        {key("up")}
        {key("left")}
        {key("activate")}
        {key("right")}
        {key("down")}
      </div>
      <div class="np-row">
        <button type="button" class="np-menu" title="The disc's menu (a Blu-ray's pop-up menu)" onClick={() => ctl.menu()}>
          <svg viewBox="0 0 24 24"><path d="M3 6h18v2H3zm0 5h18v2H3zm0 5h18v2H3z" /></svg>Menu
        </button>
        <button type="button" class="np-close" aria-label="Hide the menu pad" title="Hide (the pad button in the bar brings it back)"
          onClick={() => ctl.toggleNavPad()}>
          <svg viewBox="0 0 24 24"><path d="M19 6.4 17.6 5 12 10.6 6.4 5 5 6.4l5.6 5.6L5 17.6 6.4 19l5.6-5.6 5.6 5.6 1.4-1.4-5.6-5.6z" /></svg>
        </button>
      </div>
    </div>
  );
}
