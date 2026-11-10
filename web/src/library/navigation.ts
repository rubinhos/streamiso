// Moving between the cards with the arrow keys (a TV remote, a keyboard): to the nearest card in that
// direction, weighing the distance across the direction more.
import { focusQuietly } from "../shared/format";

type Arrow = "ArrowUp" | "ArrowDown" | "ArrowLeft" | "ArrowRight";

/** Focuses the nearest card in that direction from the focused one; null if there is none. */
export function move(root: HTMLElement, key: Arrow): HTMLElement | null {
  const cards = Array.from(root.querySelectorAll<HTMLElement>(".card"));
  if (!cards.length) return null;
  const active = document.activeElement as HTMLElement | null;
  const item = active && active.closest ? active.closest(".item") : null;
  const cur = item ? item.querySelector<HTMLElement>(".card") : null;
  if (!cur) {
    cards[0].focus();
    return null;
  }
  const r = cur.getBoundingClientRect();
  const cx = r.left + r.width / 2;
  const cy = r.top + r.height / 2;
  let best: HTMLElement | null = null;
  let bestScore = Infinity;
  for (const c of cards) {
    if (c === cur) continue;
    const b = c.getBoundingClientRect();
    const dx = b.left + b.width / 2 - cx;
    const dy = b.top + b.height / 2 - cy;
    let ok: boolean, main: number, cross: number;
    if (key === "ArrowRight") { ok = dx > 10; main = dx; cross = Math.abs(dy); }
    else if (key === "ArrowLeft") { ok = dx < -10; main = -dx; cross = Math.abs(dy); }
    else if (key === "ArrowDown") { ok = dy > 10; main = dy; cross = Math.abs(dx); }
    else { ok = dy < -10; main = -dy; cross = Math.abs(dx); }
    if (!ok) continue;
    const score = main + cross * 2.5;
    if (score < bestScore) {
      bestScore = score;
      best = c;
    }
  }
  if (best) {
    best.focus();
    best.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  return best;
}

/** Focuses the card of a disc under root; false if it is not there. */
export function focusCard(root: HTMLElement | null, id: string): boolean {
  const c = root && root.querySelector<HTMLElement>('.item[data-id="' + id + '"] .card');
  if (!c) return false;
  focusQuietly(c);
  return true;
}

/** Focuses the card of a group under root. */
export function focusGroup(root: HTMLElement | null, key: string): void {
  const c = root && root.querySelector<HTMLElement>('.item[data-group="' + key.replace(/["\\]/g, "\\$&") + '"] .card');
  focusQuietly(c);
}
