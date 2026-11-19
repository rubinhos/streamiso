// Icon buttons carry their label in data-tip: CSS shows it on hover and on keyboard focus (player.css). A finger
// has no hover, so holding the button shows the label too, and letting go after such a hold does not press it.
// The state lives on the element, so it survives the view redrawing meanwhile.

const HOLD_MS = 450;

function hide(el: HTMLElement): void {
  clearTimeout(Number(el.dataset.tipTimer || 0));
  const held = el.dataset.tipHeld === "1";
  // after a hold, the label stays a moment so it can be read
  window.setTimeout(() => el.classList.remove("tip-show"), held ? 1200 : 0);
}

export const holdTip = {
  onPointerDown(e: PointerEvent): void {
    if (e.pointerType === "mouse") return;
    const el = e.currentTarget as HTMLElement;
    el.dataset.tipHeld = "";
    el.dataset.tipTimer = String(
      window.setTimeout(() => {
        el.dataset.tipHeld = "1";
        el.classList.add("tip-show");
      }, HOLD_MS),
    );
  },
  onPointerUp(e: PointerEvent): void {
    hide(e.currentTarget as HTMLElement);
  },
  onPointerCancel(e: PointerEvent): void {
    hide(e.currentTarget as HTMLElement);
  },
  onPointerLeave(e: PointerEvent): void {
    hide(e.currentTarget as HTMLElement);
  },
  /** A hold was for reading the label: the click that follows it is not a press. */
  onClickCapture(e: MouseEvent): void {
    const el = e.currentTarget as HTMLElement;
    if (el.dataset.tipHeld === "1") {
      el.dataset.tipHeld = "";
      e.preventDefault();
      e.stopImmediatePropagation(); // the button's own click listener is on this same element
    }
  },
  /** Phones open a context menu on a long press. */
  onContextMenu(e: MouseEvent): void {
    e.preventDefault();
  },
};
